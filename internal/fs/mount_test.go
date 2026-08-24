package fs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/auth"
	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/rs/zerolog"
)

// TestMount_CacheHandles verifies that CacheHandles is created correctly
func TestMount_CacheHandles(t *testing.T) {
	inodeCache := NewInodeCache()
	contentCache, _ := NewContentCache(t.TempDir())

	handles := &CacheHandles{
		Metadata: inodeCache,
		Content:  contentCache,
	}

	if handles.Metadata == nil {
		t.Error("Metadata should not be nil")
	}
	if handles.Content == nil {
		t.Error("Content should not be nil")
	}

	// Verify that Metadata remains usable
	handles.Metadata.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "test", Name: "test"}))
	if handles.Metadata.Get("test") == nil {
		t.Error("Get should find the inserted item")
	}
}

// TestMount_ContentCacheAccess verifies ContentCache access via CacheHandles
func TestMount_ContentCacheAccess(t *testing.T) {
	contentCache, err := NewContentCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}

	handles := &CacheHandles{
		Metadata: NewInodeCache(),
		Content:  contentCache,
	}

	if err := handles.Content.Insert("test_file", []byte("hello")); err != nil {
		t.Fatalf("Insert error: %v", err)
	}
	if !handles.Content.HasContent("test_file") {
		t.Error("HasContent should be true")
	}
}

// TestMount_InodeCacheTTL verifies that InodeCache works with an implicit TTL
func TestMount_InodeCacheTTL(t *testing.T) {
	cache := NewInodeCache()
	_ = cache.Get("nonexistent") // no-op

	stats := cache.Stats()
	if stats.InodeCount != 0 {
		t.Errorf("Expected inode count 0, got %d", stats.InodeCount)
	}

	// Insert and verify stats
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "i1", Name: "one"}))
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "i2", Name: "two"}))

	if cache.Stats().InodeCount != 2 {
		t.Errorf("Expected InodeCount 2, got %d", cache.Stats().InodeCount)
	}
}

// TestDefaultMountConfig_Normal verifies the default cache path.
func TestDefaultMountConfig_Normal(t *testing.T) {
	t.Setenv("HOME", "/home/testuser")

	config := DefaultMountConfig("test@outlook.com", nil)

	expected := filepath.Join("/home/testuser", ".cache", "onecloudriver", "test@outlook.com")
	if config.CacheDir != expected {
		t.Errorf("Expected cache dir %q, got %q", expected, config.CacheDir)
	}
	if config.CacheTTL != 60*time.Second {
		t.Errorf("Expected cache TTL 60s, got %v", config.CacheTTL)
	}
}

func TestDefaultMountConfig_Persisted(t *testing.T) {
	t.Setenv("HOME", "/home/testuser")

	persisted := &auth.AccountPersistedConfig{
		CacheDir:        "/custom/cache",
		CacheTTL:        120 * time.Second,
		CacheMaxEntries: 500,
		CacheMaxSize:    1 << 30,
		DeltaInterval:   10 * time.Minute,
		GraphRetries:    5,
	}

	config := DefaultMountConfig("test@outlook.com", persisted)

	if config.CacheDir != "/custom/cache" {
		t.Errorf("Expected cache dir /custom/cache, got %q", config.CacheDir)
	}
	if config.CacheTTL != 120*time.Second {
		t.Errorf("Expected cache TTL 120s, got %v", config.CacheTTL)
	}
	if config.CacheMaxEntries != 500 {
		t.Errorf("Expected cache max entries 500, got %d", config.CacheMaxEntries)
	}
	if config.DeltaInterval != 10*time.Minute {
		t.Errorf("Expected delta interval 10m, got %v", config.DeltaInterval)
	}
	if config.GraphRetries != 5 {
		t.Errorf("Expected GraphRetries 5, got %d", config.GraphRetries)
	}
}

// TestMount_CloseIsNoop verifies that Close does not cause a panic
func TestMount_CloseIsNoop(t *testing.T) {
	cache := NewInodeCache()
	cache.Close()                                                         // primera
	cache.Close()                                                         // second: must not panic
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "x", Name: "x"})) // sigue usable
	if cache.Get("x") == nil {
		t.Error("InodeCache should keep working after Close (no-op in Phase 2)")
	}
}

// ──── preWarm tests ────

// TestPreWarm_Depth0_NoOp verifies that preWarm with depth=0 returns immediately without fetching.
func TestPreWarm_Depth0_NoOp(t *testing.T) {
	cache := NewInodeCache()
	fetcher := func(ctx context.Context, _ string) ([]graph.DriveItem, error) {
		t.Error("fetcher should not be called with depth=0")
		return nil, nil
	}

	err := preWarm(context.Background(), cache, fetcher, 0)
	if err != nil {
		t.Errorf("preWarm depth=0 should not return error, got %v", err)
	}
}

// TestPreWarm_DepthOutOfRange verifies that preWarm rejects invalid depths.
func TestPreWarm_DepthOutOfRange(t *testing.T) {
	tests := []struct {
		name      string
		depth     int
		wantError bool
	}{
		{"depth > 10", 11, true},
		{"depth 15", 15, true},
		{"depth 100", 100, true},
		{"depth -1", -1, true},
		{"depth -5", -5, true},
	}

	cache := NewInodeCache()
	fetcher := func(ctx context.Context, _ string) ([]graph.DriveItem, error) {
		return []graph.DriveItem{}, nil
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := preWarm(context.Background(), cache, fetcher, tt.depth)
			if tt.wantError && err == nil {
				t.Error("preWarm should return error for out-of-range depth")
			}
			if !tt.wantError && err != nil {
				t.Errorf("preWarm should not return error, got %v", err)
			}
		})
	}
}

// TestPreWarm_Depth1_RootOnly verifies that preWarm with depth=1 fetches only
// the root listing and does not descend into root's subfolders.
func TestPreWarm_Depth1_RootOnly(t *testing.T) {
	cache := NewInodeCache()
	fetchCount := 0

	fetcher := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		fetchCount++
		if parentID != "root" {
			t.Errorf("preWarm depth=1 should only fetch root, got parentID %q", parentID)
		}

		// Root has a folder: depth=1 must NOT fetch its children.
		return []graph.DriveItem{
			{ID: "folder1", Name: "Folder 1", Folder: &graph.Folder{ChildCount: 1}},
		}, nil
	}

	err := preWarm(context.Background(), cache, fetcher, 1)
	if err != nil {
		t.Errorf("preWarm depth=1 returned error: %v", err)
	}
	if fetchCount != 1 {
		t.Errorf("preWarm depth=1 should fetch exactly root (1 call), got %d", fetchCount)
	}
}

// TestPreWarm_Depth2_TwoLevels verifies that preWarm with depth=2 traverses two levels.
func TestPreWarm_Depth2_TwoLevels(t *testing.T) {
	cache := NewInodeCache()
	fetchCount := 0
	var fetchedParents []string

	fetcher := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		fetchCount++
		fetchedParents = append(fetchedParents, parentID)

		// Return different items based on parentID
		switch parentID {
		case "root":
			// Root has two folders
			return []graph.DriveItem{
				{
					ID:     "folder1",
					Name:   "Folder 1",
					Folder: &graph.Folder{ChildCount: 2},
				},
				{
					ID:     "folder2",
					Name:   "Folder 2",
					Folder: &graph.Folder{ChildCount: 1},
				},
			}, nil
		case "folder1", "folder2":
			// Second-level folders return files (not folders)
			return []graph.DriveItem{
				{ID: "file", Name: "file.txt"},
			}, nil
		default:
			return []graph.DriveItem{}, nil
		}
	}

	err := preWarm(context.Background(), cache, fetcher, 2)
	if err != nil {
		t.Errorf("preWarm depth=2 returned error: %v", err)
	}

	// Verify we fetched root, folder1, and folder2 (3 times total).
	if fetchCount != 3 {
		t.Errorf("preWarm depth=2 should fetch 3 times (root + 2 folders), got %d", fetchCount)
	}

	// Verify root was fetched first.
	if len(fetchedParents) == 0 || fetchedParents[0] != "root" {
		t.Errorf("preWarm should start with root, got %v", fetchedParents)
	}

	// Regression for the name-vs-ID bug: traversal must use item IDs, not the
	// display names ("Folder 1"/"Folder 2") that key the GetChildren map.
	for _, want := range []string{"folder1", "folder2"} {
		found := false
		for _, p := range fetchedParents {
			if p == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("preWarm did not traverse folder ID %q (visited %v)", want, fetchedParents)
		}
	}
}

// TestPreWarm_ContextTimeout verifies that preWarm respects context cancellation.
func TestPreWarm_ContextTimeout(t *testing.T) {
	cache := NewInodeCache()
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately before calling preWarm
	cancel()

	fetcher := func(ctx context.Context, _ string) ([]graph.DriveItem, error) {
		// This should not be called since context is already cancelled
		return []graph.DriveItem{}, nil
	}

	err := preWarm(ctx, cache, fetcher, 1)
	if err == nil {
		t.Error("preWarm should return error when context is cancelled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("preWarm should return context.Canceled, got %v", err)
	}
}

// TestPreWarm_FetcherError_BestEffort verifies that preWarm continues on fetcher errors.
func TestPreWarm_FetcherError_BestEffort(t *testing.T) {
	cache := NewInodeCache()
	visited := make(map[string]bool)

	fetcher := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		visited[parentID] = true
		switch parentID {
		case "root":
			return []graph.DriveItem{
				{ID: "folder_ok", Name: "Good Folder", Folder: &graph.Folder{ChildCount: 1}},
				{ID: "folder_err", Name: "Bad Folder", Folder: &graph.Folder{ChildCount: 1}},
			}, nil
		case "folder_err":
			// Simulate an error on this folder.
			return nil, fmt.Errorf("mock error: folder fetch failed")
		default:
			// folder_ok succeeds.
			return []graph.DriveItem{}, nil
		}
	}

	err := preWarm(context.Background(), cache, fetcher, 2)
	if err != nil {
		t.Errorf("preWarm should not fail on single folder error (best-effort), got %v", err)
	}

	// A failure in one branch must not stop traversal of sibling branches.
	for _, want := range []string{"root", "folder_ok", "folder_err"} {
		if !visited[want] {
			t.Errorf("preWarm did not attempt %q despite a sibling error (visited %v)", want, visited)
		}
	}
}

// TestPreWarm_NoCycles verifies that preWarm doesn't get stuck in cycles.
func TestPreWarm_NoCycles(t *testing.T) {
	cache := NewInodeCache()
	visitCount := make(map[string]int)

	fetcher := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		visitCount[parentID]++

		// Return a "cycle" structure: folder1 -> folder2 -> folder1
		switch parentID {
		case "root":
			return []graph.DriveItem{
				{
					ID:     "folder1",
					Name:   "Folder 1",
					Folder: &graph.Folder{ChildCount: 1},
				},
			}, nil
		case "folder1":
			return []graph.DriveItem{
				{
					ID:     "folder2",
					Name:   "Folder 2",
					Folder: &graph.Folder{ChildCount: 1},
				},
			}, nil
		case "folder2":
			// Try to cycle back
			return []graph.DriveItem{
				{
					ID:     "folder1",
					Name:   "Folder 1 (cycled)",
					Folder: &graph.Folder{ChildCount: 1},
				},
			}, nil
		}
		return []graph.DriveItem{}, nil
	}

	// depth 4 reaches the folder1→folder2→folder1 cycle (folder2 sits at level 3).
	err := preWarm(context.Background(), cache, fetcher, 4)
	if err != nil {
		t.Errorf("preWarm should handle cycles, got error %v", err)
	}

	// Verify each folder was visited exactly once (no infinite loops)
	for folderID, count := range visitCount {
		if count > 1 {
			t.Errorf("preWarm visited %q %d times (should be exactly 1)", folderID, count)
		}
	}
}

// TestDefaultMountConfig_PreWarmDepth verifies PreWarmDepth default and override.
func TestDefaultMountConfig_PreWarmDepth(t *testing.T) {
	t.Setenv("HOME", "/home/testuser")

	// Default value (no persisted config).
	config := DefaultMountConfig("test@outlook.com", nil)
	if config.PreWarmDepth != 2 {
		t.Errorf("Expected default PreWarmDepth 2, got %d", config.PreWarmDepth)
	}

	// Explicit persisted value overrides the default.
	five := 5
	config = DefaultMountConfig("test@outlook.com", &auth.AccountPersistedConfig{PreWarmDepth: &five})
	if config.PreWarmDepth != 5 {
		t.Errorf("Expected persisted PreWarmDepth 5, got %d", config.PreWarmDepth)
	}

	// Explicit persisted 0 disables pre-warming (must NOT fall back to default).
	zero := 0
	config = DefaultMountConfig("test@outlook.com", &auth.AccountPersistedConfig{PreWarmDepth: &zero})
	if config.PreWarmDepth != 0 {
		t.Errorf("Expected persisted PreWarmDepth 0 to disable pre-warming, got %d", config.PreWarmDepth)
	}

	// Absent (nil) uses the default.
	config = DefaultMountConfig("test@outlook.com", &auth.AccountPersistedConfig{})
	if config.PreWarmDepth != 2 {
		t.Errorf("Expected default PreWarmDepth 2 when persisted is nil, got %d", config.PreWarmDepth)
	}
}

// ──── Integration tests for preWarm with Mount ────

// TestMount_PreWarmDepth2_CacheHit verifies that with depth=2, Readdir
// of a pre-warmed folder completes from cache without additional Graph calls.
func TestMount_PreWarmDepth2_CacheHit(t *testing.T) {
	// Set up cache with mock data
	cache := NewInodeCache()

	// Create a mock fetcher that tracks calls
	fetcherCalls := 0
	fetcher := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		fetcherCalls++

		switch parentID {
		case "root":
			// Return root folders
			return []graph.DriveItem{
				{
					ID:     "folder1",
					Name:   "Folder 1",
					Folder: &graph.Folder{ChildCount: 1},
				},
				{
					ID:     "folder2",
					Name:   "Folder 2",
					Folder: &graph.Folder{ChildCount: 1},
				},
			}, nil
		case "folder1", "folder2":
			// Leaf folders return files (not folders, so no further traversal in depth=2)
			return []graph.DriveItem{
				{
					ID:   "file_" + parentID,
					Name: "file.txt",
					File: &graph.File{},
				},
			}, nil
		}
		return []graph.DriveItem{}, nil
	}

	// Run preWarm with depth=2
	ctx := context.Background()
	err := preWarm(ctx, cache, fetcher, 2)
	if err != nil {
		t.Fatalf("preWarm failed: %v", err)
	}

	// After pre-warm, we should have fetched root + folder1 + folder2 (3 times)
	preWarmFetches := fetcherCalls

	// Now simulate a subsequent Readdir from a pre-warmed folder
	// This should come from cache WITHOUT calling the fetcher again
	children, err := cache.GetChildren(ctx, "folder1", fetcher)
	if err != nil {
		t.Fatalf("GetChildren failed: %v", err)
	}

	// Verify children came from cache: fresh within the 60s TTL, so the fetcher
	// must not be called again.
	if fetcherCalls != preWarmFetches {
		t.Errorf("Expected cache hit after preWarm (no extra fetch), fetcher called %d -> %d", preWarmFetches, fetcherCalls)
	}

	// Verify we got the expected children
	if len(children) != 1 {
		t.Errorf("Expected 1 child, got %d", len(children))
	}
}

// TestMount_PreWarmDepth0_NoCaching verifies that with depth=0 (disabled),
// first access triggers a Graph call, not cached.
func TestMount_PreWarmDepth0_NoCaching(t *testing.T) {
	cache := NewInodeCache()

	fetcherCalls := 0
	fetcher := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		fetcherCalls++
		if parentID == "root" {
			return []graph.DriveItem{
				{
					ID:   "file",
					Name: "file.txt",
					File: &graph.File{},
				},
			}, nil
		}
		return []graph.DriveItem{}, nil
	}

	// Run preWarm with depth=0 (no-op)
	ctx := context.Background()
	err := preWarm(ctx, cache, fetcher, 0)
	if err != nil {
		t.Fatalf("preWarm failed: %v", err)
	}

	// No fetches should have occurred
	if fetcherCalls != 0 {
		t.Errorf("preWarm depth=0 should not call fetcher, but got %d calls", fetcherCalls)
	}

	// Now when we call GetChildren, it should trigger a fetcher call
	children, err := cache.GetChildren(ctx, "root", fetcher)
	if err != nil {
		t.Fatalf("GetChildren failed: %v", err)
	}

	// Verify fetcher was called exactly once
	if fetcherCalls != 1 {
		t.Errorf("Expected 1 fetcher call for first GetChildren, got %d", fetcherCalls)
	}

	if len(children) != 1 {
		t.Errorf("Expected 1 child, got %d", len(children))
	}
}

// TestMount_PreWarmDepth1_RootOnly verifies that depth=1 only pre-warms root.
func TestMount_PreWarmDepth1_RootOnly(t *testing.T) {
	cache := NewInodeCache()

	fetcherCalls := 0
	var fetchedParents []string

	fetcher := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		fetcherCalls++
		fetchedParents = append(fetchedParents, parentID)

		if parentID == "root" {
			// Return root with a file (not a folder, so no depth=2 traversal)
			return []graph.DriveItem{
				{
					ID:   "file",
					Name: "file.txt",
					File: &graph.File{},
				},
			}, nil
		}
		return []graph.DriveItem{}, nil
	}

	// Run preWarm with depth=1
	ctx := context.Background()
	err := preWarm(ctx, cache, fetcher, 1)
	if err != nil {
		t.Fatalf("preWarm failed: %v", err)
	}

	// Only root should have been fetched
	if fetcherCalls != 1 {
		t.Errorf("preWarm depth=1 should fetch exactly 1 time, got %d", fetcherCalls)
	}
	if len(fetchedParents) != 1 || fetchedParents[0] != "root" {
		t.Errorf("preWarm depth=1 should only fetch root, got %v", fetchedParents)
	}
}

// TestHandleFSPanic verifies the custom PanicHandler returns EIO (so the mount
// keeps serving after a handler panic) and logs the panic through zerolog.
func TestHandleFSPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)

	got := handleFSPanicWith(logger, "boom")
	if got != fuse.EIO {
		t.Fatalf("handleFSPanicWith() = %v, want fuse.EIO", got)
	}

	out := buf.String()
	if !strings.Contains(out, "panic in FUSE handler") {
		t.Fatalf("expected zerolog panic entry, got: %s", out)
	}
	if !strings.Contains(out, "boom") {
		t.Fatalf("expected panic value in log entry, got: %s", out)
	}
}
