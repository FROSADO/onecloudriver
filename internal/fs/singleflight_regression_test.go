package fs

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
)

// TestDriveItemNode_ReaddirPlus_ColdFolderSingleFetch is the end-to-end
// regression test for issue #153. It reproduces the readdirplus / `ls -la`
// workload: one Readdir of a cold folder plus one Lookup per entry, all
// concurrent, against a paginated Graph endpoint. The whole burst must share a
// single listing (one page 1 + one page 2 = 2 HTTP requests), and every Lookup
// must resolve from that shared fetch.
//
// Before the singleflight the N+1 callers each paginated the folder on their
// own, so the request count was 2*(N+1). This test fails on that code.
func TestDriveItemNode_ReaddirPlus_ColdFolderSingleFetch(t *testing.T) {
	const numFiles = 10

	var calls atomic.Int64
	firstPage := make(chan struct{})
	releaseFirstPage := make(chan struct{})
	var firstPageOnce sync.Once

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Query().Get("$skiptoken") == "" {
			// Page 1: keep the shared fetch in flight while every concurrent
			// caller is launched, then hand the second page via nextLink.
			firstPageOnce.Do(func() { close(firstPage) })
			<-releaseFirstPage
			fmt.Fprintf(w, `{"value":%s,"@odata.nextLink":%q}`,
				childrenJSON(0, numFiles/2),
				server.URL+"/me/drive/items/folder123/children?$skiptoken=p2")
			return
		}

		// Page 2: end of the listing.
		fmt.Fprintf(w, `{"value":%s}`, childrenJSON(numFiles/2, numFiles))
	}))
	defer server.Close()

	graphClient := &graph.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}
	inodeCache := NewInodeCache()
	deps := nodeDeps{
		graphClient:   graphClient,
		tokenProvider: &mockTokenProvider{token: "test_token"},
		inodeCache:    inodeCache,
	}
	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{
			ID: "folder123", Name: "Documentos", Folder: &graph.Folder{ChildCount: numFiles},
		}),
		nodeDeps: deps,
	}

	var readdirEntries int
	var readdirErrno syscall.Errno
	lookupErrnos := make([]syscall.Errno, numFiles)
	lookupInodes := make([]*Inode, numFiles)

	var wg sync.WaitGroup
	wg.Add(1 + numFiles)

	// The Readdir half of readdirplus.
	go func() {
		defer wg.Done()
		stream, errno := node.Readdir(context.Background())
		readdirErrno = errno
		if errno != 0 {
			return
		}
		for stream.HasNext() {
			if _, errno := stream.Next(); errno != 0 {
				return
			}
			readdirEntries++
		}
	}()

	// The per-entry Lookup half of readdirplus. lookupChild is the exact
	// function behind DriveItemNode.Lookup (fuseLookup) before NewInode, so
	// this exercises the production path without a mounted FUSE bridge.
	for i := 0; i < numFiles; i++ {
		go func(idx int) {
			defer wg.Done()
			res := lookupChild(context.Background(), "folder123",
				"file"+strconv.Itoa(idx)+".txt", inodeCache, deps.fetchChildrenWithOffline)
			lookupErrnos[idx] = res.errno
			lookupInodes[idx] = res.childInode
		}(i)
	}

	<-firstPage // the shared fetch is now in flight
	time.Sleep(100 * time.Millisecond)
	close(releaseFirstPage)
	wg.Wait()

	if got := calls.Load(); got != 2 {
		t.Fatalf("expected one paginated listing (2 HTTP requests) for %d concurrent readers, got %d", numFiles+1, got)
	}
	if readdirErrno != 0 {
		t.Fatalf("Readdir errno = %d, want 0", readdirErrno)
	}
	if readdirEntries != numFiles {
		t.Fatalf("Readdir returned %d entries, want %d", readdirEntries, numFiles)
	}
	for i, errno := range lookupErrnos {
		if errno != 0 {
			t.Fatalf("Lookup %d errno = %d, want 0", i, errno)
		}
		if lookupInodes[i] == nil {
			t.Fatalf("Lookup %d did not resolve its entry", i)
		}
		if got, want := lookupInodes[i].Name(), "file"+strconv.Itoa(i)+".txt"; got != want {
			t.Fatalf("Lookup %d resolved %q, want %q", i, got, want)
		}
	}
}

// childrenJSON builds a Graph "value" array with items [from, to).
func childrenJSON(from, to int) string {
	out := "["
	for i := from; i < to; i++ {
		if i > from {
			out += ","
		}
		out += fmt.Sprintf(`{"id":"file%d","name":"file%d.txt","size":%d}`, i, i, i+1)
	}
	return out + "]"
}
