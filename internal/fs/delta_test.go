package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// ──── applyDelta: deletion ────

func TestDeltaSync_ApplyDelta_Deleted(t *testing.T) {
	inodeCache := NewInodeCache()
	contentCache, _ := NewContentCache(t.TempDir())

	parent := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	parent.SetChildren([]string{"file1"})
	inodeCache.Insert(parent)

	child := NewInodeDriveItem(&graph.DriveItem{ID: "file1", Name: "deleteme.txt", Parent: &graph.DriveItemParent{ID: "root"}})
	inodeCache.Insert(child)

	ds := NewDeltaSync(nil, nil, inodeCache, contentCache)

	delta := &graph.DeltaItem{
		DriveItem: graph.DriveItem{ID: "file1", Name: "deleteme.txt", Parent: &graph.DriveItemParent{ID: "root"}},
		Deleted:   &graph.DeletedState{State: "deleted"},
	}

	err := ds.applyDelta(delta)
	if err != nil {
		t.Fatalf("applyDelta error: %v", err)
	}

	if inodeCache.Get("file1") != nil {
		t.Error("file1 should have been removed from InodeCache")
	}
}

func TestDeltaSync_ApplyDelta_Deleted_NonEmptyFolder(t *testing.T) {
	inodeCache := NewInodeCache()
	contentCache, _ := NewContentCache(t.TempDir())

	parent := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	parent.SetChildren([]string{"folder1"})
	inodeCache.Insert(parent)

	folder := NewInodeDriveItem(&graph.DriveItem{ID: "folder1", Name: "nonempty", Folder: &graph.Folder{}, Parent: &graph.DriveItemParent{ID: "root"}})
	folder.SetChildren([]string{"child1"})
	inodeCache.Insert(folder)

	ds := NewDeltaSync(nil, nil, inodeCache, contentCache)

	delta := &graph.DeltaItem{
		DriveItem: graph.DriveItem{ID: "folder1", Name: "nonempty", Folder: &graph.Folder{}, Parent: &graph.DriveItemParent{ID: "root"}},
		Deleted:   &graph.DeletedState{State: "deleted"},
	}

	err := ds.applyDelta(delta)
	if err == nil {
		t.Error("Expected error 'directory is non-empty'")
	}
	if inodeCache.Get("folder1") == nil {
		t.Error("folder1 should not have been removed (non-empty folder)")
	}
}

// ──── applyDelta: creation ────

func TestDeltaSync_ApplyDelta_NewItem(t *testing.T) {
	inodeCache := NewInodeCache()
	contentCache, _ := NewContentCache(t.TempDir())

	parent := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	parent.SetChildren([]string{})
	inodeCache.Insert(parent)

	ds := NewDeltaSync(nil, nil, inodeCache, contentCache)

	delta := &graph.DeltaItem{
		DriveItem: graph.DriveItem{
			ID: "newfile", Name: "nuevo.txt", Size: 100,
			Parent: &graph.DriveItemParent{ID: "root"},
		},
	}

	err := ds.applyDelta(delta)
	if err != nil {
		t.Fatalf("applyDelta error: %v", err)
	}

	if inodeCache.Get("newfile") == nil {
		t.Error("newfile should exist in InodeCache")
	}

	updatedParent := inodeCache.Get("root")
	found := false
	for _, id := range updatedParent.Children() {
		if id == "newfile" {
			found = true
			break
		}
	}
	if !found {
		t.Error("root should have 'newfile' as a child")
	}
}

func TestDeltaSync_ApplyDelta_NewItem_ParentNotInCache(t *testing.T) {
	inodeCache := NewInodeCache()
	contentCache, _ := NewContentCache(t.TempDir())

	ds := NewDeltaSync(nil, nil, inodeCache, contentCache)

	delta := &graph.DeltaItem{
		DriveItem: graph.DriveItem{
			ID: "orphan", Name: "huerfano.txt", Size: 100,
			Parent: &graph.DriveItemParent{ID: "unknown_parent"},
		},
	}

	err := ds.applyDelta(delta)
	if err != nil {
		t.Fatalf("applyDelta should not fail if the parent is not cached: %v", err)
	}
	if inodeCache.Get("orphan") != nil {
		t.Error("orphan should not exist if its parent is not cached")
	}
}

// ──── applyDelta: rename/move ────

func TestDeltaSync_ApplyDelta_Rename(t *testing.T) {
	inodeCache := NewInodeCache()
	contentCache, _ := NewContentCache(t.TempDir())

	parent := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	parent.SetChildren([]string{"file1"})
	inodeCache.Insert(parent)

	child := NewInodeDriveItem(&graph.DriveItem{ID: "file1", Name: "viejo.txt", Parent: &graph.DriveItemParent{ID: "root"}})
	inodeCache.Insert(child)

	ds := NewDeltaSync(nil, nil, inodeCache, contentCache)

	delta := &graph.DeltaItem{
		DriveItem: graph.DriveItem{
			ID: "file1", Name: "nuevo.txt",
			Parent: &graph.DriveItemParent{ID: "root"},
		},
	}

	err := ds.applyDelta(delta)
	if err != nil {
		t.Fatalf("applyDelta error: %v", err)
	}

	updated := inodeCache.Get("file1")
	if updated.Name() != "nuevo.txt" {
		t.Errorf("Expected Name 'nuevo.txt', got %q", updated.Name())
	}
}

func TestDeltaSync_ApplyDelta_MoveParent(t *testing.T) {
	inodeCache := NewInodeCache()
	contentCache, _ := NewContentCache(t.TempDir())

	oldParent := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	oldParent.SetChildren([]string{"file1"})
	inodeCache.Insert(oldParent)

	newParent := NewInodeDriveItem(&graph.DriveItem{ID: "dest", Name: "dest", Folder: &graph.Folder{}})
	newParent.SetChildren([]string{})
	inodeCache.Insert(newParent)

	child := NewInodeDriveItem(&graph.DriveItem{ID: "file1", Name: "doc.txt", Parent: &graph.DriveItemParent{ID: "root"}})
	inodeCache.Insert(child)

	ds := NewDeltaSync(nil, nil, inodeCache, contentCache)

	delta := &graph.DeltaItem{
		DriveItem: graph.DriveItem{
			ID: "file1", Name: "doc.txt",
			Parent: &graph.DriveItemParent{ID: "dest"},
		},
	}

	err := ds.applyDelta(delta)
	if err != nil {
		t.Fatalf("applyDelta error: %v", err)
	}

	oldP := inodeCache.Get("root")
	for _, id := range oldP.Children() {
		if id == "file1" {
			t.Error("root should not have file1 as a child")
		}
	}

	newP := inodeCache.Get("dest")
	found := false
	for _, id := range newP.Children() {
		if id == "file1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("dest should have file1 as a child")
	}
}

// ──── applyDelta: modified content ────

func TestDeltaSync_ApplyDelta_ContentChanged(t *testing.T) {
	inodeCache := NewInodeCache()
	contentCache, _ := NewContentCache(t.TempDir())

	parent := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	parent.SetChildren([]string{"file1"})
	inodeCache.Insert(parent)

	oldTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	child := NewInodeDriveItem(&graph.DriveItem{
		ID: "file1", Name: "data.txt", Size: 50,
		ETag: "old-etag", ModTime: &oldTime,
		Parent: &graph.DriveItemParent{ID: "root"},
	})
	inodeCache.Insert(child)

	contentCache.Insert("file1", []byte("old content"))

	ds := NewDeltaSync(nil, nil, inodeCache, contentCache)

	newTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	delta := &graph.DeltaItem{
		DriveItem: graph.DriveItem{
			ID: "file1", Name: "data.txt", Size: 100,
			ETag: "new-etag", ModTime: &newTime,
			File:   &graph.File{},
			Parent: &graph.DriveItemParent{ID: "root"},
		},
	}

	err := ds.applyDelta(delta)
	if err != nil {
		t.Fatalf("applyDelta error: %v", err)
	}

	updated := inodeCache.Get("file1")
	if updated.DriveItem.Size != 100 {
		t.Errorf("Expected size 100, got %d", updated.DriveItem.Size)
	}
	if updated.DriveItem.ETag != "new-etag" {
		t.Errorf("Expected ETag 'new-etag', got %q", updated.DriveItem.ETag)
	}

	if contentCache.HasContent("file1") {
		t.Error("ContentCache should have been cleared after a remote change")
	}
}

// TestDeltaSync_ApplyDelta_ContentHashMatch_KeepsCache verifies that when a
// remote delta arrives but the locally cached content already matches the
// remote quickXorHash, the cache is preserved (no unnecessary re-download).
func TestDeltaSync_ApplyDelta_ContentHashMatch_KeepsCache(t *testing.T) {
	inodeCache := NewInodeCache()
	contentCache, _ := NewContentCache(t.TempDir())

	parent := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	parent.SetChildren([]string{"file1"})
	inodeCache.Insert(parent)

	oldTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	child := NewInodeDriveItem(&graph.DriveItem{
		ID: "file1", Name: "data.txt", Size: 12,
		ETag: "old-etag", ModTime: &oldTime,
		Parent: &graph.DriveItemParent{ID: "root"},
	})
	inodeCache.Insert(child)

	content := []byte("same content")
	contentCache.Insert("file1", content)

	ds := NewDeltaSync(nil, nil, inodeCache, contentCache)

	newTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	delta := &graph.DeltaItem{
		DriveItem: graph.DriveItem{
			ID: "file1", Name: "data.txt", Size: uint64(len(content)),
			ETag: "new-etag", ModTime: &newTime,
			File:   &graph.File{Hashes: graph.Hashes{QuickXorHash: graph.SumQuickXORHash(content)}},
			Parent: &graph.DriveItemParent{ID: "root"},
		},
	}

	if err := ds.applyDelta(delta); err != nil {
		t.Fatalf("applyDelta error: %v", err)
	}

	if !contentCache.HasContent("file1") {
		t.Error("cached content must be kept when it already matches the remote quickXorHash")
	}
	updated := inodeCache.Get("file1")
	if updated.DriveItem.ETag != "new-etag" {
		t.Errorf("expected metadata updated to 'new-etag', got %q", updated.DriveItem.ETag)
	}
}

// mockUploadQuery implements the uploadQuery interface for applyDelta tests.
type mockUploadQuery struct{ pending map[string]bool }

func (m mockUploadQuery) HasPendingUpload(id string) bool { return m.pending[id] }

func TestDeltaSync_ApplyDelta_PreservesLocalChanges(t *testing.T) {
	inodeCache := NewInodeCache()
	contentCache, _ := NewContentCache(t.TempDir())

	parent := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	parent.SetChildren([]string{"file1"})
	inodeCache.Insert(parent)

	oldTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	child := NewInodeDriveItem(&graph.DriveItem{
		ID: "file1", Name: "data.txt", Size: 50,
		ETag: "old-etag", ModTime: &oldTime,
		Parent: &graph.DriveItemParent{ID: "root"},
	})
	child.SetHasChanges(true) // local edit not yet uploaded
	inodeCache.Insert(child)

	contentCache.Insert("file1", []byte("local unsynced edit"))

	ds := NewDeltaSync(nil, nil, inodeCache, contentCache)

	newTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	delta := &graph.DeltaItem{
		DriveItem: graph.DriveItem{
			ID: "file1", Name: "data.txt", Size: 100,
			ETag: "new-etag", ModTime: &newTime,
			File:   &graph.File{},
			Parent: &graph.DriveItemParent{ID: "root"},
		},
	}

	if err := ds.applyDelta(delta); err != nil {
		t.Fatalf("applyDelta error: %v", err)
	}

	updated := inodeCache.Get("file1")
	if !updated.HasChanges() {
		t.Error("hasChanges should stay true (local edit not uploaded)")
	}
	if updated.DriveItem.ETag != "old-etag" {
		t.Errorf("expected ETag 'old-etag' preserved, got %q", updated.DriveItem.ETag)
	}
	if updated.DriveItem.Size != 50 {
		t.Errorf("expected Size 50 preserved, got %d", updated.DriveItem.Size)
	}
	if !contentCache.HasContent("file1") {
		t.Error("local unsynced content must NOT be deleted from ContentCache")
	}
}

func TestDeltaSync_ApplyDelta_PreservesPendingUpload(t *testing.T) {
	inodeCache := NewInodeCache()
	contentCache, _ := NewContentCache(t.TempDir())

	parent := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	parent.SetChildren([]string{"file1"})
	inodeCache.Insert(parent)

	oldTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	child := NewInodeDriveItem(&graph.DriveItem{
		ID: "file1", Name: "data.txt", Size: 50,
		ETag: "old-etag", ModTime: &oldTime,
		Parent: &graph.DriveItemParent{ID: "root"},
	})
	inodeCache.Insert(child)
	contentCache.Insert("file1", []byte("local snapshot being uploaded"))

	ds := NewDeltaSync(nil, nil, inodeCache, contentCache)
	ds.SetUploadQuery(mockUploadQuery{pending: map[string]bool{"file1": true}})

	newTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	delta := &graph.DeltaItem{
		DriveItem: graph.DriveItem{
			ID: "file1", Name: "data.txt", Size: 100,
			ETag: "new-etag", ModTime: &newTime,
			File:   &graph.File{},
			Parent: &graph.DriveItemParent{ID: "root"},
		},
	}

	if err := ds.applyDelta(delta); err != nil {
		t.Fatalf("applyDelta error: %v", err)
	}

	updated := inodeCache.Get("file1")
	if updated.DriveItem.ETag != "old-etag" {
		t.Errorf("expected ETag 'old-etag' preserved (upload pending), got %q", updated.DriveItem.ETag)
	}
	if !contentCache.HasContent("file1") {
		t.Error("content must NOT be deleted while an upload is pending")
	}
}

// ──── PollAndApply con mock HTTP ────

func TestDeltaSync_PollAndApply_Success(t *testing.T) {
	var pollCalls int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pollCalls, 1)
		w.Header().Set("Content-Type", "application/json")

		resp := graphDeltaResponse{
			DeltaLink: "/me/drive/root/delta?token=mock",
			Values: []graph.DeltaItem{
				{DriveItem: graph.DriveItem{ID: "newfile", Name: "remote.txt", Size: 200, Parent: &graph.DriveItemParent{ID: "root"}}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	graphClient := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	inodeCache := NewInodeCache()
	contentCache, _ := NewContentCache(t.TempDir())

	parent := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	parent.SetChildren([]string{})
	inodeCache.Insert(parent)

	ds := NewDeltaSync(graphClient, &mockTokenProvider{token: "t"}, inodeCache, contentCache)

	success, newLink, err := ds.pollAndApply(context.Background(), server.URL+"/initial")
	if err != nil {
		t.Fatalf("pollAndApply error: %v", err)
	}
	if !success {
		t.Error("pollAndApply should have success=true")
	}
	if newLink == "" {
		t.Error("newLink should not be empty")
	}

	if inodeCache.Get("newfile") == nil {
		t.Error("newfile should have been created from the delta")
	}
}

// TestDeltaSync_PollAndApply_PartialFailure verifies the streaming behavior
// (issue #68): when page 2 of a delta cycle fails, the items of page 1 are
// already applied and the returned link resumes at page 2; a subsequent poll
// from that link completes the rest without data loss.
func TestDeltaSync_PollAndApply_PartialFailure(t *testing.T) {
	var page2Calls int32

	newItem := func(id, name string, size int64) graph.DeltaItem {
		//#nosec G115 -- test fixture sizes are tiny non-negative constants
		return graph.DeltaItem{DriveItem: graph.DriveItem{ID: id, Name: name, Size: uint64(size), Parent: &graph.DriveItemParent{ID: "root"}}}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/delta/initial":
			json.NewEncoder(w).Encode(graphDeltaResponse{
				NextLink: "http://" + r.Host + "/delta/page2",
				Values: []graph.DeltaItem{
					newItem("f1", "one.txt", 10),
					newItem("f2", "two.txt", 20),
					newItem("f3", "three.txt", 30),
				},
			})
		case "/delta/page2":
			if atomic.AddInt32(&page2Calls, 1) == 1 {
				// First attempt at page 2 fails (transient server error).
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			// Retry: page 2 completes the cycle.
			json.NewEncoder(w).Encode(graphDeltaResponse{
				DeltaLink: "http://" + r.Host + "/delta/final",
				Values: []graph.DeltaItem{
					newItem("f4", "four.txt", 40),
					newItem("f5", "five.txt", 50),
				},
			})
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	graphClient := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	inodeCache := NewInodeCache()
	contentCache, _ := NewContentCache(t.TempDir())

	parent := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	parent.SetChildren([]string{})
	inodeCache.Insert(parent)

	ds := NewDeltaSync(graphClient, &mockTokenProvider{token: "t"}, inodeCache, contentCache)

	// First poll: page 1 applies, page 2 fails.
	success, newLink, err := ds.pollAndApply(context.Background(), server.URL+"/delta/initial")
	if err == nil {
		t.Fatal("expected an error from the failing page 2")
	}
	if success {
		t.Error("expected success=false on partial failure")
	}

	// Items of page 1 must already be applied despite the page-2 failure.
	for _, id := range []string{"f1", "f2", "f3"} {
		if inodeCache.Get(id) == nil {
			t.Errorf("item %s from page 1 should be applied despite page-2 failure", id)
		}
	}
	// Resume point must be the failing page's link (not the cycle start).
	if newLink != server.URL+"/delta/page2" {
		t.Errorf("resume link = %q, want %q", newLink, server.URL+"/delta/page2")
	}

	// Second poll from the resume point completes the rest, idempotent and
	// without data loss.
	success, _, err = ds.pollAndApply(context.Background(), newLink)
	if err != nil {
		t.Fatalf("second poll error: %v", err)
	}
	if !success {
		t.Error("second poll should succeed")
	}
	for _, id := range []string{"f1", "f2", "f3", "f4", "f5"} {
		if inodeCache.Get(id) == nil {
			t.Errorf("item %s missing after the completed cycle", id)
		}
	}
}

// TestDeltaSync_DeltaLoop_ResumesFromPersistedLink verifies end-to-end (with
// BoltDB) that after a page-2 failure the delta loop resumes from the last
// persisted per-page link: page 1 is fetched exactly once and only the failed
// page is re-fetched on the next attempt.
func TestDeltaSync_DeltaLoop_ResumesFromPersistedLink(t *testing.T) {
	var initialCalls, page2Calls int32

	newItem := func(id, name string, size int64) graph.DeltaItem {
		//#nosec G115 -- test fixture sizes are tiny non-negative constants
		return graph.DeltaItem{DriveItem: graph.DriveItem{ID: id, Name: name, Size: uint64(size), Parent: &graph.DriveItemParent{ID: "root"}}}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/delta/initial":
			atomic.AddInt32(&initialCalls, 1)
			json.NewEncoder(w).Encode(graphDeltaResponse{
				NextLink: "http://" + r.Host + "/delta/page2",
				Values:   []graph.DeltaItem{newItem("f1", "one.txt", 10)},
			})
		case "/delta/page2":
			if atomic.AddInt32(&page2Calls, 1) == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(graphDeltaResponse{
				DeltaLink: "http://" + r.Host + "/delta/final",
				Values:    []graph.DeltaItem{newItem("f2", "two.txt", 20)},
			})
		case "/delta/final":
			json.NewEncoder(w).Encode(graphDeltaResponse{DeltaLink: "http://" + r.Host + "/delta/final"})
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	graphClient := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	inodeCache := NewInodeCache()
	if err := inodeCache.InitBoltDB(filepath.Join(t.TempDir(), "inodes.db")); err != nil {
		t.Fatalf("InitBoltDB: %v", err)
	}
	contentCache, _ := NewContentCache(t.TempDir())

	parent := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	parent.SetChildren([]string{})
	inodeCache.Insert(parent)
	inodeCache.SetDeltaLink(server.URL + "/delta/initial")

	ds := NewDeltaSync(graphClient, &mockTokenProvider{token: "t"}, inodeCache, contentCache)
	ctx, cancel := context.WithCancel(context.Background())
	ds.Start(ctx, 50*time.Millisecond)
	// The offline retry after a failed poll waits a fixed 2s, so the test
	// must stay alive long enough for the resumed retry to happen.
	time.Sleep(2600 * time.Millisecond)
	cancel()
	ds.Stop()

	// Page 1 must have been fetched exactly once: after the page-2 failure
	// the loop resumes from the persisted per-page link, not from the start.
	if got := atomic.LoadInt32(&initialCalls); got != 1 {
		t.Errorf("initial page fetched %d times, want exactly 1 (resume from persisted link)", got)
	}
	if got := atomic.LoadInt32(&page2Calls); got != 2 {
		t.Errorf("page 2 fetched %d times, want 2 (once failing, once resumed)", got)
	}
	if inodeCache.Get("f1") == nil || inodeCache.Get("f2") == nil {
		t.Error("both delta items should be applied after the resume")
	}
	if got := inodeCache.GetDeltaLink(); got != server.URL+"/delta/final" {
		t.Errorf("persisted delta link = %q, want final link", got)
	}
}

// TestDeltaSync_PollAndApply_BoundedMemory verifies the streaming apply keeps
// memory bounded (issue #68): a delta cycle of 100k items must not hold the
// full batch in heap. The parent "ghost" is intentionally NOT in the cache so
// applyDelta early-returns, isolating the delta-accumulation memory (the
// target of the issue) from tree growth. Acceptance criterion: peak heap
// growth during the sync < 50 MiB.
func TestDeltaSync_PollAndApply_BoundedMemory(t *testing.T) {
	const (
		itemsPerPage = 10000
		pages        = 10
		totalItems   = itemsPerPage * pages // 100k
	)

	name := strings.Repeat("n", 600)
	all := make([]graph.DeltaItem, totalItems)
	for i := range all {
		all[i] = graph.DeltaItem{DriveItem: graph.DriveItem{
			ID:     fmt.Sprintf("id-%06d", i),
			Name:   name,
			Size:   uint64(i), //#nosec G115 -- index fits in uint64 for any real fixture
			Parent: &graph.DriveItemParent{ID: "ghost"},
		}}
	}

	// Pre-marshal every page BEFORE the baseline measurement so the server
	// side does not allocate inside the measured window.
	pagesJSON := make([][]byte, pages)
	for p := 0; p < pages; p++ {
		resp := graphDeltaResponse{Values: all[p*itemsPerPage : (p+1)*itemsPerPage]}
		if p == pages-1 {
			resp.DeltaLink = "/delta/final"
		} else {
			resp.NextLink = "/delta/page"
		}
		b, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal page %d: %v", p, err)
		}
		pagesJSON[p] = b
	}

	var pageIndex int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := atomic.AddInt32(&pageIndex, 1) - 1
		if p >= pages {
			t.Errorf("unexpected extra poll: %d", p)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write(pagesJSON[p])
	}))
	defer server.Close()

	graphClient := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	inodeCache := NewInodeCache()
	contentCache, _ := NewContentCache(t.TempDir())
	ds := NewDeltaSync(graphClient, &mockTokenProvider{token: "t"}, inodeCache, contentCache)

	// Silence the trace-level "skipping delta" logs: with 100k items they
	// would dominate the heap measurement. Restored after the test.
	origLogger := log.Logger
	log.Logger = zerolog.Nop()
	defer func() { log.Logger = origLogger }()

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	stop := make(chan struct{})
	var peak atomic.Uint64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var maxHeap uint64
		for {
			select {
			case <-stop:
				peak.Store(maxHeap)
				return
			default:
			}
			// Force a GC before sampling so the peak reflects LIVE objects,
			// not transient page allocations waiting for the GC threshold
			// (which is ~2x the baseline heap and would never fire mid-sync).
			runtime.GC()
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > maxHeap {
				maxHeap = ms.HeapAlloc
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	success, _, err := ds.pollAndApply(context.Background(), server.URL+"/delta/initial")
	close(stop)
	wg.Wait()

	if err != nil {
		t.Fatalf("pollAndApply: %v", err)
	}
	if !success {
		t.Error("expected success")
	}

	growth := peak.Load() - before.HeapAlloc
	if growth > 50*1024*1024 {
		t.Errorf("peak heap growth %d bytes exceeds 50 MiB: delta accumulation is not bounded", growth)
	}
	t.Logf("peak heap growth during 100k-item sync: %d bytes (%.1f MiB)", growth, float64(growth)/(1<<20))
}

// graphDeltaResponse es un helper para el mock HTTP.
type graphDeltaResponse struct {
	NextLink  string            `json:"@odata.nextLink,omitempty"`
	DeltaLink string            `json:"@odata.deltaLink,omitempty"`
	Values    []graph.DeltaItem `json:"value,omitempty"`
}

// ──── DeltaSync Start/Stop ────

func TestDeltaSync_StartStop(t *testing.T) {
	var pollCalls int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pollCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(graphDeltaResponse{
			DeltaLink: "/me/drive/root/delta?token=test",
			Values:    []graph.DeltaItem{},
		})
	}))
	defer server.Close()

	graphClient := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	inodeCache := NewInodeCache()
	contentCache, _ := NewContentCache(t.TempDir())

	// Delta link inicial apunta al mock
	inodeCache.SetDeltaLink(server.URL + "/me/drive/root/delta?token=initial")

	ds := NewDeltaSync(graphClient, &mockTokenProvider{token: "t"}, inodeCache, contentCache)

	ctx, cancel := context.WithCancel(context.Background())
	ds.Start(ctx, 50*time.Millisecond)

	time.Sleep(200 * time.Millisecond)

	cancel()
	ds.Stop()

	calls := atomic.LoadInt32(&pollCalls)
	if calls < 1 {
		t.Errorf("Expected at least 1 delta poll, got %d", calls)
	}
	t.Logf("Poll delta calls: %d", calls)
}

// ──── PollDelta of the Graph client ────

func TestGraph_PollDelta_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(graphDeltaResponse{
			DeltaLink: "/me/drive/root/delta?token=empty",
			Values:    []graph.DeltaItem{},
		})
	}))
	defer server.Close()

	client := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}

	items, nextLink, cont, err := client.PollDelta(context.Background(), &mockTokenProvider{token: "t"}, "")
	if err != nil {
		t.Fatalf("PollDelta error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("Expected 0 items, got %d", len(items))
	}
	if cont {
		t.Error("cont should be false (no more pages)")
	}
	if !strings.Contains(nextLink, "token=empty") {
		t.Errorf("nextLink should contain the token: %s", nextLink)
	}
}

func TestGraph_PollDelta_WithItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(graphDeltaResponse{
			DeltaLink: "/me/drive/root/delta?token=items",
			Values: []graph.DeltaItem{
				{DriveItem: graph.DriveItem{ID: "a", Name: "a.txt", Size: 10}},
				{DriveItem: graph.DriveItem{ID: "b", Name: "b.txt", Size: 20}},
			},
		})
	}))
	defer server.Close()

	client := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}

	items, _, cont, err := client.PollDelta(context.Background(), &mockTokenProvider{token: "t"}, "")
	if err != nil {
		t.Fatalf("PollDelta error: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("Expected 2 items, got %d", len(items))
	}
	if cont {
		t.Error("cont should be false")
	}
}

func TestGraph_PollDelta_Pagination(t *testing.T) {
	var pageNum int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := atomic.AddInt32(&pageNum, 1)
		w.Header().Set("Content-Type", "application/json")

		if page == 1 {
			json.NewEncoder(w).Encode(graphDeltaResponse{
				NextLink: "/me/drive/root/delta?$skiptoken=page2",
				Values: []graph.DeltaItem{
					{DriveItem: graph.DriveItem{ID: "p1", Name: "page1.txt"}},
				},
			})
		} else {
			json.NewEncoder(w).Encode(graphDeltaResponse{
				DeltaLink: "/me/drive/root/delta?token=final",
				Values: []graph.DeltaItem{
					{DriveItem: graph.DriveItem{ID: "p2", Name: "page2.txt"}},
				},
			})
		}
	}))
	defer server.Close()

	client := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}

	// Page 1
	items, nextLink, cont, err := client.PollDelta(context.Background(), &mockTokenProvider{token: "t"}, "")
	if err != nil {
		t.Fatalf("PollDelta page 1 error: %v", err)
	}
	if len(items) != 1 || items[0].ID != "p1" {
		t.Error("Page 1: wrong items")
	}
	if !cont {
		t.Error("Should continue paginating")
	}

	// Page 2
	items, _, cont, err = client.PollDelta(context.Background(), &mockTokenProvider{token: "t"}, nextLink)
	if err != nil {
		t.Fatalf("PollDelta page 2 error: %v", err)
	}
	if len(items) != 1 || items[0].ID != "p2" {
		t.Error("Page 2: wrong items")
	}
	if cont {
		t.Error("Should not continue paginating")
	}
}

func TestGraph_PollDelta_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}

	_, _, _, err := client.PollDelta(context.Background(), &mockTokenProvider{token: "t"}, "")
	if err == nil {
		t.Error("Expected error for HTTP 500")
	}
}
