package fs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
)

// mockTokenProvider for tests
type mockTokenProvider struct {
	token string
}

func (m *mockTokenProvider) GetAccessToken(ctx context.Context) (string, error) {
	return m.token, nil
}

// ──── InodeCache: Get / Insert / Delete ────

func TestInodeCache_InsertAndGet(t *testing.T) {
	cache := NewInodeCache()

	inode := NewInodeDriveItem(&graph.DriveItem{ID: "item1", Name: "test.txt"})
	cache.Insert(inode)

	got := cache.Get("item1")
	if got == nil {
		t.Fatal("Get returned nil for an inserted item")
	}
	if got.ID() != "item1" {
		t.Errorf("Expected ID 'item1', got %q", got.ID())
	}
	if got.Name() != "test.txt" {
		t.Errorf("Expected Name 'test.txt', got %q", got.Name())
	}
}

func TestInodeCache_Get_Missing(t *testing.T) {
	cache := NewInodeCache()

	if got := cache.Get("nonexistent"); got != nil {
		t.Error("Get should return nil for nonexistent item")
	}
}

func TestInodeCache_InsertNil(t *testing.T) {
	cache := NewInodeCache()
	cache.Insert(nil) // should not panic

	if stats := cache.Stats(); stats.InodeCount != 0 {
		t.Errorf("Expected InodeCount 0, got %d", stats.InodeCount)
	}
}

func TestInodeCache_Delete(t *testing.T) {
	cache := NewInodeCache()

	inode := NewInodeDriveItem(&graph.DriveItem{ID: "item1", Name: "test.txt"})
	cache.Insert(inode)
	cache.Delete("item1")

	if got := cache.Get("item1"); got != nil {
		t.Error("Get should return nil after Delete")
	}
}

func TestInodeCache_Delete_Nonexistent(t *testing.T) {
	cache := NewInodeCache()
	cache.Delete("nonexistent") // should not panic
}

func TestInodeCache_Insert_UpdatesParentChildren(t *testing.T) {
	cache := NewInodeCache()

	// Create parent
	parent := NewInodeDriveItem(&graph.DriveItem{
		ID:     "parent1",
		Name:   "Documents",
		Folder: &graph.Folder{ChildCount: 0},
	})
	cache.Insert(parent)

	// Insert child with ParentID pointing to parent
	child := NewInodeDriveItem(&graph.DriveItem{
		ID:     "child1",
		Name:   "file.txt",
		Parent: &graph.DriveItemParent{ID: "parent1"},
	})
	cache.Insert(child)

	// Verify parent now has the child in children
	updatedParent := cache.Get("parent1")
	if updatedParent == nil {
		t.Fatal("Parent not found")
	}
	if !updatedParent.HasChildren() {
		t.Error("Parent should have children after Insert of child")
	}
	children := updatedParent.Children()
	if len(children) != 1 || children[0] != "child1" {
		t.Errorf("Expected children ['child1'], got %v", children)
	}
}

func TestInodeCache_Delete_UpdatesParentChildren(t *testing.T) {
	cache := NewInodeCache()

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID:     "parent1",
		Name:   "Documents",
		Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)

	child := NewInodeDriveItem(&graph.DriveItem{
		ID:     "child1",
		Name:   "file.txt",
		Parent: &graph.DriveItemParent{ID: "parent1"},
	})
	cache.Insert(child)

	// Verify parent has the child
	if p := cache.Get("parent1"); len(p.Children()) != 1 {
		t.Fatal("Setup: parent should have 1 child")
	}

	cache.Delete("child1")

	// Verify parent no longer has the child
	updatedParent := cache.Get("parent1")
	if len(updatedParent.Children()) != 0 {
		t.Error("Parent should have 0 children after Delete of child")
	}
}

// ──── GetChildren ────

func TestInodeCache_GetChildren_WithFetcher(t *testing.T) {
	cache := NewInodeCache()

	callCount := 0
	fetch := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		callCount++
		return []graph.DriveItem{
			{ID: "file1", Name: "a.txt", Size: 100},
			{ID: "file2", Name: "b.txt", Size: 200},
		}, nil
	}

	children, err := cache.GetChildren(context.Background(), "parent1", fetch)
	if err != nil {
		t.Fatalf("GetChildren error: %v", err)
	}
	if len(children) != 2 {
		t.Errorf("Expected 2 children, got %d", len(children))
	}
	if callCount != 1 {
		t.Errorf("Expected 1 call to fetcher, got %d", callCount)
	}

	// Child inodes should be in cache
	if cache.Get("file1") == nil {
		t.Error("file1 should be in cache")
	}
	if cache.Get("file2") == nil {
		t.Error("file2 should be in cache")
	}
}

// TestInodeCache_GetChildren_SetsParentID regression of the offline mode fix:
// GetChildren crea los inodos hijos con ParentID = parentID aunque Graph no
// devuelva parentReference (ListChildren NO lo incluye sin $expand). Sin esto:
//   - ItemsByParent (offline fallback) could not rebuild the list of a
//     carpeta evictada por el sweep TTL → "Error en Lookup" offline.
//   - SerializeAll did not persist the children (only inodes with ParentID) → the
//     subtree did not survive the BoltDB round-trip.
func TestInodeCache_GetChildren_SetsParentID(t *testing.T) {
	cache := NewInodeCache()

	// El fetcher devuelve items SIN parentReference (como hace Graph real)
	fetch := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		return []graph.DriveItem{
			{ID: "file1", Name: "a.txt", Size: 100},
			{ID: "folder1", Name: "sub", Folder: &graph.Folder{}},
		}, nil
	}

	_, err := cache.GetChildren(context.Background(), "parent1", fetch)
	if err != nil {
		t.Fatalf("GetChildren error: %v", err)
	}

	// Ambos hijos deben tener ParentID = parent1
	for _, id := range []string{"file1", "folder1"} {
		child := cache.Get(id)
		if child == nil {
			t.Fatalf("%s not found in cache", id)
		}
		if got := child.ParentID(); got != "parent1" {
			t.Errorf("%s: ParentID esperado 'parent1', obtenido %q", id, got)
		}
	}

	// ItemsByParent debe encontrarlos (fallback offline)
	items := cache.ItemsByParent("parent1")
	if len(items) != 2 {
		t.Errorf("ItemsByParent(parent1) esperaba 2 items, obtenidos %d", len(items))
	}
}

// TestInodeCache_GetChildren_ParentID_Then_MoveID protege el interplay entre
// the ParentID fix in GetChildren and delta reconciliation: after populating
// children via GetChildren (which now assigns ParentID since Graph does not return it),
// MoveID (swap local → remoto en delta) debe actualizar parent.children
// correctamente y el inodo movido debe conservar su ParentID.
func TestInodeCache_GetChildren_ParentID_Then_MoveID(t *testing.T) {
	cache := NewInodeCache()

	fetch := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		return []graph.DriveItem{
			{ID: "local-abc123", Name: "doc.txt", Size: 10},
		}, nil
	}

	_, err := cache.GetChildren(context.Background(), "parent1", fetch)
	if err != nil {
		t.Fatalf("GetChildren error: %v", err)
	}

	// Antes del swap, el hijo tiene ParentID asignado por GetChildren
	if got := cache.Get("local-abc123").ParentID(); got != "parent1" {
		t.Fatalf("ParentID esperado 'parent1', obtenido %q", got)
	}

	// MoveID: el item local recibe su ID remoto real (como en applyDelta)
	cache.MoveID("local-abc123", "remote-xyz")

	moved := cache.Get("remote-xyz")
	if moved == nil {
		t.Fatal("remote-xyz should exist after MoveID")
	}
	if moved.ParentID() != "parent1" {
		t.Errorf("Tras MoveID, ParentID esperado 'parent1', obtenido %q", moved.ParentID())
	}

	// parent.children debe apuntar al nuevo ID
	parent := cache.Get("parent1")
	children := parent.Children()
	if len(children) != 1 || children[0] != "remote-xyz" {
		t.Errorf("parent.children esperado ['remote-xyz'], obtenido %v", children)
	}

	// ItemsByParent (fallback offline) debe encontrar el inodo con el nuevo ID
	if items := cache.ItemsByParent("parent1"); len(items) != 1 || items[0].ID != "remote-xyz" {
		t.Errorf("ItemsByParent tras MoveID esperaba [remote-xyz], obtenido %v", items)
	}
}

func TestInodeCache_GetChildren_CacheHit(t *testing.T) {
	cache := NewInodeCache()

	callCount := 0
	fetch := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		callCount++
		return []graph.DriveItem{
			{ID: "file1", Name: "cached.txt", Size: 100},
		}, nil
	}

	// First call: fills the cache
	_, err := cache.GetChildren(context.Background(), "parent1", fetch)
	if err != nil {
		t.Fatalf("Primera GetChildren error: %v", err)
	}

	// Second call: must use the cache (0 additional calls)
	children, err := cache.GetChildren(context.Background(), "parent1", fetch)
	if err != nil {
		t.Fatalf("Segunda GetChildren error: %v", err)
	}
	if len(children) != 1 {
		t.Errorf("Se esperaba 1 hijo, obtenidos %d", len(children))
	}
	if callCount != 1 {
		t.Errorf("Se esperaba 1 llamada total al fetcher, hubo %d", callCount)
	}
}

func TestInodeCache_GetChildren_EmptyFolder(t *testing.T) {
	cache := NewInodeCache()

	callCount := 0
	fetch := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		callCount++
		return []graph.DriveItem{}, nil
	}

	// First call
	children, err := cache.GetChildren(context.Background(), "empty_folder", fetch)
	if err != nil {
		t.Fatalf("GetChildren error: %v", err)
	}
	if len(children) != 0 {
		t.Errorf("Expected 0 children, got %d", len(children))
	}

	// Second call: should use cache (children = []string{}, not nil)
	children, err = cache.GetChildren(context.Background(), "empty_folder", fetch)
	if err != nil {
		t.Fatalf("Second GetChildren error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("Empty folder should be cached: expected 1 fetch, got %d", callCount)
	}
}

func TestInodeCache_GetChildren_FetcherError(t *testing.T) {
	cache := NewInodeCache()

	fetch := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		return nil, errors.New("network error")
	}

	_, err := cache.GetChildren(context.Background(), "parent1", fetch)
	if err == nil {
		t.Error("Expected error from fetcher")
	}

	// Error should NOT be cached: children should remain nil
	parent := cache.Get("parent1")
	if parent != nil && parent.HasChildren() {
		t.Error("Errors should not be cached (children should remain nil)")
	}
}

// ──── TTL freshness in GetChildren ────

// TestInodeCache_GetChildren_StaleChildrenRefetch simulates the scenario of the bug
// "only shows cached files": children restored from a previous session
// (old childrenCachedAt) should refetch from Graph on the
// next GetChildren, even if they are "fetched" (not nil).
func TestInodeCache_GetChildren_StaleChildrenRefetch(t *testing.T) {
	cache := NewInodeCache()
	cache.SetBaseTTL(60 * time.Second)

	// Simulate an inode restored from BoltDB with children from 2 hours ago
	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"old_child"})
	// Artificially age the freshness metadata
	parent.Lock()
	parent.childrenCachedAt = time.Now().Add(-2 * time.Hour)
	parent.childrenLastAccess = time.Now().Add(-2 * time.Hour)
	parent.Unlock()
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "old_child", Name: "old.txt"}))

	callCount := 0
	fetch := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		callCount++
		return []graph.DriveItem{
			{ID: "new_child", Name: "nuevo.txt", Size: 100},
		}, nil
	}

	// Even though IsChildrenFetched() == true, children are stale → refetch
	children, err := cache.GetChildren(context.Background(), "parent1", fetch)
	if err != nil {
		t.Fatalf("GetChildren error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("Stale children should refetch: expected 1 fetch, got %d", callCount)
	}
	// The NEW account content should be present, not the old one
	if _, ok := children["nuevo.txt"]; !ok {
		t.Errorf("After refetch, should see real content (nuevo.txt), got: %v", children)
	}
	if _, ok := children["old.txt"]; ok {
		t.Error("Stale content (old.txt) should not continue being served")
	}
}

// TestInodeCache_GetChildren_FreshChildrenNoRefetch verifies that recently
// cached children (within effective TTL) are NOT refetched.
func TestInodeCache_GetChildren_FreshChildrenNoRefetch(t *testing.T) {
	cache := NewInodeCache()
	cache.SetBaseTTL(time.Hour) // Long TTL so it doesn't expire during test

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "child1", Name: "doc.txt"}))

	callCount := 0
	fetch := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		callCount++
		return nil, errors.New("should not call fetcher")
	}

	children, err := cache.GetChildren(context.Background(), "parent1", fetch)
	if err != nil {
		t.Fatalf("GetChildren error: %v", err)
	}
	if callCount != 0 {
		t.Errorf("Fresh children should not refetch: got %d fetches", callCount)
	}
	if _, ok := children["doc.txt"]; !ok {
		t.Errorf("doc.txt should be served from cache, got: %v", children)
	}
}

// TestInodeCache_GetChildren_FrequencyExtendsFreshness verifies that frequent
// hits extend the freshness window (Phase 4: frequency × TTL).
func TestInodeCache_GetChildren_FrequencyExtendsFreshness(t *testing.T) {
	cache := NewInodeCache()
	cache.SetBaseTTL(50 * time.Millisecond)

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 1},
	})
	cache.Insert(parent)
	parent.SetChildren([]string{"child1"})
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "child1", Name: "doc.txt"}))

	// 8 hits → multiplier 5.0 → effective TTL = 250ms
	for i := 0; i < 8; i++ {
		parent.BumpChildrenAccess()
	}

	// Age cachedAt 100ms (base 50ms would have expired, but with 8 hits it won't)
	parent.Lock()
	parent.childrenCachedAt = time.Now().Add(-100 * time.Millisecond)
	parent.Unlock()

	callCount := 0
	fetch := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		callCount++
		return nil, errors.New("should not call fetcher")
	}

	_, err := cache.GetChildren(context.Background(), "parent1", fetch)
	if err != nil {
		t.Fatalf("GetChildren error: %v", err)
	}
	if callCount != 0 {
		t.Errorf("With 8 hits, effective TTL (250ms) should cover 100ms age: got %d fetches", callCount)
	}
}

// ──── GetChild ────

func TestInodeCache_GetChild_Found(t *testing.T) {
	cache := NewInodeCache()

	fetch := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		return []graph.DriveItem{
			{ID: "f1", Name: "target.txt", Size: 50},
		}, nil
	}

	child, err := cache.GetChild(context.Background(), "parent1", "target.txt", fetch)
	if err != nil {
		t.Fatalf("GetChild error: %v", err)
	}
	if child == nil {
		t.Fatal("GetChild should find 'target.txt'")
	}
	if child.Name() != "target.txt" {
		t.Errorf("Expected Name 'target.txt', got %q", child.Name())
	}
}

func TestInodeCache_GetChild_NotFound(t *testing.T) {
	cache := NewInodeCache()

	fetch := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		return []graph.DriveItem{
			{ID: "f1", Name: "other.txt", Size: 50},
		}, nil
	}

	child, err := cache.GetChild(context.Background(), "parent1", "missing.txt", fetch)
	if err != nil {
		t.Fatalf("GetChild error: %v", err)
	}
	if child != nil {
		t.Error("GetChild should return nil for nonexistent name")
	}
}

// ──── Invalidate ────

func TestInodeCache_Invalidate(t *testing.T) {
	cache := NewInodeCache()

	callCount := 0
	fetch := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		callCount++
		return []graph.DriveItem{
			{ID: "file1", Name: "data.txt", Size: 100},
		}, nil
	}

	// Fill the cache
	_, err := cache.GetChildren(context.Background(), "parent1", fetch)
	if err != nil {
		t.Fatalf("GetChildren error: %v", err)
	}

	// Invalidar
	cache.Invalidate("parent1")

	// Siguiente GetChildren debe re-fetchear
	_, err = cache.GetChildren(context.Background(), "parent1", fetch)
	if err != nil {
		t.Fatalf("GetChildren post-invalidate error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("Se esperaban 2 fetches (original + post-invalidate), hubo %d", callCount)
	}
}

func TestInodeCache_InvalidateAll(t *testing.T) {
	cache := NewInodeCache()

	fetch := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		return []graph.DriveItem{
			{ID: parentID + "_child", Name: "item.txt", Size: 100},
		}, nil
	}

	// Llenar varias carpetas
	cache.GetChildren(context.Background(), "parent1", fetch)
	cache.GetChildren(context.Background(), "parent2", fetch)

	cache.InvalidateAll()

	// Verificar que ninguna tiene children
	for _, id := range []string{"parent1", "parent2"} {
		p := cache.Get(id)
		if p == nil {
			t.Errorf("%s should exist in the cache", id)
			continue
		}
		if p.HasChildren() {
			t.Errorf("%s should not have children after InvalidateAll", id)
		}
	}
}

// ──── Stats ────

func TestInodeCache_Stats(t *testing.T) {
	cache := NewInodeCache()

	cache.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "a", Name: "a"}))
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "b", Name: "b"}))
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "c", Name: "c"}))

	stats := cache.Stats()
	if stats.InodeCount != 3 {
		t.Errorf("InodeCount esperado 3, obtenido %d", stats.InodeCount)
	}
}

// ──── Subdir tracking ────

func TestInodeCache_SubdirTracking(t *testing.T) {
	cache := NewInodeCache()

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID:     "parent1",
		Name:   "root",
		Folder: &graph.Folder{ChildCount: 0},
	})
	cache.Insert(parent)

	// Insertar un hijo directorio
	dirChild := NewInodeDriveItem(&graph.DriveItem{
		ID:     "dir1",
		Name:   "subfolder",
		Folder: &graph.Folder{ChildCount: 0},
		Parent: &graph.DriveItemParent{ID: "parent1"},
	})
	cache.Insert(dirChild)

	// Insertar un hijo archivo
	fileChild := NewInodeDriveItem(&graph.DriveItem{
		ID:     "file1",
		Name:   "doc.txt",
		Size:   100,
		Parent: &graph.DriveItemParent{ID: "parent1"},
	})
	cache.Insert(fileChild)

	updatedParent := cache.Get("parent1")
	if updatedParent.NLink() != 3 { // 2 + 1 subdir
		t.Errorf("NLink esperado 3 (2+1), obtenido %d", updatedParent.NLink())
	}
}
