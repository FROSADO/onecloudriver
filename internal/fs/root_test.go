package fs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// netErrorStub simulates a transient network error (timeout) to test the
// offline mode without depending on DNS or real connections.
type netErrorStub struct{}

func (netErrorStub) Error() string   { return "simulated network timeout" }
func (netErrorStub) Timeout() bool   { return true }
func (netErrorStub) Temporary() bool { return true }

// transportErr always returns a simulated network error in RoundTrip.
type transportErr struct{}

func (transportErr) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, netErrorStub{}
}

// TestNewOneCloudFS verifies that NewOneCloudFS initializes correctly
func TestNewOneCloudFS(t *testing.T) {
	graphClient := graph.NewClient()
	tokenProvider := &mockTokenProvider{token: "test_token"}
	inodeCache := NewInodeCache()
	contentCache := &ContentCache{}

	root := NewOneCloudFS(graphClient, tokenProvider, inodeCache, contentCache, nil)

	if root == nil {
		t.Fatal("NewOneCloudFS returned nil")
	}
}

// TestOneCloudFS_Getattr verifies that the root is a directory
func TestOneCloudFS_Getattr(t *testing.T) {
	graphClient := graph.NewClient()
	tokenProvider := &mockTokenProvider{token: "test_token"}
	inodeCache := NewInodeCache()

	root := NewOneCloudFS(graphClient, tokenProvider, inodeCache, &ContentCache{}, nil)

	var out fuse.AttrOut
	errno := root.Getattr(context.Background(), nil, &out)

	if errno != 0 {
		t.Errorf("Expected errno 0, got %d", errno)
	}
	if out.Mode&syscall.S_IFDIR == 0 {
		t.Error("The root should be a directory (S_IFDIR)")
	}
}

// TestOneCloudFS_Readdir verifies that it lists the root content
func TestOneCloudFS_Readdir(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"value": [
				{"id": "file1", "name": "root-file.txt", "size": 100},
				{"id": "folder1", "name": "MiCarpeta", "folder": {"childCount": 3}}
			]
		}`))
	}))
	defer server.Close()

	graphClient := &graph.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}
	tokenProvider := &mockTokenProvider{token: "test_token"}
	inodeCache := NewInodeCache()

	root := NewOneCloudFS(graphClient, tokenProvider, inodeCache, &ContentCache{}, nil)

	stream, errno := root.Readdir(context.Background())

	if errno != 0 {
		t.Fatalf("Expected errno 0, got %d", errno)
	}

	entries := make([]fuse.DirEntry, 0, 4)
	for stream.HasNext() {
		entry, errno := stream.Next()
		if errno != 0 {
			t.Fatalf("Error reading stream: %d", errno)
		}
		entries = append(entries, entry)
	}

	if len(entries) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(entries))
	}
}

// TestOneCloudFS_Lookup verifies that Lookup finds a file
func TestOneCloudFS_Lookup(t *testing.T) {
	t.Skip("Requires mounted FUSE bridge (NewInode) — integration test")
}

// TestOneCloudFS_FetchChildren_OfflineFallback_StaleData verifies the path
// riskiest of the TTL freshness fix: when children are stale
// (TTL exceeded, e.g. restored from a previous session) and the network fails,
// fetchChildren must serve the local cache instead of propagating the error
// (offline mode of Phase 3), and must reset childrenCachedAt to avoid
// retry the network on every Readdir.
func TestOneCloudFS_FetchChildren_OfflineFallback_StaleData(t *testing.T) {
	graphClient := &graph.Client{
		BaseURL:    "http://example.invalid",
		HTTPClient: &http.Client{Transport: transportErr{}},
	}
	tokenProvider := &mockTokenProvider{token: "test_token"}
	inodeCache := NewInodeCache()
	root := NewOneCloudFS(graphClient, tokenProvider, inodeCache, &ContentCache{}, nil)

	// Cache with stale children (simulates a previous session, 2 hours ago)
	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "root", Name: "root", Folder: &graph.Folder{ChildCount: 1},
	})
	parent.SetChildren([]string{"cached1"})
	parent.Lock()
	parent.childrenCachedAt = time.Now().Add(-2 * time.Hour)
	parent.childrenLastAccess = time.Now().Add(-2 * time.Hour)
	parent.Unlock()
	inodeCache.Insert(parent)
	inodeCache.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "cached1", Name: "cached.txt"}))

	// 1. GetChildren with stale children + network error → fallback to cache
	children, err := inodeCache.GetChildren(context.Background(), "root", root.fetchChildren)
	if err != nil {
		t.Fatalf("GetChildren should serve the cache in offline mode, error: %v", err)
	}
	if len(children) != 1 || children["cached.txt"] == nil {
		t.Errorf("Should serve cached.txt from the cache, got: %v", children)
	}
	if !inodeCache.IsOffline() {
		t.Error("Offline mode should have been activated")
	}

	// 2. After the fallback, childrenCachedAt is reset → the next GetChildren
	//    is a HIT and does not retry the network (don't hammer the API offline).
	secondCalls := 0
	fetch2 := func(ctx context.Context, _ string) ([]graph.DriveItem, error) {
		secondCalls++
		return nil, errors.New("should not call the fetcher")
	}
	children2, err := inodeCache.GetChildren(context.Background(), "root", fetch2)
	if err != nil {
		t.Fatalf("Second GetChildren error: %v", err)
	}
	if secondCalls != 0 {
		t.Errorf("After the offline fallback, cachedAt should be fresh: there were %d extra fetches", secondCalls)
	}
	if children2["cached.txt"] == nil {
		t.Errorf("cached.txt should keep being served from cache, got: %v", children2)
	}
}

// TestOneCloudFS_FetchChildren_OfflineFallback_EvictedParent rebuilds the
// real scenario detected in the offline test: the parent's children list
// was evicted by the TTL sweep (children=nil), but the child inodes remain
// in the sync.Map with their ParentID (persisted by SerializeAll). With the network
// down, fetchChildren must rebuild the list from ItemsByParent instead
// of returning EIO.
func TestOneCloudFS_FetchChildren_OfflineFallback_EvictedParent(t *testing.T) {
	graphClient := &graph.Client{
		BaseURL:    "http://example.invalid",
		HTTPClient: &http.Client{Transport: transportErr{}},
	}
	tokenProvider := &mockTokenProvider{token: "test_token"}
	inodeCache := NewInodeCache()
	root := NewOneCloudFS(graphClient, tokenProvider, inodeCache, &ContentCache{}, nil)

	// Parent with evicted children (TTL sweep → children=nil)
	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 2},
	})
	inodeCache.Insert(parent)

	// Child inodes with ParentID (they remain in the sync.Map even though the parent
	// has been evicted)
	inodeCache.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "child1", Name: "a.txt", Size: 10,
		Parent: &graph.DriveItemParent{ID: "parent1"},
	}))
	inodeCache.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "child2", Name: "b.txt", Size: 20,
		Parent: &graph.DriveItemParent{ID: "parent1"},
	}))

	// Network error → must rebuild children from ItemsByParent
	children, err := inodeCache.GetChildren(context.Background(), "parent1", root.fetchChildren)
	if err != nil {
		t.Fatalf("GetChildren with an evicted parent offline should rebuild, error: %v", err)
	}
	if len(children) != 2 {
		t.Errorf("Expected 2 rebuilt children, got %d: %v", len(children), children)
	}
	if children["a.txt"] == nil || children["b.txt"] == nil {
		t.Errorf("a.txt and b.txt should be rebuilt, got: %v", children)
	}
	if !inodeCache.IsOffline() {
		t.Error("Offline mode should have been activated")
	}
}

// TestInodeCache_ItemsByParent verifies the reconstruction of the list of
// children by ParentID when the parent's list was evicted.
func TestInodeCache_ItemsByParent(t *testing.T) {
	cache := NewInodeCache()

	// Parent without children (evicted)
	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{},
	})
	cache.Insert(parent)

	cache.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "child1", Name: "a.txt", Size: 10,
		Parent: &graph.DriveItemParent{ID: "parent1"},
	}))
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "child2", Name: "b.txt", Size: 20,
		Parent: &graph.DriveItemParent{ID: "parent1"},
	}))
	// Inode of another parent (must not appear)
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "other", Name: "c.txt", Size: 30,
		Parent: &graph.DriveItemParent{ID: "parent2"},
	}))

	items := cache.ItemsByParent("parent1")
	if len(items) != 2 {
		t.Errorf("ItemsByParent(parent1) expected 2 items, got %d", len(items))
	}

	names := map[string]bool{}
	for _, it := range items {
		names[it.Name] = true
	}
	if !names["a.txt"] || !names["b.txt"] || names["c.txt"] {
		t.Errorf("ItemsByParent should return only children of parent1: %v", names)
	}
}

// TestInodeCache_SerializeAll_PersistsEvictedSubtree verifies that a subtree
// whose folder was evicted (children=nil) DOES survive the round-trip: the
// inodes with ParentID are persisted and ItemsByParent recovers them offline.
func TestInodeCache_SerializeAll_PersistsEvictedSubtree(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Session 1: subtree with an evicted parent
	cache1 := NewInodeCache()
	if err := cache1.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}

	// root fetched with child onedriver_tests
	root := NewInodeDriveItem(&graph.DriveItem{
		ID: "root", Name: "root", Folder: &graph.Folder{ChildCount: 1},
	})
	root.SetChildren([]string{"ontests"})
	cache1.Insert(root)

	// onedriver_tests: EVICTED list (children=nil) but its children persist
	ontests := NewInodeDriveItem(&graph.DriveItem{
		ID: "ontests", Name: "onedriver_tests", Folder: &graph.Folder{},
		Parent: &graph.DriveItemParent{ID: "root"},
	})
	cache1.Insert(ontests)
	cache1.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "paging", Name: "paging", Folder: &graph.Folder{},
		Parent: &graph.DriveItemParent{ID: "ontests"},
	}))
	cache1.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "delta", Name: "delta", Folder: &graph.Folder{},
		Parent: &graph.DriveItemParent{ID: "ontests"},
	}))

	if err := cache1.SerializeAll(); err != nil {
		t.Fatalf("SerializeAll error: %v", err)
	}
	cache1.Close()

	// Session 2: restore and rebuild the evicted subtree offline
	cache2 := NewInodeCache()
	if err := cache2.InitBoltDB(dbPath); err != nil {
		t.Fatalf("Second InitBoltDB error: %v", err)
	}
	defer cache2.Close()

	// The child inodes of the subtree must exist in memory
	if cache2.Get("paging") == nil || cache2.Get("delta") == nil {
		t.Fatal("paging/delta should be persisted even if onedriver_tests was evicted")
	}

	// ItemsByParent rebuilds the onedriver_tests list
	items := cache2.ItemsByParent("ontests")
	if len(items) != 2 {
		t.Errorf("ItemsByParent(ontests) expected 2 items, got %d", len(items))
	}
}

// TestIsNetworkError_RealProxyError reproduces the EXACT error produced by
// http.Client con un proxy roto (HTTPS_PROXY=http://127.0.0.1:1), que es el
// real scenario of the end-to-end offline test. Verifies that isNetworkError
// detects it and that the Graph layer wrapping does not hide it.
func TestIsNetworkError_RealProxyError(t *testing.T) {
	// HTTP client with proxy pointing to a closed port → connection refused
	transport := &http.Transport{
		Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: "127.0.0.1:1"}),
	}
	client := &http.Client{Transport: transport}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://graph.microsoft.com/v1.0/me/drive/root/delta", nil)
	if err != nil {
		t.Fatalf("error creating request: %v", err)
	}
	_, err = client.Do(req)
	if err == nil {
		t.Fatal("The broken proxy should produce an error")
	}

	// Same wrapping as the Graph layer (drive_item.go:66)
	wrapped := fmt.Errorf("network error querying Graph: %w", err)
	if !isNetworkError(wrapped) {
		t.Errorf("isNetworkError should detect the REAL error from the broken proxy. Error: %v", err)
	}
	if !isNetworkError(err) {
		t.Errorf("Direct isNetworkError should detect the REAL error from the broken proxy. Error: %v", err)
	}
}
func TestOneCloudFS_Lookup_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value": []}`))
	}))
	defer server.Close()

	graphClient := &graph.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}
	tokenProvider := &mockTokenProvider{token: "test_token"}
	inodeCache := NewInodeCache()

	root := NewOneCloudFS(graphClient, tokenProvider, inodeCache, &ContentCache{}, nil)

	var out fuse.EntryOut
	_, errno := root.Lookup(context.Background(), "no_existe.txt", &out)

	if errno != syscall.ENOENT {
		t.Errorf("Expected errno ENOENT, got %d", errno)
	}
}
