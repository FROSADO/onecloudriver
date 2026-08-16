package fs

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
)

// ──── UploadSession ────

func TestUploadSession_NewUploadSession(t *testing.T) {
	data := []byte("hello world")
	s, err := NewUploadSession("local-abc", "root", "test.txt", data)
	if err != nil {
		t.Fatalf("NewUploadSession error: %v", err)
	}
	if s.ID != "local-abc" {
		t.Errorf("Expected ID 'local-abc', got %q", s.ID)
	}
	if s.ParentID != "root" {
		t.Errorf("Expected ParentID 'root', got %q", s.ParentID)
	}
	if s.Name != "test.txt" {
		t.Errorf("Expected name 'test.txt', got %q", s.Name)
	}
	if string(s.Data) != "hello world" {
		t.Errorf("Expected data 'hello world', got %q", string(s.Data))
	}
	if s.getState() != uploadPending {
		t.Errorf("Expected initial state uploadPending, got %v", s.getState())
	}
}

func TestUploadSession_NewUploadSession_EmptyData(t *testing.T) {
	_, err := NewUploadSession("id", "parent", "name", nil)
	if err == nil {
		t.Fatal("NewUploadSession with nil data should return an error")
	}
	_, err = NewUploadSession("id", "parent", "name", []byte{})
	if err == nil {
		t.Fatal("NewUploadSession with empty data should return an error")
	}
}

func TestUploadSession_StateTransitions(t *testing.T) {
	s, _ := NewUploadSession("id", "parent", "name", []byte("data"))

	if s.getState() != uploadPending {
		t.Error("Initial state should be uploadPending")
	}
	s.setState(uploadUploading, nil)
	if s.getState() != uploadUploading {
		t.Error("State should be uploadUploading")
	}

	s.setState(uploadComplete, nil)
	if s.getState() != uploadComplete {
		t.Error("State should be uploadComplete")
	}

	s2, _ := NewUploadSession("id2", "p", "n", []byte("x"))
	s2.setState(uploadUploading, nil)
	s2.setState(uploadErrored, io.ErrUnexpectedEOF)
	if s2.getState() != uploadErrored {
		t.Error("State should be uploadErrored")
	}
	if s2.LastErr != "unexpected EOF" {
		t.Errorf("Expected LastErr 'unexpected EOF', got %q", s2.LastErr)
	}
}

func TestUploadSession_JSONRoundTrip(t *testing.T) {
	s1, _ := NewUploadSession("local-xyz", "folder123", "document.pdf", []byte("binary content here"))
	s1.setState(uploadPending, nil)
	s1.Retries = 2

	data, err := s1.AsJSON()
	if err != nil {
		t.Fatalf("AsJSON error: %v", err)
	}

	s2, err := NewUploadSessionJSON(data)
	if err != nil {
		t.Fatalf("NewUploadSessionJSON error: %v", err)
	}

	if s2.ID != s1.ID {
		t.Errorf("ID: expected %q, got %q", s1.ID, s2.ID)
	}
	if s2.ParentID != s1.ParentID {
		t.Errorf("ParentID: expected %q, got %q", s1.ParentID, s2.ParentID)
	}
	if s2.Name != s1.Name {
		t.Errorf("Name: expected %q, got %q", s1.Name, s2.Name)
	}
	if string(s2.Data) != string(s1.Data) {
		t.Errorf("Data: expected %q, got %q", string(s1.Data), string(s2.Data))
	}
	if s2.Retries != 2 {
		t.Errorf("Retries: expected 2, got %d", s2.Retries)
	}
}

// ──── byteReader ────

func TestByteReader_ReadAll(t *testing.T) {
	r := &byteReader{data: []byte("hello")}
	result, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if string(result) != "hello" {
		t.Errorf("Expected 'hello', got %q", string(result))
	}
}

func TestByteReader_ReadPartial(t *testing.T) {
	r := &byteReader{data: []byte("hello world")}
	buf := make([]byte, 5)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if n != 5 {
		t.Errorf("Read: expected 5, got %d", n)
	}
	if string(buf) != "hello" {
		t.Errorf("Expected 'hello', got %q", string(buf))
	}

	rest, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if string(rest) != " world" {
		t.Errorf("Expected ' world', got %q", string(rest))
	}
}

func TestByteReader_ReadEmpty(t *testing.T) {
	r := &byteReader{data: []byte{}}
	buf := make([]byte, 10)
	n, err := r.Read(buf)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Read on empty data should return io.EOF, got: %v", err)
	}
	if n != 0 {
		t.Errorf("Read on empty data: expected 0, got %d", n)
	}
}

// ──── ContentCache.ReadAll ────

func TestContentCache_ReadAll_Success(t *testing.T) {
	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)
	defer cc.CloseAll()

	cc.Insert("test-readall", []byte("hello readall"))

	data := cc.ReadAll("test-readall")
	if string(data) != "hello readall" {
		t.Errorf("ReadAll: expected 'hello readall', got %q", string(data))
	}
}

func TestContentCache_ReadAll_NonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)
	defer cc.CloseAll()

	data := cc.ReadAll("does-not-exist")
	if len(data) != 0 {
		t.Errorf("ReadAll of a non-existent file should return an empty slice, got %q", string(data))
	}
}

// ──── UploadManager: QueueUpload + CancelUpload (unit tests) ────

func TestUploadManager_QueueUpload_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)
	defer cc.CloseAll()

	gc := graph.NewClient()
	ic := NewInodeCache()
	um := NewUploadManager(gc, &mockTokenProvider{token: "t"}, ic, cc, 0, 0)

	// Empty file (not inserted in ContentCache) — QueueUpload should not enqueue it
	um.QueueUpload("empty", "root", "empty.txt")

	um.mu.Lock()
	count := len(um.sessions)
	um.mu.Unlock()
	if count != 0 {
		t.Errorf("There should be no sessions for an empty file, got %d", count)
	}
}

func TestUploadManager_CancelUpload_RemovesSession(t *testing.T) {
	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)
	defer cc.CloseAll()
	cc.Insert("will-delete", []byte("data"))

	gc := graph.NewClient()
	ic := NewInodeCache()
	um := NewUploadManager(gc, &mockTokenProvider{token: "t"}, ic, cc, 0, 0)
	um.Start()
	defer um.Stop()

	um.QueueUpload("will-delete", "root", "will-delete.txt")

	// Wait for uploadLoop to process the queued session before cancelling
	time.Sleep(100 * time.Millisecond)

	um.CancelUpload("will-delete")

	// Wait for the cancellation to be processed
	time.Sleep(100 * time.Millisecond)

	um.mu.Lock()
	_, exists := um.sessions["will-delete"]
	count := len(um.sessions)
	um.mu.Unlock()

	if exists {
		t.Error("The cancelled session should not exist")
	}
	if count != 0 {
		t.Errorf("Expected 0 sessions, got %d", count)
	}
}

// ──── UploadManager: Persistence (BoltDB round-trip) ────

func TestUploadManager_PersistenceRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)
	defer cc.CloseAll()

	cc.Insert("persist-id", []byte("persistent content"))

	dbPath := tmpDir + "/test.db"
	ic := NewInodeCache()
	if err := ic.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}
	defer ic.Close()

	gc := graph.NewClient()
	um := NewUploadManager(gc, &mockTokenProvider{token: "t"}, ic, cc, 0, 0)
	um.Start()
	defer um.Stop()

	um.QueueUpload("persist-id", "root", "persist.txt")

	// Wait for it to be persisted (the uploadLoop saves it in the queue case)
	time.Sleep(200 * time.Millisecond)

	raw := ic.LoadUploadSessions()
	if len(raw) != 1 {
		t.Fatalf("Expected 1 session in BoltDB, got %d", len(raw))
	}

	var session UploadSession
	if data, ok := raw["persist-id"]; ok {
		if err := json.Unmarshal(data, &session); err != nil {
			t.Fatalf("Error deserializing: %v", err)
		}
		if session.Name != "persist.txt" {
			t.Errorf("Expected Name 'persist.txt', got %q", session.Name)
		}
	} else {
		t.Fatal("'persist-id' was not found in BoltDB")
	}

	// Clean up the BoltDB session
	ic.DeleteUploadSession("persist-id")
	raw = ic.LoadUploadSessions()
	if len(raw) != 0 {
		t.Errorf("Expected 0 sessions after DeleteUploadSession, got %d", len(raw))
	}
}

// ──── UploadManager: in-flight slot release (issue #87) ────

// waitForErrored polls until the session reaches the errored state.
func waitForErrored(t *testing.T, s *UploadSession) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.getState() == uploadErrored {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session %q did not reach errored state (now %v)", s.ID, s.getState())
}

// waitForGone polls until the session is removed from the manager.
func waitForGone(t *testing.T, um *UploadManager, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		um.mu.Lock()
		_, exists := um.sessions[id]
		um.mu.Unlock()
		if !exists {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session %q was not removed from the manager", id)
}

// A failed upload must release its in-flight slot. Before the fix, every
// failed upload kept its slot consumed forever (inFlight was only decremented
// in finishSession), so with the cap reached the pipeline stalled: retries
// could never re-launch and new uploads were never started — files stayed
// queued forever (issue #87).
func TestUploadManager_FailedUpload_ReleasesInFlightSlot(t *testing.T) {
	var mu sync.Mutex
	var attempts int
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		attempts++
		bodies = append(bodies, string(body))
		mu.Unlock()
		w.WriteHeader(http.StatusBadRequest) // permanent error
	}))
	defer server.Close()

	um := NewUploadManager(
		&graph.Client{BaseURL: server.URL, HTTPClient: server.Client()},
		&mockTokenProvider{token: "t"},
		NewInodeCache(),
		&ContentCache{},
		1, // maxUploadsInFlight: single slot, so B can only run after A releases it
		1, // maxUploadRetries: abandon after 2 failures
	)

	sA, err := NewUploadSession("a", "root", "a.txt", []byte("AAA"))
	if err != nil {
		t.Fatal(err)
	}
	sB, err := NewUploadSession("b", "root", "b.txt", []byte("BBB"))
	if err != nil {
		t.Fatal(err)
	}

	// Sequential and deterministic: only one session in play at a time.
	um.enqueueSession(sA)

	// Tick 1: launch A. It fails with a permanent error.
	um.processSessions()
	waitForErrored(t, sA)
	um.mu.Lock()
	leaked := um.inFlight
	um.mu.Unlock()
	if leaked != 0 {
		t.Fatalf("in-flight slot leaked after failed upload: inFlight=%d (want 0)", leaked)
	}

	// Tick 2: A retries (retries=1 <= cap) and fails again.
	um.processSessions()
	waitForErrored(t, sA)

	// Tick 3: A abandons (retries=2 > cap) and is cleaned up.
	um.processSessions()
	waitForGone(t, um, "a")

	// B must now be launched normally: the slot is free. This is the
	// regression check — with the leak, B would stay pending forever.
	um.enqueueSession(sB)
	um.processSessions()
	waitForErrored(t, sB)
	um.processSessions()
	waitForErrored(t, sB)
	um.processSessions()
	waitForGone(t, um, "b")

	mu.Lock()
	defer mu.Unlock()
	if attempts != 4 {
		t.Errorf("expected exactly 4 upload attempts (2 per session), got %d", attempts)
	}
	var sawA, sawB bool
	for _, b := range bodies {
		if b == "AAA" {
			sawA = true
		}
		if b == "BBB" {
			sawB = true
		}
	}
	if !sawA || !sawB {
		t.Errorf("both sessions must reach the network: sawA=%v sawB=%v", sawA, sawB)
	}
}

func TestUploadManager_QueueUpload_ReturnsBool(t *testing.T) {
	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)
	defer cc.CloseAll()

	um := NewUploadManager(graph.NewClient(), &mockTokenProvider{token: "t"}, NewInodeCache(), cc, 0, 0)

	// Empty file: nothing to upload → false.
	if ok := um.QueueUpload("empty", "root", "empty.txt"); ok {
		t.Error("QueueUpload of an empty file should return false")
	}

	// File with content → true.
	if err := cc.Insert("filled", []byte("content")); err != nil {
		t.Fatal(err)
	}
	if ok := um.QueueUpload("filled", "root", "filled.txt"); !ok {
		t.Error("QueueUpload with content should return true")
	}
}

// ──── UploadManager: enqueueSession (unit test, sin HTTP) ────

func TestUploadManager_EnqueueSession_Dedup(t *testing.T) {
	gc := graph.NewClient()
	ic := NewInodeCache()
	cc := &ContentCache{}
	um := NewUploadManager(gc, &mockTokenProvider{token: "t"}, ic, cc, 0, 0)

	// Wrap session 1
	s1, _ := NewUploadSession("dedup-id", "root", "first.txt", []byte("first"))
	um.enqueueSession(s1)

	um.mu.Lock()
	if _, exists := um.sessions["dedup-id"]; !exists {
		t.Fatal("Session 1 should exist")
	}
	um.mu.Unlock()

	// Wrap session 2 (same ID)
	s2, _ := NewUploadSession("dedup-id", "root", "second.txt", []byte("second"))
	um.enqueueSession(s2)

	um.mu.Lock()
	session := um.sessions["dedup-id"]
	um.mu.Unlock()

	if session == nil {
		t.Fatal("The dedup session should exist")
	}
	if session.Name != "second.txt" {
		t.Errorf("The active session should be the second one: expected 'second.txt', got %q", session.Name)
	}
}

func TestUploadManager_FinishSession(t *testing.T) {
	gc := graph.NewClient()
	ic := NewInodeCache()
	cc := &ContentCache{}
	um := NewUploadManager(gc, &mockTokenProvider{token: "t"}, ic, cc, 0, 0)

	s, _ := NewUploadSession("finish-id", "root", "finish.txt", []byte("data"))
	um.enqueueSession(s)

	um.finishSession("finish-id")

	um.mu.Lock()
	_, exists := um.sessions["finish-id"]
	count := len(um.sessions)
	um.mu.Unlock()

	if exists {
		t.Error("The finished session should not exist")
	}
	if count != 0 {
		t.Errorf("Expected 0 sessions, got %d", count)
	}
}

// ──── UploadManager: RenameSession (issue #83) ────

func TestUploadManager_RenameSession(t *testing.T) {
	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)
	defer cc.CloseAll()

	dbPath := tmpDir + "/test.db"
	ic := NewInodeCache()
	if err := ic.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}
	defer ic.Close()

	um := NewUploadManager(graph.NewClient(), &mockTokenProvider{token: "t"}, ic, cc, 0, 0)

	s, _ := NewUploadSession("local-abc123", "folder1", "viejo.txt", []byte("data"))
	um.enqueueSession(s)

	// Rename and move to another folder
	um.RenameSession("local-abc123", "folder2", "nuevo.txt")

	um.mu.Lock()
	got := um.sessions["local-abc123"]
	um.mu.Unlock()
	if got == nil {
		t.Fatal("session should still exist after RenameSession")
	}
	if got.Name != "nuevo.txt" {
		t.Errorf("expected Name 'nuevo.txt', got %q", got.Name)
	}
	if got.ParentID != "folder2" {
		t.Errorf("expected ParentID 'folder2', got %q", got.ParentID)
	}

	// The new name is persisted to BoltDB (survives a restart)
	raw := ic.LoadUploadSessions()
	if data, ok := raw["local-abc123"]; ok {
		var restored UploadSession
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatalf("Error deserializing: %v", err)
		}
		if restored.Name != "nuevo.txt" || restored.ParentID != "folder2" {
			t.Errorf("persisted session not updated: name=%q parentID=%q", restored.Name, restored.ParentID)
		}
	} else {
		t.Error("session not found in BoltDB after RenameSession")
	}

	// Renaming an unknown ID is a no-op (no new session is created)
	um.RenameSession("local-unknown", "folder1", "x.txt")
	um.mu.Lock()
	_, exists := um.sessions["local-unknown"]
	um.mu.Unlock()
	if exists {
		t.Error("RenameSession must not create new sessions")
	}

	// In-flight sessions are updated too: the running request keeps the old
	// name, but retries and the post-upload fix-up use the new one.
	s.setState(uploadUploading, nil)
	um.RenameSession("local-abc123", "folder1", "vuelo.txt")
	um.mu.Lock()
	inflight := um.sessions["local-abc123"]
	um.mu.Unlock()
	if inflight.Name != "vuelo.txt" {
		t.Errorf("expected in-flight session renamed, got %q", inflight.Name)
	}
	if inflight.ParentID != "folder1" {
		t.Errorf("expected in-flight session moved, got %q", inflight.ParentID)
	}
}

// Renaming a locally-created file while its first upload is in flight: the
// PUT goes out with the old name, but once it completes the rename must be
// applied remotely with the real ID (issue #83 follow-up).
func TestUploadManager_RenameDuringInflightUpload(t *testing.T) {
	var putName, patchedName string
	putStarted := make(chan struct{})
	releasePut := make(chan struct{})
	done := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			putName = r.URL.Path
			close(putStarted)
			<-releasePut // hold the PUT until the test renames the session
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "real123", "name": "viejo.txt"}`))
		case http.MethodPatch:
			var body struct {
				Name string `json:"name"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			patchedName = body.Name
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "real123", "name": "nuevo.txt"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	inodeCache := NewInodeCache()
	parent := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	child := NewInodeLocal("viejo.txt", 0, parent)
	inodeCache.Insert(parent)
	inodeCache.Insert(child)
	parent.SetChildren([]string{child.ID()})

	cc, _ := NewContentCache(t.TempDir())
	defer cc.CloseAll()

	um := NewUploadManager(
		&graph.Client{BaseURL: server.URL, HTTPClient: server.Client()},
		&mockTokenProvider{token: "t"},
		inodeCache, cc, 0, 0,
	)

	session, _ := NewUploadSession(child.ID(), "root", "viejo.txt", []byte("hola"))
	um.enqueueSession(session)

	go func() {
		um.executeUpload(session)
		close(done)
	}()

	<-putStarted
	// The upload is in flight with the old name; rename now, updating the
	// session and the inode cache exactly like doRename does.
	um.RenameSession(child.ID(), "root", "nuevo.txt")
	child.Lock()
	child.DriveItem.Name = "nuevo.txt"
	child.Unlock()
	close(releasePut)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("executeUpload did not finish")
	}

	if !strings.Contains(putName, "viejo.txt") {
		t.Errorf("expected PUT with the old name, got %q", putName)
	}
	if patchedName != "nuevo.txt" {
		t.Errorf("expected PATCH rename to 'nuevo.txt', got %q", patchedName)
	}

	// The inode now has the real remote ID and the new name.
	if child.ID() != "real123" {
		t.Errorf("expected ID 'real123' after upload, got %q", child.ID())
	}
	if child.Name() != "nuevo.txt" {
		t.Errorf("expected name 'nuevo.txt', got %q", child.Name())
	}
}

// ──── UploadManager: retry policy (issue #14) ────

// fillInFlightToCap blocks new launches so the processSessions assertions below
// are deterministic (no real HTTP call is attempted).
func fillInFlightToCap(um *UploadManager) {
	um.mu.Lock()
	um.inFlight = um.inFlightCap()
	um.mu.Unlock()
}

func TestUploadManager_NetworkErrorNeverAbandons(t *testing.T) {
	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)
	defer cc.CloseAll()

	ic := NewInodeCache()
	um := NewUploadManager(graph.NewClient(), &mockTokenProvider{token: "t"}, ic, cc, 5, 3)
	fillInFlightToCap(um)

	s, _ := NewUploadSession("net-err", "root", "net.txt", []byte("data"))
	s.setState(uploadErrored, errors.New("dial tcp: connection refused"))
	s.setTransient(true)
	s.Retries = 100 // far beyond the permanent-error cap
	um.mu.Lock()
	um.sessions["net-err"] = s
	um.mu.Unlock()

	um.processSessions()

	um.mu.Lock()
	_, exists := um.sessions["net-err"]
	um.mu.Unlock()
	if !exists {
		t.Fatal("a session with a transient network error must never be abandoned")
	}
	if s.getState() != uploadPending {
		t.Errorf("expected the session to be re-scheduled as pending, got state %v", s.getState())
	}
}

func TestUploadManager_PermanentErrorAbandons(t *testing.T) {
	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)
	defer cc.CloseAll()

	ic := NewInodeCache()
	um := NewUploadManager(graph.NewClient(), &mockTokenProvider{token: "t"}, ic, cc, 5, 3)
	fillInFlightToCap(um)

	s, _ := NewUploadSession("perm-err", "root", "perm.txt", []byte("data"))
	s.setState(uploadErrored, errors.New("HTTP 400 Bad Request"))
	s.setTransient(false)
	s.Retries = um.retryCap() // next processSessions exceeds the cap
	um.mu.Lock()
	um.sessions["perm-err"] = s
	um.mu.Unlock()

	um.processSessions()

	um.mu.Lock()
	_, exists := um.sessions["perm-err"]
	um.mu.Unlock()
	if exists {
		t.Fatal("a session with a permanent error should be abandoned after the retry cap")
	}
}

func TestNewUploadManager_WiresLimits(t *testing.T) {
	cc := &ContentCache{}

	um := NewUploadManager(nil, nil, NewInodeCache(), cc, 10, 3)
	if um.maxUploadsInFlight != 10 {
		t.Errorf("expected maxUploadsInFlight 10, got %d", um.maxUploadsInFlight)
	}
	if um.maxUploadRetries != 3 {
		t.Errorf("expected maxUploadRetries 3, got %d", um.maxUploadRetries)
	}

	// Zero values fall back to the package defaults.
	um2 := NewUploadManager(nil, nil, NewInodeCache(), cc, 0, 0)
	if um2.maxUploadsInFlight != defaultMaxUploadsInFlight {
		t.Errorf("expected default maxUploadsInFlight %d, got %d", defaultMaxUploadsInFlight, um2.maxUploadsInFlight)
	}
	if um2.maxUploadRetries != defaultMaxUploadRetries {
		t.Errorf("expected default maxUploadRetries %d, got %d", defaultMaxUploadRetries, um2.maxUploadRetries)
	}
}
