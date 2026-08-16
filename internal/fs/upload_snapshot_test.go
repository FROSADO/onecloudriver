package fs

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
)

// TestContentCache_Snapshot_ContentEqualsAndIndependent verifies that Snapshot
// returns a byte-identical copy that is independent of later edits to the live
// cache file (the stability guarantee required by the streaming upload path).
func TestContentCache_Snapshot_ContentEqualsAndIndependent(t *testing.T) {
	cache, err := NewContentCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cache.CloseAll()

	const id = "snap-equals"
	if err := cache.Insert(id, []byte("AAAA")); err != nil {
		t.Fatal(err)
	}

	path, size, err := cache.Snapshot(id)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	defer os.Remove(path)

	if size != 4 {
		t.Fatalf("size = %d, want 4", size)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "AAAA" {
		t.Fatalf("snapshot content = %q (err=%v), want %q", got, err, "AAAA")
	}

	// Modify the live cache file after the snapshot was taken.
	if err := cache.Insert(id, []byte("BBBBBBBB")); err != nil {
		t.Fatal(err)
	}

	// The snapshot must be unaffected.
	if got, err := os.ReadFile(path); err != nil || string(got) != "AAAA" {
		t.Fatalf("snapshot changed after edit: %q (err=%v), want %q", got, err, "AAAA")
	}
}

// TestContentCache_Snapshot_MissingAndEmpty covers the two edge cases: a
// missing file returns os.ErrNotExist, and an empty file yields (0, nil).
func TestContentCache_Snapshot_MissingAndEmpty(t *testing.T) {
	cache, err := NewContentCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cache.CloseAll()

	if _, _, err := cache.Snapshot("does-not-exist"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: err = %v, want os.ErrNotExist", err)
	}

	if err := cache.Insert("empty", []byte{}); err != nil {
		t.Fatal(err)
	}
	path, size, err := cache.Snapshot("empty")
	if err != nil {
		t.Fatalf("empty file Snapshot: %v", err)
	}
	if path != "" || size != 0 {
		t.Fatalf("empty file: path=%q size=%d, want empty path and 0", path, size)
	}
}

// TestContentCache_Snapshot_BlocksUntilConcurrentWriteCompletes verifies that
// Snapshot takes the copy under the per-inode lock (issue #64): a snapshot
// taken while InsertStream is mid-write blocks and returns the complete new
// content, never a torn read.
func TestContentCache_Snapshot_BlocksUntilConcurrentWriteCompletes(t *testing.T) {
	cache, err := NewContentCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cache.CloseAll()

	const id = "snap-race"
	if err := cache.Insert(id, []byte("AAAA")); err != nil {
		t.Fatal(err)
	}

	br := &blockingReader{
		data:    []byte("BBBB"),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}

	insertDone := make(chan error, 1)
	go func() {
		_, err := cache.InsertStream(id, br)
		insertDone <- err
	}()
	<-br.started // InsertStream is blocked mid-write, holding the lock

	type result struct {
		path string
		size int64
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		p, n, err := cache.Snapshot(id)
		resultCh <- result{p, n, err}
	}()

	select {
	case r := <-resultCh:
		close(br.release)
		t.Fatalf("Snapshot returned early (path=%q size=%d err=%v); expected it to block", r.path, r.size, r.err)
	case <-time.After(250 * time.Millisecond):
		// Expected: still blocked on the per-inode lock.
	}

	close(br.release)
	if err := <-insertDone; err != nil {
		t.Fatalf("InsertStream: %v", err)
	}

	r := <-resultCh
	if r.err != nil {
		t.Fatalf("Snapshot: %v", r.err)
	}
	defer os.Remove(r.path)
	got, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "BBBB" {
		t.Fatalf("snapshot = %q, want %q", got, "BBBB")
	}
}

// TestUploadSession_Snapshot_Variants covers constructor validation and the
// JSON round-trip of the streaming variant (metadata persisted, no content).
func TestUploadSession_Snapshot_Variants(t *testing.T) {
	if _, err := NewUploadSessionSnapshot("id", "p", "n", "", 10); err == nil {
		t.Error("empty snapshot path should fail")
	}
	if _, err := NewUploadSessionSnapshot("id", "p", "n", "/tmp/x", 0); err == nil {
		t.Error("non-positive size should fail")
	}

	s, err := NewUploadSessionSnapshot("id", "p", "n", "/tmp/snap-123", 42)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.AsJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hello") {
		t.Fatal("unexpected content in JSON (should be metadata only)")
	}

	restored, err := NewUploadSessionJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if restored.SnapshotPath != "/tmp/snap-123" || restored.Size != 42 {
		t.Fatalf("round-trip mismatch: path=%q size=%d", restored.SnapshotPath, restored.Size)
	}
	if len(restored.Data) != 0 {
		t.Fatalf("restored session should have no in-memory Data, got %d bytes", len(restored.Data))
	}
}

// TestUploadSession_DiscardSnapshot verifies the snapshot file is removed and
// the reference cleared (idempotent).
func TestUploadSession_DiscardSnapshot(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "snap-*")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()

	s, err := NewUploadSessionSnapshot("id", "p", "n", path, 1)
	if err != nil {
		t.Fatal(err)
	}

	s.DiscardSnapshot()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("snapshot still exists after DiscardSnapshot (err=%v)", err)
	}

	// Idempotent: second call must not panic.
	s.DiscardSnapshot()
}

// TestUploadManager_StreamingSnapshotUpload runs an end-to-end upload that
// streams from an on-disk snapshot and asserts the uploaded bytes match the
// snapshot, the inode moves to the remote ID, and finishSession removes the
// snapshot file.
func TestUploadManager_StreamingSnapshotUpload(t *testing.T) {
	cache, err := NewContentCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cache.CloseAll()

	const content = "hola streaming snapshot"
	if err := cache.Insert("local-1", []byte(content)); err != nil {
		t.Fatal(err)
	}

	var uploaded []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading body: %v", err)
		}
		uploaded = b
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"remote-1","name":"stream.txt","size":22,"eTag":"\"etag-new\""}`))
	}))
	defer server.Close()

	parent := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	child := NewInodeDriveItem(&graph.DriveItem{ID: "local-1", Name: "stream.txt", Parent: &graph.DriveItemParent{ID: "root"}})
	ic := NewInodeCache()
	ic.Insert(parent)
	ic.Insert(child)
	parent.SetChildren([]string{"local-1"})

	um := NewUploadManager(
		&graph.Client{BaseURL: server.URL, HTTPClient: server.Client()},
		&mockTokenProvider{token: "t"},
		ic,
		cache,
		0, 0,
	)

	// Same snapshot the QueueUpload path takes.
	path, size, err := cache.Snapshot("local-1")
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewUploadSessionSnapshot("local-1", "root", "stream.txt", path, size)
	if err != nil {
		t.Fatal(err)
	}

	um.executeUpload(session)

	if string(uploaded) != content {
		t.Fatalf("uploaded %q, want %q", uploaded, content)
	}
	if session.getState() != uploadComplete {
		t.Fatalf("session state = %v, want uploadComplete", session.getState())
	}
	if ic.Get("remote-1") == nil {
		t.Fatal("expected inode to move to the remote ID")
	}

	// finishSession (the loop's cleanup path) must remove the snapshot.
	um.mu.Lock()
	um.sessions["local-1"] = session
	um.mu.Unlock()
	um.finishSession("local-1")

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("snapshot still exists after finishSession (err=%v)", err)
	}
}

// TestUploadManager_ConflictResolution_StreamingSnapshot verifies the 412
// conflict path re-seeks the snapshot and re-streams it for the "local wins"
// re-upload.
func TestUploadManager_ConflictResolution_StreamingSnapshot(t *testing.T) {
	cache, err := NewContentCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cache.CloseAll()

	const content = "conflict streaming content"
	if err := cache.Insert("file1", []byte(content)); err != nil {
		t.Fatal(err)
	}

	var (
		firstIfMatch string
		secondBody   string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/items/file1/content"):
			firstIfMatch = r.Header.Get("If-Match")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPreconditionFailed)
			w.Write([]byte(`{"error":{"code":"notAllowed","message":"Precondition failed"}}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/me/drive/items/file1":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"id":"file1","name":"doc.txt_conflict","eTag":"\"new-remote\""}`))
		case r.Method == http.MethodPut:
			b, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("reading body: %v", err)
			}
			secondBody = string(b)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id":"file2","name":"doc.txt","size":24,"eTag":"\"new-local\""}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	parent := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	child := NewInodeDriveItem(&graph.DriveItem{
		ID:     "file1",
		Name:   "doc.txt",
		ETag:   `"old-etag"`,
		Parent: &graph.DriveItemParent{ID: "root"},
	})
	ic := NewInodeCache()
	ic.Insert(parent)
	ic.Insert(child)
	parent.SetChildren([]string{"file1"})

	um := NewUploadManager(
		&graph.Client{BaseURL: server.URL, HTTPClient: server.Client()},
		&mockTokenProvider{token: "t"},
		ic,
		cache,
		0, 0,
	)

	path, size, err := cache.Snapshot("file1")
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewUploadSessionSnapshot("file1", "root", "doc.txt", path, size)
	if err != nil {
		t.Fatal(err)
	}

	um.executeUpload(session)

	if firstIfMatch != `"old-etag"` {
		t.Errorf("first upload If-Match = %q, want %q", firstIfMatch, `"old-etag"`)
	}
	if secondBody != content {
		t.Errorf("re-uploaded content = %q, want %q", secondBody, content)
	}
	if session.getState() != uploadComplete {
		t.Errorf("session state = %v, want uploadComplete", session.getState())
	}
}

// TestContentCache_Snapshot_PathAndPermissions verifies the snapshot is
// created inside the cache directory (user space, never a shared location like
// /tmp) with owner-only permissions (0600 file, 0700 dir), so other local
// users cannot read or tamper with the content mid-upload.
func TestContentCache_Snapshot_PathAndPermissions(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewContentCache(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.CloseAll()

	if err := cache.Insert("perm", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	path, _, err := cache.Snapshot("perm")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	// The snapshot must be a direct child of the cache's snapshots/ subdir.
	// If os.CreateTemp had been called with an empty dir (the bug), it would
	// fall back to os.TempDir() (/tmp) instead of the user's cache directory.
	wantParent := filepath.Join(dir, "snapshots")
	if got := filepath.Dir(path); got != wantParent {
		t.Fatalf("snapshot parent = %q, want %q (must live in the cache, not a shared temp dir)", got, wantParent)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("snapshot file perms = %04o, want 0600", got)
	}

	snapDirInfo, err := os.Stat(filepath.Join(dir, "snapshots"))
	if err != nil {
		t.Fatal(err)
	}
	if got := snapDirInfo.Mode().Perm() & 0077; got != 0 {
		t.Errorf("snapshots dir perms = %04o, want owner-only (no group/other bits)", snapDirInfo.Mode().Perm())
	}
}

// TestContentCache_Snapshot_NoFullFileAllocation asserts that Snapshot does
// not allocate a buffer the size of the whole file (unlike ReadAll). The
// allocation comes from io.Copy's fixed-size buffer, not a full-file []byte.
func TestContentCache_Snapshot_NoFullFileAllocation(t *testing.T) {
	cache, err := NewContentCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cache.CloseAll()

	const size = 4 * 1024 * 1024
	if err := cache.Insert("alloc", bytes.Repeat([]byte("x"), size)); err != nil {
		t.Fatal(err)
	}

	allocs := testing.AllocsPerRun(3, func() {
		path, n, err := cache.Snapshot("alloc")
		if err != nil {
			t.Fatal(err)
		}
		if n != size {
			t.Fatalf("size = %d, want %d", n, size)
		}
		os.Remove(path)
	})

	// A full-file []byte copy would allocate at least one 4 MB buffer per run.
	// io.Copy uses a bounded buffer, so allocations must be orders of
	// magnitude smaller. Use a generous bound to stay robust across Go
	// versions while still catching a full-file allocation regression.
	if allocs > 64 {
		t.Fatalf("Snapshot made %v allocs/op, expected a bounded number (no full-file []byte)", allocs)
	}
}
