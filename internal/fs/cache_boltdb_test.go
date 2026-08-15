package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
)

// ──── GetPath ────

func TestInodeCache_GetPath_Root(t *testing.T) {
	cache := NewInodeCache()
	_, err := cache.GetPath(context.Background(), "/", nil)
	if !errors.Is(err, ErrIsRoot) {
		t.Errorf("GetPath('/') should return ErrIsRoot, got: %v", err)
	}
}

func TestInodeCache_GetPath_EmptyString(t *testing.T) {
	cache := NewInodeCache()
	_, err := cache.GetPath(context.Background(), "", nil)
	if !errors.Is(err, ErrIsRoot) {
		t.Errorf("GetPath('') should return ErrIsRoot, got: %v", err)
	}
}

func TestInodeCache_GetPath_Dot(t *testing.T) {
	cache := NewInodeCache()
	_, err := cache.GetPath(context.Background(), ".", nil)
	if !errors.Is(err, ErrIsRoot) {
		t.Errorf("GetPath('.') should return ErrIsRoot, got: %v", err)
	}
}

func TestInodeCache_GetPath_SingleLevel(t *testing.T) {
	cache := NewInodeCache()

	callCount := 0
	fetch := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		callCount++
		if parentID != "root" {
			t.Errorf("fetcher called with unexpected parentID: %q", parentID)
		}
		return []graph.DriveItem{
			{ID: "folder1", Name: "Documents", Folder: &graph.Folder{ChildCount: 0}},
			{ID: "file1", Name: "readme.txt", Size: 42},
		}, nil
	}

	child, err := cache.GetPath(context.Background(), "Documents", fetch)
	if err != nil {
		t.Fatalf("GetPath('Documents') error: %v", err)
	}
	if child == nil {
		t.Fatal("GetPath('Documents') returned nil")
	}
	if child.Name() != "Documents" {
		t.Errorf("Expected name 'Documents', got %q", child.Name())
	}
	if !child.IsDir() {
		t.Error("Documents should be a folder")
	}
	if callCount != 1 {
		t.Errorf("Expected 1 fetch, got %d", callCount)
	}
}

func TestInodeCache_GetPath_Nested(t *testing.T) {
	cache := NewInodeCache()

	fetchCount := make(map[string]int)
	fetch := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		fetchCount[parentID]++
		switch parentID {
		case "root":
			return []graph.DriveItem{
				{ID: "folder1", Name: "Documents", Folder: &graph.Folder{ChildCount: 1}},
			}, nil
		case "folder1":
			return []graph.DriveItem{
				{ID: "folder2", Name: "Projects", Folder: &graph.Folder{ChildCount: 0}},
			}, nil
		default:
			return nil, errors.New("unexpected parentID")
		}
	}

	child, err := cache.GetPath(context.Background(), "Documents/Projects", fetch)
	if err != nil {
		t.Fatalf("GetPath('Documents/Projects') error: %v", err)
	}
	if child == nil {
		t.Fatal("GetPath('Documents/Projects') returned nil")
	}
	if child.Name() != "Projects" {
		t.Errorf("Expected Name 'Projects', got %q", child.Name())
	}
	if child.ID() != "folder2" {
		t.Errorf("Expected ID 'folder2', got %q", child.ID())
	}

	// Verify that both folders were fetched
	if fetchCount["root"] != 1 {
		t.Errorf("Expected 1 fetch to root, got %d", fetchCount["root"])
	}
	if fetchCount["folder1"] != 1 {
		t.Errorf("Expected 1 fetch to folder1, got %d", fetchCount["folder1"])
	}
}

func TestInodeCache_GetPath_Nested_ThreeLevels(t *testing.T) {
	cache := NewInodeCache()

	fetch := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		switch parentID {
		case "root":
			return []graph.DriveItem{
				{ID: "a", Name: "A", Folder: &graph.Folder{}},
			}, nil
		case "a":
			return []graph.DriveItem{
				{ID: "b", Name: "B", Folder: &graph.Folder{}},
			}, nil
		case "b":
			return []graph.DriveItem{
				{ID: "c", Name: "C", Folder: &graph.Folder{}},
			}, nil
		default:
			return nil, errors.New("inesperado")
		}
	}

	child, err := cache.GetPath(context.Background(), "A/B/C", fetch)
	if err != nil {
		t.Fatalf("GetPath('A/B/C') error: %v", err)
	}
	if child.Name() != "C" {
		t.Errorf("Expected Name 'C', got %q", child.Name())
	}
}

func TestInodeCache_GetPath_NotFound(t *testing.T) {
	cache := NewInodeCache()

	fetch := func(ctx context.Context, _ string) ([]graph.DriveItem, error) {
		return []graph.DriveItem{
			{ID: "folder1", Name: "Documents", Folder: &graph.Folder{}},
		}, nil
	}

	_, err := cache.GetPath(context.Background(), "MissingFolder", fetch)
	if err == nil {
		t.Fatal("GetPath('MissingFolder') should return an error")
	}
	// The error should be ENOENT
	if !errors.Is(err, syscall.ENOENT) {
		t.Errorf("Expected ENOENT, got: %v", err)
	}
}

func TestInodeCache_GetPath_IntermediateNotFound(t *testing.T) {
	cache := NewInodeCache()

	fetch := func(ctx context.Context, _ string) ([]graph.DriveItem, error) {
		return []graph.DriveItem{
			{ID: "folder1", Name: "Documents", Folder: &graph.Folder{}},
		}, nil
	}

	_, err := cache.GetPath(context.Background(), "Documents/Missing/Deep", fetch)
	if err == nil {
		t.Fatal("GetPath('Documents/Missing/Deep') should return an error")
	}
	if !errors.Is(err, syscall.ENOENT) {
		t.Errorf("Expected ENOENT, got: %v", err)
	}
}

func TestInodeCache_GetPath_FileAsIntermediate(t *testing.T) {
	cache := NewInodeCache()

	fetch := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		switch parentID {
		case "root":
			return []graph.DriveItem{
				{ID: "file1", Name: "readme.txt", Size: 100},
			}, nil
		default:
			return nil, errors.New("inesperado")
		}
	}

	_, err := cache.GetPath(context.Background(), "readme.txt/subdir", fetch)
	if err == nil {
		t.Fatal("GetPath with a file as an intermediate component should return an error")
	}
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("Expected ENOTDIR, got: %v", err)
	}
}

func TestInodeCache_GetPath_CacheHit(t *testing.T) {
	cache := NewInodeCache()

	callCount := 0
	fetch := func(ctx context.Context, _ string) ([]graph.DriveItem, error) {
		callCount++
		return []graph.DriveItem{
			{ID: "folder1", Name: "Documents", Folder: &graph.Folder{ChildCount: 1}},
			{ID: "file1", Name: "readme.txt", Size: 42},
		}, nil
	}

	// First call: fills the cache
	_, err := cache.GetPath(context.Background(), "Documents", fetch)
	if err != nil {
		t.Fatalf("Primera GetPath error: %v", err)
	}
	firstCallCount := callCount

	// Second call: must use the cache, without additional fetches
	child, err := cache.GetPath(context.Background(), "Documents", fetch)
	if err != nil {
		t.Fatalf("Segunda GetPath error: %v", err)
	}
	if child.Name() != "Documents" {
		t.Errorf("Expected name 'Documents', got %q", child.Name())
	}
	if callCount != firstCallCount {
		t.Errorf("Second GetPath should not call the fetcher. Calls: %d (expected %d)", callCount, firstCallCount)
	}
}

func TestInodeCache_GetPath_FetcherError(t *testing.T) {
	cache := NewInodeCache()

	fetch := func(ctx context.Context, _ string) ([]graph.DriveItem, error) {
		return nil, errors.New("network error")
	}

	_, err := cache.GetPath(context.Background(), "Documents", fetch)
	if err == nil {
		t.Fatal("GetPath should return an error when the fetcher fails")
	}
}

func TestInodeCache_GetPath_TrailingSlash(t *testing.T) {
	cache := NewInodeCache()

	callCount := 0
	fetch := func(ctx context.Context, _ string) ([]graph.DriveItem, error) {
		callCount++
		return []graph.DriveItem{
			{ID: "folder1", Name: "Documents", Folder: &graph.Folder{}},
		}, nil
	}

	child, err := cache.GetPath(context.Background(), "Documents/", fetch)
	if err != nil {
		t.Fatalf("GetPath('Documents/') error: %v", err)
	}
	if child.Name() != "Documents" {
		t.Errorf("Expected name 'Documents', got %q", child.Name())
	}
}

func TestInodeCache_GetPath_LeadingSlash(t *testing.T) {
	cache := NewInodeCache()

	fetch := func(ctx context.Context, _ string) ([]graph.DriveItem, error) {
		return []graph.DriveItem{
			{ID: "folder1", Name: "Documents", Folder: &graph.Folder{}},
		}, nil
	}

	child, err := cache.GetPath(context.Background(), "/Documents", fetch)
	if err != nil {
		t.Fatalf("GetPath('/Documents') error: %v", err)
	}
	if child.Name() != "Documents" {
		t.Errorf("Expected name 'Documents', got %q", child.Name())
	}
}

func TestInodeCache_GetPath_FileLeaf(t *testing.T) {
	cache := NewInodeCache()

	fetch := func(ctx context.Context, _ string) ([]graph.DriveItem, error) {
		return []graph.DriveItem{
			{ID: "file1", Name: "readme.txt", Size: 42},
		}, nil
	}

	child, err := cache.GetPath(context.Background(), "readme.txt", fetch)
	if err != nil {
		t.Fatalf("GetPath('readme.txt') error: %v", err)
	}
	if child.IsDir() {
		t.Error("readme.txt should not be a folder")
	}
	if child.Name() != "readme.txt" {
		t.Errorf("Expected Name 'readme.txt', got %q", child.Name())
	}
}

// ──── BoltDB: InitBoltDB ────

func TestInodeCache_InitBoltDB_Success(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cache := NewInodeCache()
	err := cache.InitBoltDB(dbPath)
	if err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}

	// Verify that the file was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Errorf("Archivo BoltDB no creado en %s", dbPath)
	}

	// Close must work
	if err := cache.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	// Re-abrir sin error
	err = cache.InitBoltDB(dbPath)
	if err != nil {
		t.Fatalf("Segunda InitBoltDB error: %v", err)
	}

	cache.Close()
}

func TestInodeCache_InitBoltDB_InvalidPath(t *testing.T) {
	cache := NewInodeCache()
	// Try to create BoltDB in a non-existent directory without write permissions
	// (e.g. a path with a component that is a file, not a directory)
	tmpDir := t.TempDir()
	blockerFile := filepath.Join(tmpDir, "notadir")
	if err := os.WriteFile(blockerFile, []byte("block"), 0600); err != nil {
		t.Fatalf("Setup error: %v", err)
	}

	// Try to create BoltDB where a path component is a file
	err := cache.InitBoltDB(filepath.Join(blockerFile, "sub", "test.db"))
	if err == nil {
		t.Fatal("InitBoltDB should fail with an invalid path")
	}
	// Close must not panic even if InitBoltDB failed
	cache.Close()
}

func TestInodeCache_InitBoltDB_DoubleMount(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cache1 := NewInodeCache()
	if err := cache1.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}
	defer cache1.Close()

	// A second cache on the same path must fail with a clear message about
	// the double mount instead of an opaque "timeout".
	cache2 := NewInodeCache()
	err := cache2.initBoltDB(dbPath, 150*time.Millisecond)
	if err == nil {
		t.Fatal("second InitBoltDB on a locked path should fail")
	}
	if !strings.Contains(err.Error(), "locked by another running instance") {
		t.Errorf("expected a clear double-mount message, got: %v", err)
	}

	// Close must not panic even though InitBoltDB failed.
	cache2.Close()
}

func TestInodeCache_Close_Idempotent(t *testing.T) {
	cache := NewInodeCache()

	// Close without prior InitBoltDB must not panic
	cache.Close()
	cache.Close() // Segunda vez
	cache.Close() // Tercera vez

	// The cache must remain usable
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "x", Name: "x"}))
	if cache.Get("x") == nil {
		t.Error("InodeCache should keep working after Close without BoltDB")
	}
}

// TestInodeCache_Close_Concurrent verifies that Close() can be called from
// varias goroutines a la vez (defer de Mount + signal handler de desmontaje)
// without panic or race conditions. Regression of the crash:
//
//	panic: bbolt.(*DB).Close(0x0) — two concurrent Close() calls, one set c.db=nil
//	y la otra llamaba c.db.Close() sobre nil.
func TestInodeCache_Close_Concurrent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cache := NewInodeCache()
	if err := cache.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}

	// Poblar un poco para que SerializeAll tenga trabajo
	root := NewInodeDriveItem(&graph.DriveItem{
		ID: "root", Name: "root", Folder: &graph.Folder{ChildCount: 1},
	})
	root.SetChildren([]string{"child1"})
	cache.Insert(root)
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "child1", Name: "a.txt"}))

	// Launch N concurrent Close() calls — must be safe and idempotent
	const goroutines = 8
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() { errs <- cache.Close() }()
	}
	for i := 0; i < goroutines; i++ {
		if err := <-errs; err != nil {
			t.Errorf("Concurrent Close returned an error: %v", err)
		}
	}

	// The cache must remain usable afterwards
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "x", Name: "x"}))
	if cache.Get("x") == nil {
		t.Error("InodeCache should keep working after concurrent Close")
	}
}

func TestInodeCache_Close_WithBoltDB_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cache := NewInodeCache()
	if err := cache.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}

	// Close multiple times
	cache.Close()
	cache.Close()
	cache.Close()

	// Re-abrir sin error
	if err := cache.InitBoltDB(dbPath); err != nil {
		t.Fatalf("Re-opening after multiple Close calls should work: %v", err)
	}
	cache.Close()
}

// ──── BoltDB: SerializeAll / DeserializeFromDisk ────

func TestInodeCache_SerializeAll_Deserialize_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// 1. Create, populate and persist
	cache1 := NewInodeCache()
	if err := cache1.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}

	// Insert root explicitly (InsertChild does not create the parent)
	root := NewInodeDriveItem(&graph.DriveItem{
		ID: "root", Name: "root", Folder: &graph.Folder{ChildCount: 2},
	})
	cache1.Insert(root)

	// Insert child folders of root
	cache1.InsertChild("root", "folder1", NewInodeDriveItem(&graph.DriveItem{
		ID: "folder1", Name: "Documents", Folder: &graph.Folder{ChildCount: 0},
	}))
	cache1.InsertChild("root", "folder2", NewInodeDriveItem(&graph.DriveItem{
		ID: "folder2", Name: "Images", Folder: &graph.Folder{ChildCount: 0},
	}))

	// Marcar los children de root como "fetched" para que SerializeAll los persista
	root = cache1.Get("root")
	if root == nil {
		t.Fatal("root should exist in the cache")
	}
	root.SetChildren([]string{"folder1", "folder2"})

	// Insert a child inside folder1 and mark it as fetched
	subfolder := NewInodeDriveItem(&graph.DriveItem{
		ID: "subfolder", Name: "Projects", Folder: &graph.Folder{ChildCount: 0},
	})
	cache1.InsertChild("folder1", "subfolder", subfolder)
	folder1 := cache1.Get("folder1")
	if folder1 == nil {
		t.Fatal("folder1 should exist in the cache")
	}
	folder1.SetChildren([]string{"subfolder"})
	// Mark subfolder as fetched (empty folder) so SerializeAll persists it
	subfolder.SetChildren([]string{})

	// folder2 is an empty folder: mark as fetched with empty children
	folder2 := cache1.Get("folder2")
	if folder2 == nil {
		t.Fatal("folder2 should exist in the cache")
	}
	folder2.SetChildren([]string{})

	// Persistir
	if err := cache1.SerializeAll(); err != nil {
		t.Fatalf("SerializeAll error: %v", err)
	}
	cache1.Close()

	// 2. Open a new cache and load from disk
	cache2 := NewInodeCache()
	if err := cache2.InitBoltDB(dbPath); err != nil {
		t.Fatalf("Segunda InitBoltDB error: %v", err)
	}
	defer cache2.Close()

	// Verify that the persisted inodes are in memory
	// Note: SerializeAll only persists inodes with IsChildrenFetched() == true.
	// Files (without children) are not persisted — only folders with fetched children.
	for _, tc := range []struct{ id, name string }{
		{"root", "root"},
		{"folder1", "Documents"},
		{"folder2", "Images"},
		{"subfolder", "Projects"},
	} {
		inode := cache2.Get(tc.id)
		if inode == nil {
			t.Errorf("%q should be in the cache after DeserializeFromDisk", tc.id)
			continue
		}
		if inode.Name() != tc.name {
			t.Errorf("Expected %q.Name %q, got %q", tc.id, tc.name, inode.Name())
		}
	}

	// Verify that the children were restored correctly
	restoredRoot := cache2.Get("root")
	if restoredRoot == nil {
		t.Fatal("root no encontrado")
	}
	if !restoredRoot.IsChildrenFetched() {
		t.Error("root should have restored children")
	}
	rootChildren := restoredRoot.Children()
	if len(rootChildren) != 2 {
		t.Errorf("root should have 2 children, got %d: %v", len(rootChildren), rootChildren)
	}

	restoredFolder := cache2.Get("folder1")
	if restoredFolder == nil {
		t.Fatal("folder1 no encontrado")
	}
	folder1Children := restoredFolder.Children()
	if len(folder1Children) != 1 {
		t.Errorf("folder1 should have 1 child, got %d", len(folder1Children))
	} else if folder1Children[0] != "subfolder" {
		t.Errorf("Expected folder1.Children[0] 'subfolder', got %q", folder1Children[0])
	}

	// folder2 is an empty folder: children must be an empty slice (not nil)
	restoredFolder2 := cache2.Get("folder2")
	if restoredFolder2 == nil {
		t.Fatal("folder2 no encontrado")
	}
	if !restoredFolder2.IsChildrenFetched() {
		t.Error("folder2 should have fetched children (empty slice)")
	}
	if len(restoredFolder2.Children()) != 0 {
		t.Errorf("folder2 should have 0 children, got %d", len(restoredFolder2.Children()))
	}
}

func TestInodeCache_SerializeAll_WithoutBoltDB(t *testing.T) {
	cache := NewInodeCache()
	// SerializeAll without initialized BoltDB must not panic or error
	err := cache.SerializeAll()
	if err != nil {
		t.Errorf("SerializeAll without BoltDB should return nil, got: %v", err)
	}
}

// ──── Regression: files (children without children) survive the round-trip ────

// TestInodeCache_SerializeAll_PersistsChildFiles verifies that SerializeAll
// also persists FILE inodes (children==nil) referenced as children
// of a persisted folder. Without this, after the round-trip a folder would
// restores with children=[a,b] but a/b do not exist in memory → buildChildMap
// loses the listing files (bug "missing files after remounting").
func TestInodeCache_SerializeAll_PersistsChildFiles(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// 1. Session 1: folder with children that include files
	cache1 := NewInodeCache()
	if err := cache1.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}

	root := NewInodeDriveItem(&graph.DriveItem{
		ID: "root", Name: "root", Folder: &graph.Folder{ChildCount: 2},
	})
	root.SetChildren([]string{"folder1", "file1", "file2"})
	cache1.Insert(root)

	// Child folder (with children fetched, it is persisted)
	folder1 := NewInodeDriveItem(&graph.DriveItem{
		ID: "folder1", Name: "Docs", Folder: &graph.Folder{},
	})
	folder1.SetChildren([]string{})
	cache1.Insert(folder1)

	// Child files (children==nil: NEVER persisted before this fix)
	cache1.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "file1", Name: "a.txt", Size: 10,
		Parent: &graph.DriveItemParent{ID: "root"},
	}))
	cache1.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "file2", Name: "b.pdf", Size: 20,
		Parent: &graph.DriveItemParent{ID: "root"},
	}))

	if err := cache1.SerializeAll(); err != nil {
		t.Fatalf("SerializeAll error: %v", err)
	}
	cache1.Close()

	// 2. Session 2: restore and verify that the FILES exist
	cache2 := NewInodeCache()
	if err := cache2.InitBoltDB(dbPath); err != nil {
		t.Fatalf("Segunda InitBoltDB error: %v", err)
	}
	defer cache2.Close()

	for _, id := range []string{"root", "folder1", "file1", "file2"} {
		if cache2.Get(id) == nil {
			t.Errorf("%q should be in the cache after the round-trip", id)
		}
	}

	// 3. buildChildMap must rebuild the COMPLETE root listing
	//    (folders + files), without calling the fetcher.
	restoredRoot := cache2.Get("root")
	if restoredRoot == nil {
		t.Fatal("root no encontrado")
	}
	children := cache2.buildChildMap(restoredRoot.Children())
	if len(children) != 3 {
		t.Errorf("buildChildMap(root) expected 3 children, got %d: %v", len(children), children)
	}
	if _, ok := children["a.txt"]; !ok {
		t.Errorf("a.txt should be rebuilt from the cache, got: %v", children)
	}
	if _, ok := children["b.pdf"]; !ok {
		t.Errorf("b.pdf should be rebuilt from the cache, got: %v", children)
	}
}

// TestInodeCache_SerializeAll_OnlyFetchedInodes verifies that an orphan inode
// (without children and without being referenced by any folder) is NOT persisted.
func TestInodeCache_SerializeAll_OnlyFetchedInodes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cache := NewInodeCache()
	if err := cache.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}

	// Insert an inode WITHOUT children (file, not "fetched")
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "orphan", Name: "orphan.txt", Size: 10,
	}))

	// Insert an inode WITH empty children (empty folder, "fetched")
	parent := NewInodeDriveItem(&graph.DriveItem{
		ID:     "parent",
		Name:   "Docs",
		Folder: &graph.Folder{},
	})
	cache.Insert(parent)
	// Mark as fetched: empty children (folder without content)
	parent.SetChildren([]string{})

	if err := cache.SerializeAll(); err != nil {
		t.Fatalf("SerializeAll error: %v", err)
	}
	cache.Close()

	// Re-open and verify that only the "fetched" one was persisted
	cache2 := NewInodeCache()
	if err := cache2.InitBoltDB(dbPath); err != nil {
		t.Fatalf("Segunda InitBoltDB error: %v", err)
	}
	defer cache2.Close()

	if cache2.Get("parent") == nil {
		t.Error("parent (fetched) should be persisted")
	}
	if cache2.Get("orphan") != nil {
		t.Error("orphan (without children) should NOT be persisted")
	}
}

func TestInodeCache_DeserializeFromDisk_EmptyBucket(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a cache, initialize BoltDB, and close without serializing anything
	cache1 := NewInodeCache()
	if err := cache1.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}
	cache1.Close()

	// Open again — DeserializeFromDisk must handle an empty bucket without error
	cache2 := NewInodeCache()
	if err := cache2.InitBoltDB(dbPath); err != nil {
		t.Fatalf("Segunda InitBoltDB error: %v", err)
	}
	defer cache2.Close()

	// Stats must show 0 inodes
	stats := cache2.Stats()
	if stats.InodeCount != 0 {
		t.Errorf("Expected inode count 0, got %d", stats.InodeCount)
	}
}

// ──── Regression: restore from BoltDB with stale children ────

// TestInodeCache_RestoredFromDisk_RefetchesStaleChildren reproduce el bug
// "only shows the files that were already in the cache": after persisting
// metadata in one session, when remounting, the restored children from
// BoltDB have stale childrenCachedAt → GetChildren must refetch from
// Graph en vez de servir datos obsoletos.
func TestInodeCache_RestoredFromDisk_RefetchesStaleChildren(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// 1. Session 1: persist an inode with "old" children
	cache1 := NewInodeCache()
	if err := cache1.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}
	root := NewInodeDriveItem(&graph.DriveItem{
		ID: "root", Name: "root", Folder: &graph.Folder{ChildCount: 1},
	})
	root.SetChildren([]string{"old1"})
	cache1.Insert(root)
	cache1.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "old1", Name: "viejo.txt"}))

	if err := cache1.SerializeAll(); err != nil {
		t.Fatalf("SerializeAll error: %v", err)
	}
	cache1.Close()

	// 2. Session 2: restore and age the freshness metadata
	//    (simulates that session 1 was hours ago, not seconds)
	cache2 := NewInodeCache()
	if err := cache2.InitBoltDB(dbPath); err != nil {
		t.Fatalf("Segunda InitBoltDB error: %v", err)
	}
	defer cache2.Close()

	restored := cache2.Get("root")
	if restored == nil || !restored.IsChildrenFetched() {
		t.Fatal("root should be restored with fetched children")
	}
	// Age to simulate that the previous session was hours ago
	restored.Lock()
	restored.childrenCachedAt = time.Now().Add(-2 * time.Hour)
	restored.childrenLastAccess = time.Now().Add(-2 * time.Hour)
	restored.Unlock()

	// 3. GetChildren must refetch (the restored data is stale)
	callCount := 0
	fetch := func(ctx context.Context, _ string) ([]graph.DriveItem, error) {
		callCount++
		return []graph.DriveItem{
			{ID: "real1", Name: "real.txt", Size: 200},
		}, nil
	}

	children, err := cache2.GetChildren(context.Background(), "root", fetch)
	if err != nil {
		t.Fatalf("GetChildren error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("Stale restored children should be refetched: expected 1 fetch, got %d", callCount)
	}
	if _, ok := children["real.txt"]; !ok {
		t.Errorf("Real content should be visible (real.txt), got: %v", children)
	}
	if _, ok := children["viejo.txt"]; ok {
		t.Error("Old content (viejo.txt) should not keep being served after refetch")
	}
}

// TestInodeCache_RestoredFromDisk_FreshServedFromCache verifies the case
// complementary: if the previous session was recent (within the TTL), the
// restored children are served from the cache without calling Graph.
func TestInodeCache_RestoredFromDisk_FreshServedFromCache(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cache1 := NewInodeCache()
	if err := cache1.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}
	root := NewInodeDriveItem(&graph.DriveItem{
		ID: "root", Name: "root", Folder: &graph.Folder{ChildCount: 1},
	})
	root.SetChildren([]string{"old1"})
	cache1.Insert(root)

	// The child must be a FOLDER with fetched children (empty): SerializeAll
	// only persists inodes with IsChildrenFetched() == true, so a file
	// without children would not survive the round-trip.
	oldDir := NewInodeDriveItem(&graph.DriveItem{
		ID: "old1", Name: "vieja", Folder: &graph.Folder{},
	})
	oldDir.SetChildren([]string{})
	cache1.Insert(oldDir)

	if err := cache1.SerializeAll(); err != nil {
		t.Fatalf("SerializeAll error: %v", err)
	}
	cache1.Close()

	cache2 := NewInodeCache()
	if err := cache2.InitBoltDB(dbPath); err != nil {
		t.Fatalf("Segunda InitBoltDB error: %v", err)
	}
	defer cache2.Close()

	callCount := 0
	fetch := func(ctx context.Context, _ string) ([]graph.DriveItem, error) {
		callCount++
		return nil, errors.New("should not call the fetcher")
	}

	// Without aging, the freshly restored children are fresh → hit
	children, err := cache2.GetChildren(context.Background(), "root", fetch)
	if err != nil {
		t.Fatalf("GetChildren error: %v", err)
	}
	if callCount != 0 {
		t.Errorf("Fresh restored children should be served from cache: got %d fetches", callCount)
	}
	if _, ok := children["vieja"]; !ok {
		t.Errorf("vieja should be served from cache, got: %v", children)
	}
}

func TestInodeCache_DeserializeFromDisk_DoesNotOverwriteMemory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// 1. First cache: writes "v1" to BoltDB
	cache1 := NewInodeCache()
	if err := cache1.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}
	root := NewInodeDriveItem(&graph.DriveItem{
		ID: "inode1", Name: "v1_from_disk", Folder: &graph.Folder{},
	})
	root.SetChildren([]string{})
	cache1.Insert(root)
	cache1.SerializeAll()
	cache1.Close()

	// 2. Second cache: inserts an inode in memory BEFORE InitBoltDB
	cache2 := NewInodeCache()
	// Insert in memory before loading from disk
	cache2.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "inode1", Name: "v2_from_memory", Folder: &graph.Folder{},
	}))

	// InitBoltDB loads from disk — it must NOT overwrite the in-memory inode
	if err := cache2.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}
	defer cache2.Close()

	// Memory wins: the name must be "v2_from_memory"
	inode := cache2.Get("inode1")
	if inode == nil {
		t.Fatal("inode1 should exist in the cache")
	}
	if inode.Name() != "v2_from_memory" {
		t.Errorf("Expected Name 'v2_from_memory' (memory wins), got %q", inode.Name())
	}
}

// ──── BoltDB: GetDeltaLink / SetDeltaLink ────

func TestInodeCache_GetDeltaLink_EmptyInitially(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cache := NewInodeCache()
	if err := cache.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}
	defer cache.Close()

	link := cache.GetDeltaLink()
	if link != "" {
		t.Errorf("Initial delta link should be '', got %q", link)
	}
}

func TestInodeCache_SetDeltaLink_GetDeltaLink(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cache := NewInodeCache()
	if err := cache.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}
	defer cache.Close()

	expected := "https://graph.microsoft.com/v1.0/me/drive/root/delta?token=abc123"
	cache.SetDeltaLink(expected)

	got := cache.GetDeltaLink()
	if got != expected {
		t.Errorf("Expected delta link %q, got %q", expected, got)
	}
}

func TestInodeCache_SetDeltaLink_PersistsAcrossRestarts(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	expected := "https://graph.microsoft.com/v1.0/me/drive/root/delta?token=xyz789"

	// 1. Escribir delta link
	cache1 := NewInodeCache()
	if err := cache1.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}
	cache1.SetDeltaLink(expected)
	cache1.Close()

	// 2. Read the delta link from a new instance
	cache2 := NewInodeCache()
	if err := cache2.InitBoltDB(dbPath); err != nil {
		t.Fatalf("Segunda InitBoltDB error: %v", err)
	}
	defer cache2.Close()

	got := cache2.GetDeltaLink()
	if got != expected {
		t.Errorf("Expected delta link %q after restart, got %q", expected, got)
	}
}

func TestInodeCache_SetDeltaLink_WithoutBoltDB(t *testing.T) {
	cache := NewInodeCache()
	// Must not panic
	cache.SetDeltaLink("https://example.com/delta")
	link := cache.GetDeltaLink()
	if link != "" {
		t.Errorf("GetDeltaLink without BoltDB should return '', got %q", link)
	}
}

func TestInodeCache_SetDeltaLink_Overwrite(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cache := NewInodeCache()
	if err := cache.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}
	defer cache.Close()

	cache.SetDeltaLink("first")
	cache.SetDeltaLink("second")
	cache.SetDeltaLink("third")

	if got := cache.GetDeltaLink(); got != "third" {
		t.Errorf("Expected delta link 'third', got %q", got)
	}
}

func TestInodeCache_GetDeltaLink_WithoutBoltDB(t *testing.T) {
	cache := NewInodeCache()
	link := cache.GetDeltaLink()
	if link != "" {
		t.Errorf("GetDeltaLink without BoltDB should return '', got %q", link)
	}
}
