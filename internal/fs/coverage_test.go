package fs

import (
	"testing"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/frosado/onecloudriver/internal/auth"
	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/hanwen/go-fuse/v2/fs"
)

// =============================================================================
// resolveNewParentID - edge cases
// =============================================================================

type unknownEmbedder struct {
	fs.Inode
}

func TestResolveNewParentID(t *testing.T) {
	t.Run("OneCloudFS returns root", func(t *testing.T) {
		ocfs := &OneCloudFS{}
		if got := resolveNewParentID(ocfs); got != "root" {
			t.Errorf("expected 'root', got %q", got)
		}
	})

	t.Run("DriveItemNode returns folder ID", func(t *testing.T) {
		inode := &Inode{}
		inode.DriveItem.ID = "folder-123"
		node := &DriveItemNode{inode: inode}
		if got := resolveNewParentID(node); got != "folder-123" {
			t.Errorf("expected 'folder-123', got %q", got)
		}
	})

	t.Run("unknown type returns empty string", func(t *testing.T) {
		unknown := &unknownEmbedder{}
		if got := resolveNewParentID(unknown); got != "" {
			t.Errorf("expected empty string for unknown type, got %q", got)
		}
	})
}

// =============================================================================
// Inode.newLocalID
// =============================================================================

func TestNewLocalID(t *testing.T) {
	// Generate multiple IDs and verify they are all unique
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := newLocalID()
		if ids[id] {
			t.Errorf("duplicate ID generated: %q", id)
		}
		ids[id] = true

		// Verify format: "local-" + 32 hex chars
		if len(id) != len("local-")+32 {
			t.Errorf("unexpected ID length: %d for %q", len(id), id)
		}
		if id[:len("local-")] != "local-" {
			t.Errorf("ID doesn't start with 'local-': %q", id)
		}
	}
}

// =============================================================================
// Inode.Path - edge cases
// =============================================================================

func TestInode_Path(t *testing.T) {
	t.Run("root folder returns /", func(t *testing.T) {
		inode := &Inode{}
		inode.DriveItem.Name = "root"
		// ParentID returns "" because Parent is nil
		if got := inode.Path(); got != "/" {
			t.Errorf("expected '/', got %q", got)
		}
	})

	t.Run("non-root with parent returns name", func(t *testing.T) {
		inode := &Inode{}
		inode.DriveItem.Name = "Documents"
		inode.DriveItem.Parent = &graph.DriveItemParent{ID: "parent-123"}
		if got := inode.Path(); got != "Documents" {
			t.Errorf("expected 'Documents', got %q", got)
		}
	})

	t.Run("file without parent returns name", func(t *testing.T) {
		inode := &Inode{}
		inode.DriveItem.Name = "file.txt"
		if got := inode.Path(); got != "file.txt" {
			t.Errorf("expected 'file.txt', got %q", got)
		}
	})

	t.Run("root folder with parent ID is not root", func(t *testing.T) {
		inode := &Inode{}
		inode.DriveItem.Name = "root"
		inode.DriveItem.Parent = &graph.DriveItemParent{ID: "some-parent"}
		if got := inode.Path(); got != "root" {
			t.Errorf("expected 'root' (has parent so not treated as root), got %q", got)
		}
	})
}

// =============================================================================
// DefaultMountConfig - more coverage
// =============================================================================

func TestDefaultMountConfig(t *testing.T) {
	t.Run("with nil persisted config", func(t *testing.T) {
		cfg := DefaultMountConfig("test@example.com", nil)

		if cfg.CacheTTL != 60*time.Second {
			t.Errorf("expected 60s CacheTTL, got %v", cfg.CacheTTL)
		}
		if cfg.CacheMaxEntries != 2000 {
			t.Errorf("expected 2000 CacheMaxEntries, got %d", cfg.CacheMaxEntries)
		}
		if cfg.MaxUploadsInFlight != 5 {
			t.Errorf("expected 5 MaxUploadsInFlight, got %d", cfg.MaxUploadsInFlight)
		}
	})

	t.Run("with fully populated persisted config", func(t *testing.T) {
		persisted := &auth.AccountPersistedConfig{
			CacheDir:           "/custom/cache",
			CacheTTL:           120 * time.Second,
			CacheMaxEntries:    5000,
			CacheMaxSize:       int64(1 * humanize.GiByte),
			DeltaInterval:      10 * time.Minute,
			MaxUploadsInFlight: 10,
			MaxUploadRetries:   3,
			GraphRetries:       5,
			HTTPTimeout:        30 * time.Second,
		}

		cfg := DefaultMountConfig("test@example.com", persisted)

		if cfg.CacheDir != "/custom/cache" {
			t.Errorf("expected /custom/cache, got %q", cfg.CacheDir)
		}
		if cfg.CacheTTL != 120*time.Second {
			t.Errorf("expected 120s CacheTTL, got %v", cfg.CacheTTL)
		}
		if cfg.CacheMaxEntries != 5000 {
			t.Errorf("expected 5000 CacheMaxEntries, got %d", cfg.CacheMaxEntries)
		}
		if cfg.CacheMaxSize != int64(1*humanize.GiByte) {
			t.Errorf("expected 1GiB CacheMaxSize, got %d", cfg.CacheMaxSize)
		}
		if cfg.DeltaInterval != 10*time.Minute {
			t.Errorf("expected 10m DeltaInterval, got %v", cfg.DeltaInterval)
		}
		if cfg.MaxUploadsInFlight != 10 {
			t.Errorf("expected 10 MaxUploadsInFlight, got %d", cfg.MaxUploadsInFlight)
		}
		if cfg.MaxUploadRetries != 3 {
			t.Errorf("expected 3 MaxUploadRetries, got %d", cfg.MaxUploadRetries)
		}
		if cfg.GraphRetries != 5 {
			t.Errorf("expected 5 GraphRetries, got %d", cfg.GraphRetries)
		}
		if cfg.HTTPTimeout != 30*time.Second {
			t.Errorf("expected 30s HTTPTimeout, got %v", cfg.HTTPTimeout)
		}
	})

	t.Run("partially populated persisted config", func(t *testing.T) {
		persisted := &auth.AccountPersistedConfig{
			CacheTTL:         90 * time.Second,
			MaxUploadRetries: 7,
		}

		cfg := DefaultMountConfig("test@example.com", persisted)

		// Set fields should override
		if cfg.CacheTTL != 90*time.Second {
			t.Errorf("expected 90s CacheTTL override, got %v", cfg.CacheTTL)
		}
		if cfg.MaxUploadRetries != 7 {
			t.Errorf("expected 7 MaxUploadRetries override, got %d", cfg.MaxUploadRetries)
		}

		// Unset fields should remain at defaults
		if cfg.CacheMaxEntries != 2000 {
			t.Errorf("expected default CacheMaxEntries, got %d", cfg.CacheMaxEntries)
		}
		if cfg.MaxUploadsInFlight != 5 {
			t.Errorf("expected default MaxUploadsInFlight, got %d", cfg.MaxUploadsInFlight)
		}
	})
}

// =============================================================================
// Inode.IsChildrenFetched + HasChildren
// =============================================================================

func TestInode_ChildrenState(t *testing.T) {
	t.Run("IsChildrenFetched false when children is nil", func(t *testing.T) {
		inode := &Inode{}
		if inode.IsChildrenFetched() {
			t.Error("expected false when children is nil")
		}
	})

	t.Run("IsChildrenFetched true when children is empty slice", func(t *testing.T) {
		inode := &Inode{children: []string{}}
		if !inode.IsChildrenFetched() {
			t.Error("expected true when children is empty slice (fetched but empty)")
		}
	})

	t.Run("HasChildren with ChildCount > 0", func(t *testing.T) {
		inode := &Inode{}
		inode.DriveItem.Folder = &graph.Folder{ChildCount: 5}
		if !inode.HasChildren() {
			t.Error("expected HasChildren true when ChildCount > 0")
		}
	})

	t.Run("HasChildren with children slice populated", func(t *testing.T) {
		inode := &Inode{children: []string{"child-1", "child-2"}}
		if !inode.HasChildren() {
			t.Error("expected HasChildren true when children slice is populated")
		}
	})

	t.Run("HasChildren false for empty folder", func(t *testing.T) {
		inode := &Inode{children: []string{}}
		inode.DriveItem.Folder = &graph.Folder{ChildCount: 0}
		if inode.HasChildren() {
			t.Error("expected HasChildren false for empty folder with no children")
		}
	})

	// Regression for #129: the children were fetched and then all removed
	// through the mount (RemoveChild), so the local list is empty but the
	// remote childCount is still stale (the delta poll has not refreshed it
	// yet). The local list must be authoritative: the folder is empty.
	t.Run("HasChildren false when children emptied locally with stale ChildCount", func(t *testing.T) {
		inode := &Inode{children: []string{}}
		inode.DriveItem.Folder = &graph.Folder{ChildCount: 5}
		if inode.HasChildren() {
			t.Error("expected HasChildren false: local list fetched and emptied, stale ChildCount ignored")
		}
	})
}

// =============================================================================
// Inode.Subdir
// =============================================================================

func TestInode_SubdirState(t *testing.T) {
	inode := &Inode{}
	inode.SetSubdir(10)
	if got := inode.Subdir(); got != 10 {
		t.Errorf("expected 10, got %d", got)
	}

	inode.SetSubdir(0)
	if got := inode.Subdir(); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

// =============================================================================
// Inode.SetHasChanges + HasChanges
// =============================================================================

func TestInode_HasChanges(t *testing.T) {
	inode := &Inode{}
	if inode.HasChanges() {
		t.Error("expected HasChanges false initially")
	}

	inode.SetHasChanges(true)
	if !inode.HasChanges() {
		t.Error("expected HasChanges true after setting")
	}

	inode.SetHasChanges(false)
	if inode.HasChanges() {
		t.Error("expected HasChanges false after clearing")
	}
}

// =============================================================================
// isLocalID
// =============================================================================

func TestIsLocalID(t *testing.T) {
	t.Run("valid local ID", func(t *testing.T) {
		if !isLocalID("local-abc123def456") {
			t.Error("expected true for local ID")
		}
	})

	t.Run("too short", func(t *testing.T) {
		if isLocalID("local") {
			t.Error("expected false for short ID")
		}
	})

	t.Run("non-local ID", func(t *testing.T) {
		if isLocalID("01BYE5RZABC123") {
			t.Error("expected false for Graph API ID")
		}
	})

	t.Run("exactly the prefix", func(t *testing.T) {
		if isLocalID("local-") {
			t.Error("expected false for exactly 'local-'")
		}
	})
}
