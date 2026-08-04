package fs

import (
	"encoding/json"
	"io"
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
		t.Errorf("ID esperado 'local-abc', obtenido %q", s.ID)
	}
	if s.ParentID != "root" {
		t.Errorf("ParentID esperado 'root', obtenido %q", s.ParentID)
	}
	if s.Name != "test.txt" {
		t.Errorf("Name esperado 'test.txt', obtenido %q", s.Name)
	}
	if string(s.Data) != "hello world" {
		t.Errorf("Data esperado 'hello world', obtenido %q", string(s.Data))
	}
	if s.getState() != uploadPending {
		t.Errorf("Estado inicial esperado uploadPending, obtenido %v", s.getState())
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
		t.Errorf("LastErr esperado 'unexpected EOF', obtenido %q", s2.LastErr)
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
		t.Errorf("ID: esperado %q, obtenido %q", s1.ID, s2.ID)
	}
	if s2.ParentID != s1.ParentID {
		t.Errorf("ParentID: esperado %q, obtenido %q", s1.ParentID, s2.ParentID)
	}
	if s2.Name != s1.Name {
		t.Errorf("Name: esperado %q, obtenido %q", s1.Name, s2.Name)
	}
	if string(s2.Data) != string(s1.Data) {
		t.Errorf("Data: esperado %q, obtenido %q", string(s1.Data), string(s2.Data))
	}
	if s2.Retries != 2 {
		t.Errorf("Retries: esperado 2, obtenido %d", s2.Retries)
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
		t.Errorf("Esperado 'hello', obtenido %q", string(result))
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
		t.Errorf("Read: esperado 5, obtenido %d", n)
	}
	if string(buf) != "hello" {
		t.Errorf("Esperado 'hello', obtenido %q", string(buf))
	}

	rest, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if string(rest) != " world" {
		t.Errorf("Esperado ' world', obtenido %q", string(rest))
	}
}

func TestByteReader_ReadEmpty(t *testing.T) {
	r := &byteReader{data: []byte{}}
	buf := make([]byte, 10)
	n, err := r.Read(buf)
	if err != io.EOF {
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
		t.Errorf("ReadAll: esperado 'hello readall', obtenido %q", string(data))
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
	um := NewUploadManager(gc, &mockTokenProvider{token: "t"}, ic, cc)

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
	um := NewUploadManager(gc, &mockTokenProvider{token: "t"}, ic, cc)
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
		t.Errorf("Esperadas 0 sesiones, hay %d", count)
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
	um := NewUploadManager(gc, &mockTokenProvider{token: "t"}, ic, cc)
	um.Start()
	defer um.Stop()

	um.QueueUpload("persist-id", "root", "persist.txt")

	// Esperar a que se persista (el uploadLoop lo guarda en el case queue)
	time.Sleep(200 * time.Millisecond)

	raw := ic.LoadUploadSessions()
	if len(raw) != 1 {
		t.Fatalf("Expected 1 session in BoltDB, got %d", len(raw))
	}

	var session UploadSession
	if data, ok := raw["persist-id"]; ok {
		if err := json.Unmarshal(data, &session); err != nil {
			t.Fatalf("Error deserializando: %v", err)
		}
		if session.Name != "persist.txt" {
			t.Errorf("Name esperado 'persist.txt', obtenido %q", session.Name)
		}
	} else {
		t.Fatal("'persist-id' was not found in BoltDB")
	}

	// Clean up the BoltDB session
	ic.DeleteUploadSession("persist-id")
	raw = ic.LoadUploadSessions()
	if len(raw) != 0 {
		t.Errorf("Se esperaban 0 sesiones tras DeleteUploadSession, hay %d", len(raw))
	}
}

// ──── UploadManager: enqueueSession (unit test, sin HTTP) ────

func TestUploadManager_EnqueueSession_Dedup(t *testing.T) {
	gc := graph.NewClient()
	ic := NewInodeCache()
	cc := &ContentCache{}
	um := NewUploadManager(gc, &mockTokenProvider{token: "t"}, ic, cc)

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
	um := NewUploadManager(gc, &mockTokenProvider{token: "t"}, ic, cc)

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
		t.Errorf("Esperadas 0 sesiones, hay %d", count)
	}
}
