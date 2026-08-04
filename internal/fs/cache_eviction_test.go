package fs

import (
	"context"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
)

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
		t.Errorf("Con 4 hits, TTL esperado %v, obtenido %v", expected4, ttl4)
	}

	// 8 hits → 5.0× TTL
	ttl8 := effectiveTTL(baseTTL, 8)
	expected8 := time.Duration(float64(baseTTL) * 5.0)
	if ttl8 != expected8 {
		t.Errorf("Con 8 hits, TTL esperado %v, obtenido %v", expected8, ttl8)
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

	// Simular 8 accesos
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
	cache := NewInodeCache()
	cache.SetBaseTTL(50 * time.Millisecond)

	// Insertar carpeta con children cacheados
	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})

	// Verify that it is cached
	if !parent.IsChildrenFetched() {
		t.Fatal("Setup: parent should have fetched children")
	}

	// Esperar a que el TTL expire (sin hits, el TTL base es 50ms)
	time.Sleep(100 * time.Millisecond)

	// Ejecutar sweep
	cache.evictExpiredChildren()

	// Verificar que los children fueron evictados
	if parent.IsChildrenFetched() {
		t.Error("After TTL eviction, children should be nil")
	}
	if cache.evictions.Load() != 1 {
		t.Errorf("Contador de evictions esperado 1, obtenido %d", cache.evictions.Load())
	}
}

func TestInodeCache_FrequencyExtendsTTL(t *testing.T) {
	cache := NewInodeCache()
	cache.SetBaseTTL(50 * time.Millisecond)

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "HotDocs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})

	// Simular 8 hits → TTL efectivo = 60ms * 5.0 = 300ms
	for i := 0; i < 8; i++ {
		parent.BumpChildrenAccess()
	}

	// Wait 100ms — with a base TTL of 50ms it would have expired, but with 8 hits
	// the effective TTL is 300ms, so it should survive
	time.Sleep(100 * time.Millisecond)

	cache.evictExpiredChildren()

	if !parent.IsChildrenFetched() {
		t.Error("With 8 hits, children should NOT have expired after 100ms (effective TTL = 300ms)")
	}

	// Tras varios decays, el accessCount baja y el TTL efectivo se reduce
	for i := 0; i < 5; i++ {
		parent.DecayChildrenAccess()
	}

	// Ahora accessCount ≈ 0 → TTL efectivo ≈ 50ms
	// Avanzar el tiempo para que expire
	parent.BumpChildrenAccess() // Reset lastAccess a now
	parent.Lock()
	parent.childrenAccessCount = 0 // Forzar accessCount a 0 (simula decay total)
	parent.Unlock()

	time.Sleep(100 * time.Millisecond)

	cache.evictExpiredChildren()

	if parent.IsChildrenFetched() {
		t.Error("Tras decay a 0 hits y TTL expirado, los children DEBERÍAN ser evictados")
	}
}

// ──── Eviction by size limit ────

func TestInodeCache_SizeLimitEviction(t *testing.T) {
	cache := NewInodeCache()
	cache.SetMaxEntries(2) // Solo 2 carpetas con hijos cacheados

	// Crear 4 carpetas con children
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

	// Crear 3 carpetas: lowFreq (1 hit), midFreq (5 hits), highFreq (10 hits)
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
	cache.SetMaxEntries(1) // Solo 1 carpeta con children

	// Crear 2 carpetas con diferente frecuencia
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

	// Crear 2 carpetas con mismo accessCount (empate por score)
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

	// Sus metadatos deben seguir intactos
	p := cache.Get("parent1")
	if p.Name() != "Docs" {
		t.Errorf("Name esperado 'Docs', obtenido %q", p.Name())
	}
	if !p.IsDir() {
		t.Error("IsDir should still be true")
	}

	// Pero children debe ser nil
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

	fetch := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
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

	// Segundo GetChildren: cache hit, debe incrementar accessCount
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
	cache := NewInodeCache()
	cache.SetBaseTTL(1 * time.Nanosecond) // TTL inmediato
	cache.SetMaxEntries(0)                // no size limit

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})

	// ForceSweep sin esperar al tick
	cache.ForceSweep()

	if parent.IsChildrenFetched() {
		t.Error("After ForceSweep with a 1ns TTL, children should be evicted")
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

	// Verificar que NINGUNA fue evictada (maxEntries=0 = unlimited)
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

func TestInodeCache_StartSweep_StopOnClose(t *testing.T) {
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

	// Segunda llamada a Close no debe bloquearse (stopCh ya es nil)
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

	// Sweep con BoltDB activo
	cache.ForceSweep()

	if parent.IsChildrenFetched() {
		t.Error("After sweep, children should be nil")
	}

	// Close con BoltDB: debe serializar y cerrar sin panic
	if err := cache.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
}
