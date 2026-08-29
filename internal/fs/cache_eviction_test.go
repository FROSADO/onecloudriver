package fs

import (
	"context"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
)

type fakeClock struct {
	current time.Time
}

func (fc *fakeClock) Now() time.Time {
	return fc.current
}
func (fc *fakeClock) Advance(d time.Duration) {
	fc.current = fc.current.Add(d)
}

// ──── effectiveTTL ────

func TestEffectiveTTL_BaseCase(t *testing.T) {
	baseTTL := 60 * time.Second
	ttl := effectiveTTL(baseTTL, 0)
	if ttl != 60*time.Second {
		t.Errorf("With 0 accesses, TTL should be 60s, got %v", ttl)
	}
}

func TestEffectiveTTL_GrowsWithFrequency(t *testing.T) {
	baseTTL := 60 * time.Second

	// 4 hits → 3.0× TTL
	ttl4 := effectiveTTL(baseTTL, 4)
	expected4 := time.Duration(float64(baseTTL) * 3.0)
	if ttl4 != expected4 {
		t.Errorf("With 4 hits, expected TTL %v, got %v", expected4, ttl4)
	}

	// 8 hits → 5.0× TTL
	ttl8 := effectiveTTL(baseTTL, 8)
	expected8 := time.Duration(float64(baseTTL) * 5.0)
	if ttl8 != expected8 {
		t.Errorf("With 8 hits, expected TTL %v, got %v", expected8, ttl8)
	}
}

func TestEffectiveTTL_MaxCapped(t *testing.T) {
	baseTTL := 60 * time.Second
	ttl := effectiveTTL(baseTTL, 100) // 100 hits → multiplicador 51.0, cap a 20.0
	expected := time.Duration(float64(baseTTL) * freqMultiplierMax)
	if ttl != expected {
		t.Errorf("With 100 hits, TTL should be capped at %v, got %v", expected, ttl)
	}
}

// ──── Children access tracking ────

func TestInode_BumpChildrenAccess(t *testing.T) {
	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 2},
	})
	parent.SetChildren([]string{"a", "b"})

	initialAccess := parent.ChildrenAccessCount()
	if initialAccess != 0 {
		t.Errorf("Initial AccessCount should be 0, got %d", initialAccess)
	}

	parent.BumpChildrenAccess()
	if parent.ChildrenAccessCount() != 1 {
		t.Errorf("After 1 hit, AccessCount should be 1, got %d", parent.ChildrenAccessCount())
	}
}

func TestInode_DecayChildrenAccess(t *testing.T) {
	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{},
	})
	parent.SetChildren([]string{})

	// Simulate 8 accesses
	for i := 0; i < 8; i++ {
		parent.BumpChildrenAccess()
	}
	if parent.ChildrenAccessCount() != 8 {
		t.Fatalf("Setup: AccessCount should be 8, got %d", parent.ChildrenAccessCount())
	}

	// Decay: 8 >> 1 = 4
	parent.DecayChildrenAccess()
	if parent.ChildrenAccessCount() != 4 {
		t.Errorf("After decay, AccessCount should be 4, got %d", parent.ChildrenAccessCount())
	}

	// Segundo decay: 4 >> 1 = 2
	parent.DecayChildrenAccess()
	if parent.ChildrenAccessCount() != 2 {
		t.Errorf("After second decay, AccessCount should be 2, got %d", parent.ChildrenAccessCount())
	}
}

// ──── Eviction by expired TTL ────

func TestInodeCache_EvictExpiredChildren(t *testing.T) {
	clock := &fakeClock{
		current: time.Now(),
	}

	cache := NewInodeCache()
	cache.now = clock.Now
	cache.SetBaseTTL(50 * time.Second)

	// Insert folder with cached children
	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})

	// Verify that it is cached
	if !parent.IsChildrenFetched() {
		t.Fatal("Setup: parent should have fetched children")
	}

	// Advance time beyond TTL
	clock.Advance(100 * time.Second)

	// Ejecutar sweep (paridad: full scan)
	cache.evictExpiredChildrenFullScan()
	// Verify that the children were evicted
	if parent.IsChildrenFetched() {
		t.Error("After TTL eviction, children should be nil")
	}
	if cache.evictions.Load() != 1 {
		t.Errorf("Expected eviction counter 1, got %d", cache.evictions.Load())
	}
}

func TestInodeCache_FrequencyExtendsTTL(t *testing.T) {
	clock := &fakeClock{
		current: time.Now(),
	}
	cache := NewInodeCache()
	cache.now = clock.Now
	cache.SetBaseTTL(300 * time.Millisecond)

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "HotDocs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})

	// Simular 8 hits → multiplicador inicial = 5.0.
	for i := 0; i < 8; i++ {
		parent.BumpChildrenAccess()
	}

	// El sweep aplica decay: 8 hits pasan a 4.
	// TTL efectivo posterior al decay = 300ms * 3 = 900ms.
	clock.Advance(100 * time.Millisecond)

	cache.evictExpiredChildrenFullScan()

	if !parent.IsChildrenFetched() {
		t.Error("With 8 hits, children should NOT have expired after 100ms (effective TTL = 300ms)")
	}

	// Tras varios decays, el accessCount baja y el TTL efectivo se reduce
	for i := 0; i < 5; i++ {
		parent.DecayChildrenAccess()
	}

	// Ahora accessCount = 0 → TTL efectivo = 300ms.
	// El reloj del cache debe avanzar explícitamente; Sleep no modifica clock.
	parent.BumpChildrenAccess() // Actualiza lastAccess con el reloj real.
	parent.Lock()
	parent.childrenAccessCount = 0 // Forzar accessCount a 0 (simula decay total).
	parent.Unlock()

	clock.Advance(1 * time.Second)
	cache.evictExpiredChildrenFullScan()

	if parent.IsChildrenFetched() {
		t.Error("After decay to 0 hits and TTL expiry, the children SHOULD have been evicted")
	}
}

// ──── Eviction by size limit ────

func TestInodeCache_SizeLimitEviction(t *testing.T) {
	cache := NewInodeCache()
	cache.SetMaxEntries(2) // Only 2 folders with cached children

	// Create 4 folders with children
	for i, name := range []string{"A", "B", "C", "D"} {
		parent := NewInodeDriveItem(&graph.DriveItem{
			ID: name, Name: name, Folder: &graph.Folder{ChildCount: 1},
		})
		cache.Insert(parent)
		parent.SetChildren([]string{"child_" + name})

		// Dar diferentes accessCounts: A=1, B=2, C=3, D=4
		for j := 0; j <= i; j++ {
			parent.BumpChildrenAccess()
		}
	}

	// Run size-based eviction
	cache.evictChildrenBySizeLimit()

	// Count how many still have children
	survivors := 0
	evicted := 0
	for _, name := range []string{"A", "B", "C", "D"} {
		if p := cache.Get(name); p != nil && p.IsChildrenFetched() {
			survivors++
		} else {
			evicted++
		}
	}

	if survivors > 2 {
		t.Errorf("At most 2 folders should keep their children, %d remain", survivors)
	}
	if evicted < 2 {
		t.Errorf("At least 2 folders should be evicted, %d were evicted", evicted)
	}
}

func TestInodeCache_LowFrequencyEvictedFirst(t *testing.T) {
	cache := NewInodeCache()
	cache.SetMaxEntries(2)

	// Create 3 folders: lowFreq (1 hit), midFreq (5 hits), highFreq (10 hits)
	low := NewInodeDriveItem(&graph.DriveItem{
		ID: "low", Name: "Low", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(low)
	low.SetChildren([]string{"c_low"})
	low.BumpChildrenAccess() // 1 hit

	mid := NewInodeDriveItem(&graph.DriveItem{
		ID: "mid", Name: "Mid", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(mid)
	mid.SetChildren([]string{"c_mid"})
	for i := 0; i < 5; i++ {
		mid.BumpChildrenAccess()
	}

	high := NewInodeDriveItem(&graph.DriveItem{
		ID: "high", Name: "High", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(high)
	high.SetChildren([]string{"c_high"})
	for i := 0; i < 10; i++ {
		high.BumpChildrenAccess()
	}

	cache.evictChildrenBySizeLimit()

	// The least frequently used (low) should be evicted
	if cache.Get("low") != nil && cache.Get("low").IsChildrenFetched() {
		t.Error("low (1 hit) should be evicted first (lowest score)")
	}
	// The high-frequency ones should be kept
	if cache.Get("mid") == nil || !cache.Get("mid").IsChildrenFetched() {
		t.Error("mid (5 hits) should keep its children")
	}
	if cache.Get("high") == nil || !cache.Get("high").IsChildrenFetched() {
		t.Error("high (10 hits) should keep its children")
	}
}

func TestInodeCache_HighFrequencySurvivesSizeLimit(t *testing.T) {
	cache := NewInodeCache()
	cache.SetMaxEntries(1) // Only 1 folder with children

	// Create 2 folders with different frequency
	stale := NewInodeDriveItem(&graph.DriveItem{
		ID: "stale", Name: "Stale", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(stale)
	stale.SetChildren([]string{"c_stale"})

	hot := NewInodeDriveItem(&graph.DriveItem{
		ID: "hot", Name: "Hot", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(hot)
	hot.SetChildren([]string{"c_hot"})
	for i := 0; i < 50; i++ {
		hot.BumpChildrenAccess()
	}

	cache.evictChildrenBySizeLimit()

	// hot (50 hits) should survive; stale (0 hits) should be evicted
	if cache.Get("hot") == nil || !cache.Get("hot").IsChildrenFetched() {
		t.Error("hot (50 hits) should survive size-based eviction")
	}
	if cache.Get("stale") != nil && cache.Get("stale").IsChildrenFetched() {
		t.Error("stale (0 hits) should be evicted")
	}
}

func TestInodeCache_SizeLimit_WithTiebreaker(t *testing.T) {
	cache := NewInodeCache()
	cache.SetMaxEntries(1)

	// Create 2 folders with the same accessCount (tie on score)
	older := NewInodeDriveItem(&graph.DriveItem{
		ID: "older", Name: "Older", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(older)
	older.SetChildren([]string{"c_older"})
	older.BumpChildrenAccess()

	newer := NewInodeDriveItem(&graph.DriveItem{
		ID: "newer", Name: "Newer", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(newer)
	newer.SetChildren([]string{"c_newer"})
	newer.BumpChildrenAccess()

	// Ambos tienen accessCount=1 y lastAccess similar → empate por score.
	// The tiebreaker: the oldest (older) is evicted first.
	cache.evictChildrenBySizeLimit()

	if cache.Get("older") != nil && cache.Get("older").IsChildrenFetched() {
		t.Error("older (oldest) should be evicted on a tie")
	}
	if cache.Get("newer") == nil || !cache.Get("newer").IsChildrenFetched() {
		t.Error("newer (most recent) should survive on a tie")
	}
}

// ──── Eviction does not delete the Inode ────

func TestInodeCache_EvictionDoesNotRemoveInodeFromTree(t *testing.T) {
	cache := NewInodeCache()

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})

	// Evictar children
	parent.EvictChildren()

	// The Inode MUST keep existing in the cache
	if cache.Get("parent1") == nil {
		t.Error("Evicting children must NOT remove the Inode from the tree")
	}

	// Its metadata must remain intact
	p := cache.Get("parent1")
	if p.Name() != "Docs" {
		t.Errorf("Expected Name 'Docs', got %q", p.Name())
	}
	if !p.IsDir() {
		t.Error("IsDir should still be true")
	}

	// But children must be nil
	if p.IsChildrenFetched() {
		t.Error("After eviction, IsChildrenFetched should be false")
	}
}

// ──── Access tracking en GetChildren ────

func TestInodeCache_GetChildren_BumpsAccessCount(t *testing.T) {
	cache := NewInodeCache()

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{})

	fetch := func(ctx context.Context, _ string) ([]graph.DriveItem, error) {
		return []graph.DriveItem{
			{ID: "file1", Name: "doc.txt", Size: 100},
		}, nil
	}

	// Primer GetChildren: fetch (miss)
	_, err := cache.GetChildren(context.Background(), "parent1", fetch)
	if err != nil {
		t.Fatalf("Setup error: %v", err)
	}
	accessAfterSetup := parent.ChildrenAccessCount()

	// Second GetChildren: cache hit, must increment accessCount
	_, err = cache.GetChildren(context.Background(), "parent1", fetch)
	if err != nil {
		t.Fatalf("GetChildren error: %v", err)
	}

	if parent.ChildrenAccessCount() != accessAfterSetup+1 {
		t.Errorf("AccessCount should increment on a cache hit. Before: %d, After: %d",
			accessAfterSetup, parent.ChildrenAccessCount())
	}
}

// ──── ForceSweep ────

func TestInodeCache_ForceSweep(t *testing.T) {
	clock := &fakeClock{
		current: time.Now(),
	}
	cache := NewInodeCache()
	cache.now = clock.Now
	cache.SetBaseTTL(1 * time.Minute) // TTL de un minuto
	cache.SetMaxEntries(0)            // no size limit

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})
	cache.registerTTL(parent, clock.Now()) // sweep() usa el anillo desde la Fase 6

	// ForceSweep without waiting for the tick
	cache.ForceSweep()
	if !parent.IsChildrenFetched() {
		t.Error("After ForceSweep with a 1min TTL, children should NOT be evicted")
	}
	clock.Advance(2 * time.Minute) // Avanzar más allá del TTL
	cache.ForceSweep()
	if parent.IsChildrenFetched() {
		t.Error("After ForceSweep with a 1min TTL, children should be evicted")
	}
}

func TestInodeCache_ForceSweep_DoesNotEvictFreshChildren(t *testing.T) {
	clock := &fakeClock{current: time.Now()}
	cache := NewInodeCache()
	cache.now = clock.Now
	cache.SetBaseTTL(time.Minute)
	cache.SetMaxEntries(0)

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})
	cache.registerTTL(parent, clock.Now())

	clock.Advance(30 * time.Second)
	cache.ForceSweep()

	if !parent.IsChildrenFetched() {
		t.Fatal("ForceSweep should keep fresh children")
	}
	if got := cache.Stats().Evictions; got != 0 {
		t.Fatalf("expected no evictions, got %d", got)
	}
}

func TestInodeCache_ForceSweep_KeepsInode(t *testing.T) {
	clock := &fakeClock{current: time.Now()}
	cache := NewInodeCache()
	cache.now = clock.Now
	cache.SetBaseTTL(time.Minute)
	cache.SetMaxEntries(0)

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})
	cache.registerTTL(parent, clock.Now())

	clock.Advance(2 * time.Minute)
	cache.ForceSweep()

	if cache.Get("parent1") == nil {
		t.Fatal("ForceSweep should not remove the inode")
	}
	if parent.IsChildrenFetched() {
		t.Error("ForceSweep should evict only the children")
	}
}

func TestInodeCache_ForceSweep_IncrementsEvictionCount(t *testing.T) {
	clock := &fakeClock{current: time.Now()}
	cache := NewInodeCache()
	cache.now = clock.Now
	cache.SetBaseTTL(time.Minute)
	cache.SetMaxEntries(0)

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})
	cache.registerTTL(parent, clock.Now())

	clock.Advance(2 * time.Minute)
	cache.ForceSweep()

	if got := cache.Stats().Evictions; got != 1 {
		t.Fatalf("expected 1 eviction, got %d", got)
	}

	cache.ForceSweep()

	if got := cache.Stats().Evictions; got != 1 {
		t.Fatalf("second sweep should not increment evictions, got %d", got)
	}
}

func TestInodeCache_ForceSweep_IgnoresUnfetchedInodes(t *testing.T) {
	cache := NewInodeCache()
	cache.SetBaseTTL(time.Nanosecond)
	cache.SetMaxEntries(0)

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)

	cache.ForceSweep()

	if cache.Get("parent1") == nil {
		t.Fatal("unfetched inode should remain in cache")
	}
	if got := cache.Stats().Evictions; got != 0 {
		t.Fatalf("expected no evictions, got %d", got)
	}
}

func TestInodeCache_SizeLimit_ZeroMeansUnlimited(t *testing.T) {
	cache := NewInodeCache()
	cache.SetMaxEntries(0) // 0 = no limit

	names := []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J"}
	for _, name := range names {
		parent := NewInodeDriveItem(&graph.DriveItem{
			ID: name, Name: name, Folder: &graph.Folder{ChildCount: 0},
		})
		cache.Insert(parent)
		parent.SetChildren([]string{})
	}

	cache.evictChildrenBySizeLimit()

	// Verify that NONE were evicted (maxEntries=0 = unlimited)
	evicted := 0
	for _, name := range names {
		if p := cache.Get(name); p == nil || !p.IsChildrenFetched() {
			evicted++
		}
	}
	if evicted > 0 {
		t.Errorf("With maxEntries=0, no folder should be evicted. Evicted: %d", evicted)
	}
}

// ──── Close detiene sweep ────

func TestInodeCache_StartSweep_StopOnClose(_ *testing.T) {
	cache := NewInodeCache()
	cache.SetBaseTTL(10 * time.Millisecond)

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})

	cache.StartSweep()

	// Close the cache — must stop the sweep goroutine without panicking
	cache.Close()

	// A second Close call must not block (stopCh is already nil)
	cache.Close()
}

// ──── Sweep con BoltDB sigue funcionando ────

func TestInodeCache_Sweep_WithBoltDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"

	cache := NewInodeCache()
	cache.SetBaseTTL(1 * time.Nanosecond)

	if err := cache.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})
	cache.registerTTL(parent, cache.currentTime()) // sweep() usa el anillo desde la Fase 6

	// Sweep con BoltDB activo
	cache.ForceSweep()

	if parent.IsChildrenFetched() {
		t.Error("After sweep, children should be nil")
	}

	// Close with BoltDB: must serialize and close without panic
	if err := cache.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
}

// ──── registerTTL ────

func TestInodeCache_RegisterTTL(t *testing.T) {
	clock := &fakeClock{current: time.Now()}
	cache := NewInodeCache()
	cache.now = clock.Now
	cache.SetBaseTTL(time.Minute)

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)

	// Without fetched children: nothing should be scheduled.
	cache.registerTTL(parent, clock.Now())
	assertTTLEntries(t, cache, 0)

	parent.SetChildren([]string{"child1"})
	parent.BumpChildrenAccess() // accessCount = 1 → TTL = 1m × 1.5 = 90s

	cache.registerTTL(parent, clock.Now())

	// The bucket derives from the expiry (ChildrenLastAccess + TTL), which
	// SetChildren recorded with the real clock — close to clock.current.
	ttl := effectiveTTL(cache.BaseTTL(), parent.ChildrenAccessCount())
	expiry := parent.ChildrenLastAccess().Add(ttl)
	bucket := ttlBucketIndex(expiry)

	cache.ttlMu.Lock()
	entries := append([]ttlEntry(nil), cache.ttlBuckets[bucket]...)
	cache.ttlMu.Unlock()

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry in bucket %d, got %d", bucket, len(entries))
	}
	if entries[0].inodeID != "parent1" {
		t.Errorf("expected inodeID parent1, got %s", entries[0].inodeID)
	}
	if !entries[0].expiry.Equal(expiry) {
		t.Errorf("expected expiry %v, got %v", expiry, entries[0].expiry)
	}
}

func TestInodeCache_RegisterTTL_AllowsDuplicates(t *testing.T) {
	// Re-registering after an access moves the folder to the bucket of its
	// new deadline, leaving the old entry behind (lazy invalidation).
	clock := &fakeClock{current: time.Now()}
	cache := NewInodeCache()
	cache.now = clock.Now
	cache.SetBaseTTL(time.Minute)

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})

	cache.registerTTL(parent, clock.Now())
	clock.Advance(10 * time.Second)
	parent.BumpChildrenAccess()
	cache.registerTTL(parent, clock.Now())

	assertTTLEntries(t, cache, 2)
}

func TestInodeCache_Insert_RegistersSeededParent(t *testing.T) {
	clock := &fakeClock{current: time.Now()}
	cache := NewInodeCache()
	cache.now = clock.Now
	cache.SetBaseTTL(time.Minute)

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	assertTTLEntries(t, cache, 0) // no children yet: nothing to schedule

	// Inserting a child whose parent has no cached children seeds the list
	// (attachChild seed=true) → the parent becomes a TTL candidate.
	child := NewInodeDriveItem(&graph.DriveItem{
		ID: "child1", Name: "c1", Parent: &graph.DriveItemParent{ID: "parent1"},
	})
	cache.Insert(child)

	if !parent.IsChildrenFetched() {
		t.Fatal("parent should be seeded with the child")
	}
	assertTTLEntries(t, cache, 1)
}

func TestInodeCache_Insert_DoesNotDoubleRegister(t *testing.T) {
	clock := &fakeClock{current: time.Now()}
	cache := NewInodeCache()
	cache.now = clock.Now
	cache.SetBaseTTL(time.Minute)

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "child1", Name: "c1", Parent: &graph.DriveItemParent{ID: "parent1"},
	}))

	// A second insert into an already-seeded parent must not add a new entry:
	// the folder keeps its existing registration (its lastAccess did not change).
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "child2", Name: "c2", Parent: &graph.DriveItemParent{ID: "parent1"},
	}))

	assertTTLEntries(t, cache, 1)
}

func TestInodeCache_MoveChild_RegistersSeededParent(t *testing.T) {
	clock := &fakeClock{current: time.Now()}
	cache := NewInodeCache()
	cache.now = clock.Now
	cache.SetBaseTTL(time.Minute)

	oldParent := NewInodeDriveItem(&graph.DriveItem{ID: "old", Name: "Old", Folder: &graph.Folder{ChildCount: 1}})
	newParent := NewInodeDriveItem(&graph.DriveItem{ID: "new", Name: "New", Folder: &graph.Folder{ChildCount: 1}})
	cache.Insert(oldParent)
	cache.Insert(newParent)
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "child1", Name: "c1", Parent: &graph.DriveItemParent{ID: "old"},
	})) // seeds oldParent → 1 entry
	assertTTLEntries(t, cache, 1)

	cache.MoveChild("old", "new", "child1")

	// newParent was not fetched → seeded by the move → registered.
	if !newParent.IsChildrenFetched() {
		t.Fatal("newParent should be seeded with the child")
	}
	assertTTLEntries(t, cache, 2) // oldParent (empty list) + newParent
}

// assertTTLEntries checks the total number of entries across all TTL buckets.
func assertTTLEntries(t *testing.T, cache *InodeCache, want int) {
	t.Helper()
	total := 0
	cache.ttlMu.Lock()
	for bucket := range cache.ttlBuckets {
		total += len(cache.ttlBuckets[bucket])
	}
	cache.ttlMu.Unlock()
	if total != want {
		t.Fatalf("expected %d TTL entries, got %d", want, total)
	}
}

// option-1 design.
func seededTTLInode(cache *InodeCache, id string) *Inode {
	inode := NewInodeDriveItem(&graph.DriveItem{
		ID: id, Name: "Folder " + id, Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(inode)
	inode.SetChildren([]string{"child_of_" + id})
	cache.registerTTL(inode, cache.currentTime())
	return inode
}

// TestInodeCache_SweepExpiredBucket_Evicts when the expiry has passed, the
// bucket sweep evicts the children and increments the counter.
func TestInodeCache_SweepExpiredBucket_Evicts(t *testing.T) {
	clock := &fakeClock{current: time.Now()}
	cache := NewInodeCache()
	cache.now = clock.Now
	cache.SetBaseTTL(1 * time.Minute)
	cache.SetMaxEntries(0)

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})
	cache.registerTTL(parent, clock.Now())

	// Let 2 minutes pass so parent expires.
	clock.Advance(2 * time.Minute)
	cache.sweepExpiredBucket(clock.Now())

	if parent.IsChildrenFetched() {
		t.Error("expired folder should have its children evicted")
	}
	if ev := cache.Stats().Evictions; ev != 1 {
		t.Errorf("expected exactly 1 eviction, got %d", ev)
	}
	if cache.Get("parent1") == nil {
		t.Error("the inode itself must remain in the cache")
	}
}

// TestInodeCache_SweepExpiredBucket_FreshReRegisters checks that a folder still
// inside its window keeps its children and is re-registered (bucket of its new
// post-decay deadline).
func TestInodeCache_SweepExpiredBucket_FreshReRegisters(t *testing.T) {
	clock := &fakeClock{current: time.Now()}
	cache := NewInodeCache()
	cache.now = clock.Now
	cache.SetBaseTTL(1 * time.Minute)
	cache.SetMaxEntries(0)

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})
	cache.registerTTL(parent, clock.Now())
	assertTTLEntries(t, cache, 1)

	// Advance well inside the window: the parent stays fetched and becomes a
	// fresh re-registration rather than an eviction.
	clock.Advance(5 * time.Second)
	cache.sweepExpiredBucket(clock.Now())

	if !parent.IsChildrenFetched() {
		t.Error("fresh folder should not be evicted")
	}
	if ev := cache.Stats().Evictions; ev != 0 {
		t.Errorf("expected no evictions, got %d", ev)
	}
	assertTTLEntries(t, cache, 1) // re-registered
}

// TestInodeCache_SweepExpiredBucket_DeletedInode verifies a stale entry whose
// inode was removed produces no eviction and no panic.
func TestInodeCache_SweepExpiredBucket_DeletedInode(t *testing.T) {
	clock := &fakeClock{current: time.Now()}
	cache := NewInodeCache()
	cache.now = clock.Now
	cache.SetBaseTTL(1 * time.Minute)
	cache.SetMaxEntries(0)

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})
	cache.registerTTL(parent, clock.Now())
	cache.Delete("parent1")

	clock.Advance(2 * time.Minute)
	cache.sweepExpiredBucket(clock.Now())

	if ev := cache.Stats().Evictions; ev != 0 {
		t.Errorf("deleted inode should not produce evictions, got %d", ev)
	}
	assertTTLEntries(t, cache, 0) // bucket drained
}

// TestInodeCache_SweepExpiredBucket_InvalidatedInode verifies an invalidated
// folder (children reset, IsChildrenFetched false) is skipped.
func TestInodeCache_SweepExpiredBucket_InvalidatedInode(t *testing.T) {
	clock := &fakeClock{current: time.Now()}
	cache := NewInodeCache()
	cache.now = clock.Now
	cache.SetBaseTTL(1 * time.Minute)
	cache.SetMaxEntries(0)

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})
	cache.registerTTL(parent, clock.Now())
	cache.Invalidate("parent1")

	clock.Advance(2 * time.Minute)
	cache.sweepExpiredBucket(clock.Now())

	if ev := cache.Stats().Evictions; ev != 0 {
		t.Errorf("invalidated inode should not produce evictions, got %d", ev)
	}
	assertTTLEntries(t, cache, 0)
}

// TestInodeCache_SweepExpiredBucket_DecaysOnce verifies that two duplicate
// entries of the same inode in the same bucket decay the counter only once
// (8 → 4, not 8 → 2).
func TestInodeCache_SweepExpiredBucket_DecaysOnce(t *testing.T) {
	clock := &fakeClock{current: time.Now()}
	cache := NewInodeCache()
	cache.now = clock.Now
	cache.SetBaseTTL(time.Minute)
	cache.SetMaxEntries(0)

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})
	for i := 0; i < 8; i++ {
		parent.BumpChildrenAccess()
	}
	// Two duplicate entries in the same bucket (two hits in the same second).
	cache.registerTTL(parent, clock.Now())
	cache.registerTTL(parent, clock.Now())

	clock.Advance(2 * time.Minute)
	cache.sweepExpiredBucket(clock.Now())

	// Decayed once: 8 >> 1 = 4.
	if got := parent.ChildrenAccessCount(); got != 4 {
		t.Errorf("expected a single decay (8 → 4), got %d", got)
	}
}

// TestInodeCache_SweepExpiredBuckets_SeveralInodes evicts only the expired ones
// and keeps the inodes themselves (5.4).
func TestInodeCache_SweepExpiredBuckets_SeveralInodes(t *testing.T) {
	clock := &fakeClock{current: time.Now()}
	cache := NewInodeCache()
	cache.now = clock.Now
	cache.SetBaseTTL(time.Minute)
	cache.SetMaxEntries(0)

	expired := seededTTLInode(cache, "expired")
	fresh := seededTTLInode(cache, "fresh")
	// 8 hits → TTL 300s → after the sweep's decay (8→4) still 180s, enough
	// to survive the 70s jump. expired stays at 0 hits → 60s → evicted.
	for i := 0; i < 8; i++ {
		fresh.BumpChildrenAccess()
	}

	// Establish lastSweep with a first pass (expired keeps 0 hits / 60s).
	cache.sweepExpiredBuckets(clock.Now())

	// A jump beyond the 60s ring window sweeps every bucket.
	clock.Advance(70 * time.Second)
	cache.sweepExpiredBuckets(clock.Now())

	if expired.IsChildrenFetched() {
		t.Error("expired folder should be evicted")
	}
	if !fresh.IsChildrenFetched() {
		t.Error("fresh folder should keep its children")
	}
	if cache.Get("expired") == nil || cache.Get("fresh") == nil {
		t.Error("inodes must remain in the cache")
	}
}

// TestInodeCache_SweepExpiredBuckets_TimeJump verifies 5.5: a jump larger than
// the 60s ring window sweeps every bucket.
func TestInodeCache_SweepExpiredBuckets_TimeJump(t *testing.T) {
	clock := &fakeClock{current: time.Now()}
	cache := NewInodeCache()
	cache.now = clock.Now
	cache.SetBaseTTL(time.Minute)
	cache.SetMaxEntries(0)

	seededTTLInode(cache, "a")
	seededTTLInode(cache, "b")
	assertTTLEntries(t, cache, 2)

	// Two minutes is beyond the one-minute window → all buckets swept.
	clock.Advance(2 * time.Minute)
	cache.sweepExpiredBuckets(clock.Now())

	assertTTLEntries(t, cache, 0) // both buckets drained (entries evicted)
	if ev := cache.Stats().Evictions; ev != 2 {
		t.Errorf("expected 2 evictions after a time jump, got %d", ev)
	}
}
