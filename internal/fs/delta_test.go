package fs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
)

// mockURL es la URL base usada en los handlers mock que no pueden
// reference `server` because it is not declared yet in the closure.
const mockURL = "http://127.0.0.1:1"

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
		t.Error("Se esperaba error 'directory is non-empty'")
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
		t.Errorf("Nombre esperado 'nuevo.txt', obtenido %q", updated.Name())
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

// ──── applyDelta: contenido modificado ────

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
		t.Errorf("Size esperado 100, obtenido %d", updated.DriveItem.Size)
	}
	if updated.DriveItem.ETag != "new-etag" {
		t.Errorf("ETag esperado 'new-etag', obtenido %q", updated.DriveItem.ETag)
	}

	if contentCache.HasContent("file1") {
		t.Error("ContentCache should have been cleared after a remote change")
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
		t.Errorf("Se esperaba al menos 1 poll delta, hubo %d", calls)
	}
	t.Logf("Poll delta calls: %d", calls)
}

// ──── PollDelta del cliente Graph ────

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
		t.Errorf("Se esperaban 0 items, obtenidos %d", len(items))
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
		t.Errorf("Se esperaban 2 items, obtenidos %d", len(items))
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
		t.Error("Se esperaba error para HTTP 500")
	}
}
