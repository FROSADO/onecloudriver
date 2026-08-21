package fs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"

	"github.com/frosado/onecloudriver/internal/graph"
)

// A closed cache must reject mutations: returning nil made the FUSE layer
// acknowledge writes whose bytes were dropped.
func TestContentCache_MutationsAfterCloseReportError(t *testing.T) {
	cache, err := NewContentCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewContentCache: %v", err)
	}
	cache.CloseAll()

	if err := cache.Insert("id", []byte("data")); !errors.Is(err, ErrCacheClosed) {
		t.Errorf("Insert after close: got %v, want ErrCacheClosed", err)
	}
	if _, err := cache.InsertStream("id", strings.NewReader("data")); !errors.Is(err, ErrCacheClosed) {
		t.Errorf("InsertStream after close: got %v, want ErrCacheClosed", err)
	}
	if _, err := cache.WriteAt("id", []byte("data"), 0); !errors.Is(err, ErrCacheClosed) {
		t.Errorf("WriteAt after close: got %v, want ErrCacheClosed", err)
	}
	if err := cache.Delete("id"); !errors.Is(err, ErrCacheClosed) {
		t.Errorf("Delete after close: got %v, want ErrCacheClosed", err)
	}
}

// The download must report a failure of either half of the pipe, and keep the
// Graph error when both fail: the previous selection returned success when
// only the cache write failed, and dropped the root cause when both did.
func TestDownloadError_KeepsEveryFailure(t *testing.T) {
	graphErr := errors.New("graph download failed")
	cacheErr := errors.New("cache write failed")

	if err := downloadError(nil, nil); err != nil {
		t.Errorf("both halves succeeded: got %v, want nil", err)
	}
	if err := downloadError(nil, cacheErr); !errors.Is(err, cacheErr) {
		t.Errorf("cache write failure: got %v, want the cache error", err)
	}
	if err := downloadError(graphErr, nil); !errors.Is(err, graphErr) {
		t.Errorf("download failure: got %v, want the Graph error", err)
	}
	err := downloadError(graphErr, cacheErr)
	if !errors.Is(err, graphErr) || !errors.Is(err, cacheErr) {
		t.Errorf("both halves failed: got %v, want both errors", err)
	}
}

// End-to-end counterpart: a download whose content cannot be cached must be
// reported to the kernel as an I/O error, never as a successful read.
func TestDriveItemNode_DownloadContent_ReportsCacheWriteError(t *testing.T) {
	const content = "hello"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			w.Header().Set("Content-Range", "bytes 0-4/5")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte(content))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": "file1", "name": "file.txt", "size": 5}`))
	}))
	defer server.Close()

	contentCache, err := NewContentCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewContentCache: %v", err)
	}
	// Closing the cache makes every write fail, simulating a full or
	// unwritable cache directory.
	contentCache.CloseAll()

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "file1", Name: "file.txt", Size: 5}),
		nodeDeps: nodeDeps{
			graphClient:   &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()},
			tokenProvider: &mockTokenProvider{token: "test_token"},
			inodeCache:    NewInodeCache(),
			contentCache:  contentCache,
		},
	}

	if errno := node.downloadContent(context.Background(), "file1"); errno != syscall.EIO {
		t.Errorf("downloadContent with a failing content cache: got errno %v, want EIO", errno)
	}
}

// The blocked-folder-deletion retry must key off a sentinel error, not the
// message text, so applyPage keeps retrying the right items.
func TestDeltaSync_ApplyDelta_NonEmptyFolder_ReturnsSentinel(t *testing.T) {
	inodeCache := NewInodeCache()
	contentCache, err := NewContentCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewContentCache: %v", err)
	}

	parent := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	parent.SetChildren([]string{"folder1"})
	inodeCache.Insert(parent)

	folder := NewInodeDriveItem(&graph.DriveItem{
		ID: "folder1", Name: "nonempty", Folder: &graph.Folder{},
		Parent: &graph.DriveItemParent{ID: "root"},
	})
	folder.SetChildren([]string{"child1"})
	inodeCache.Insert(folder)

	ds := NewDeltaSync(nil, nil, inodeCache, contentCache)
	delta := &graph.DeltaItem{
		DriveItem: graph.DriveItem{
			ID: "folder1", Name: "nonempty", Folder: &graph.Folder{},
			Parent: &graph.DriveItemParent{ID: "root"},
		},
		Deleted: &graph.DeletedState{State: "deleted"},
	}

	err = ds.applyDelta(delta)
	if !errors.Is(err, errDirNotEmpty) {
		t.Fatalf("applyDelta on a non-empty folder: got %v, want errDirNotEmpty", err)
	}
	if !ds.applyOrReport(delta) {
		t.Error("a deletion blocked by children must be reported as retryable")
	}
}

// applyPage must hand a still-blocked deletion back to the caller so a later
// page (which may delete the children) can retry it.
func TestDeltaSync_ApplyPage_ReturnsStillBlockedDeletions(t *testing.T) {
	inodeCache := NewInodeCache()
	contentCache, err := NewContentCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewContentCache: %v", err)
	}

	root := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	root.SetChildren([]string{"folder1"})
	inodeCache.Insert(root)
	folder := NewInodeDriveItem(&graph.DriveItem{
		ID: "folder1", Name: "nonempty", Folder: &graph.Folder{},
		Parent: &graph.DriveItemParent{ID: "root"},
	})
	folder.SetChildren([]string{"child1"})
	inodeCache.Insert(folder)

	ds := NewDeltaSync(nil, nil, inodeCache, contentCache)
	items := []graph.DeltaItem{{
		DriveItem: graph.DriveItem{
			ID: "folder1", Name: "nonempty", Folder: &graph.Folder{},
			Parent: &graph.DriveItemParent{ID: "root"},
		},
		Deleted: &graph.DeletedState{State: "deleted"},
	}}

	pending := ds.applyPage(items, nil)
	if len(pending) != 1 || pending[0].ID != "folder1" {
		t.Fatalf("applyPage should return the blocked deletion, got %+v", pending)
	}
}
