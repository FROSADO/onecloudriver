package fs

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/auth"
	"github.com/frosado/onecloudriver/internal/graph"
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
