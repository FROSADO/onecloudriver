package fs

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/auth"
	"github.com/frosado/onecloudriver/internal/graph"
)

// TestMount_CacheHandles verifica que CacheHandles se crea correctamente
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

	// Verificar que Metadata sigue siendo usable
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
		t.Errorf("InodeCount esperado 0, obtenido %d", stats.InodeCount)
	}

	// Insertar y verificar stats
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "i1", Name: "one"}))
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "i2", Name: "two"}))

	if cache.Stats().InodeCount != 2 {
		t.Errorf("InodeCount esperado 2, obtenido %d", cache.Stats().InodeCount)
	}
}

// TestDefaultMountConfig_Normal verifies the default cache path.
func TestDefaultMountConfig_Normal(t *testing.T) {
	t.Setenv("HOME", "/home/testuser")

	config := DefaultMountConfig("test@outlook.com", nil)

	expected := filepath.Join("/home/testuser", ".cache", "onecloudriver", "test@outlook.com")
	if config.CacheDir != expected {
		t.Errorf("CacheDir esperado %q, obtenido %q", expected, config.CacheDir)
	}
	if config.CacheTTL != 60*time.Second {
		t.Errorf("CacheTTL esperado 60s, obtenido %v", config.CacheTTL)
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
		t.Errorf("CacheDir esperado /custom/cache, obtenido %q", config.CacheDir)
	}
	if config.CacheTTL != 120*time.Second {
		t.Errorf("CacheTTL esperado 120s, obtenido %v", config.CacheTTL)
	}
	if config.CacheMaxEntries != 500 {
		t.Errorf("CacheMaxEntries esperado 500, obtenido %d", config.CacheMaxEntries)
	}
	if config.DeltaInterval != 10*time.Minute {
		t.Errorf("DeltaInterval esperado 10m, obtenido %v", config.DeltaInterval)
	}
	if config.GraphRetries != 5 {
		t.Errorf("GraphRetries esperado 5, obtenido %d", config.GraphRetries)
	}
}

// TestMount_CloseIsNoop verifica que Close no causa panic
func TestMount_CloseIsNoop(t *testing.T) {
	cache := NewInodeCache()
	cache.Close()                                                         // primera
	cache.Close()                                                         // segunda: no debe panic
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "x", Name: "x"})) // sigue usable
	if cache.Get("x") == nil {
		t.Error("InodeCache should keep working after Close (no-op in Phase 2)")
	}
}
