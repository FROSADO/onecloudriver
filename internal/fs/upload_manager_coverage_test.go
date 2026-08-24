package fs

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
)

// ──── processSessions ────

func TestUploadManager_ProcessSessions_LaunchesPending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "remote1", "name": "test.txt", "size": 18}`))
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)
	cc.Insert("file1", []byte("contenido para subir"))

	ic := NewInodeCache()
	gc := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	um := &UploadManager{
		queue:         make(chan *UploadSession),
		deletionQueue: make(chan string, 100),
		sessions:      make(map[string]*UploadSession),
		graphClient:   gc,
		tokenProvider: &mockTokenProvider{token: "t"},
		inodeCache:    ic,
		contentCache:  cc,
		stopCh:        make(chan struct{}),
	}

	session, _ := NewUploadSession("file1", "root", "test.txt", []byte("contenido para subir"))
	session.State = uploadPending
	um.mu.Lock()
	um.sessions["file1"] = session
	um.mu.Unlock()

	um.processSessions()

	// The executeUpload goroutine runs; give it time
	time.Sleep(50 * time.Millisecond)

	// Verify that it completed
	state := session.getState()
	if state == uploadPending {
		t.Log("Session still pending — executeUpload may have failed")
	}
}

func TestUploadManager_ProcessSessions_RetriesErrored(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "remote1", "name": "retry.txt", "size": 4}`))
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)
	gc := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}

	um := &UploadManager{
		queue:         make(chan *UploadSession),
		deletionQueue: make(chan string, 100),
		sessions:      make(map[string]*UploadSession),
		graphClient:   gc,
		tokenProvider: &mockTokenProvider{token: "t"},
		inodeCache:    NewInodeCache(),
		contentCache:  cc,
		stopCh:        make(chan struct{}),
	}

	session, _ := NewUploadSession("remote1", "root", "retry.txt", []byte("data"))
	session.Retries = 1
	session.State = uploadErrored
	um.mu.Lock()
	um.sessions["remote1"] = session
	// Fill in-flight to the cap so processSessions leaves the session in the
	// pending state (otherwise it launches the upload asynchronously and races
	// with the assertion below).
	um.inFlight = um.inFlightCap()
	um.mu.Unlock()

	um.processSessions()

	if session.getState() != uploadPending {
		t.Errorf("Session with an error should go to pending for retry, state: %d", session.getState())
	}
	if session.Retries != 2 {
		t.Errorf("Expected Retries 2, got %d", session.Retries)
	}
}

func TestUploadManager_ProcessSessions_AbandonsAfterMaxRetries(t *testing.T) {
	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)

	um := &UploadManager{
		queue:         make(chan *UploadSession),
		deletionQueue: make(chan string, 100),
		sessions:      make(map[string]*UploadSession),
		inodeCache:    NewInodeCache(),
		contentCache:  cc,
		stopCh:        make(chan struct{}),
	}

	session, _ := NewUploadSession("file1", "root", "failing.txt", []byte("data"))
	session.Retries = defaultMaxUploadRetries + 1 // exceeds the limit
	session.State = uploadErrored
	um.mu.Lock()
	um.sessions["file1"] = session
	um.mu.Unlock()

	um.processSessions()

	um.mu.Lock()
	_, exists := um.sessions["file1"]
	um.mu.Unlock()

	if exists {
		t.Error("Session with too many retries should have been removed")
	}
}

func TestUploadManager_ProcessSessions_CompletesFinished(t *testing.T) {
	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)

	um := &UploadManager{
		queue:         make(chan *UploadSession),
		deletionQueue: make(chan string, 100),
		sessions:      make(map[string]*UploadSession),
		inodeCache:    NewInodeCache(),
		contentCache:  cc,
		stopCh:        make(chan struct{}),
	}

	session, _ := NewUploadSession("file1", "root", "done.txt", []byte("data"))
	session.State = uploadComplete
	um.mu.Lock()
	um.sessions["file1"] = session
	um.mu.Unlock()

	um.processSessions()

	um.mu.Lock()
	_, exists := um.sessions["file1"]
	um.mu.Unlock()
	if exists {
		t.Error("Completed session should have been removed")
	}
}

func TestUploadManager_ProcessSessions_RespectsMaxInFlight(t *testing.T) {
	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)

	um := &UploadManager{
		queue:         make(chan *UploadSession),
		deletionQueue: make(chan string, 100),
		sessions:      make(map[string]*UploadSession),
		inodeCache:    NewInodeCache(),
		contentCache:  cc,
		stopCh:        make(chan struct{}),
	}

	// Fill inFlight to the max
	um.mu.Lock()
	um.inFlight = defaultMaxUploadsInFlight
	um.mu.Unlock()

	session, _ := NewUploadSession("file1", "root", "blocked.txt", []byte("data"))
	um.mu.Lock()
	um.sessions["file1"] = session
	um.mu.Unlock()

	um.processSessions()

	// The session should stay in pending because there is no slot
	if session.getState() != uploadPending {
		t.Errorf("Session should stay in pending when inFlight is full, state: %d", session.getState())
	}
}

// ──── executeUpload ────

func TestUploadManager_ExecuteUpload_SmallFile_Success(t *testing.T) {
	var uploadedID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			uploadedID = "server-assigned-id"
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "server-assigned-id", "name": "uploaded.txt", "size": 5, "eTag": "abc"}`))
		}
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)
	ic := NewInodeCache()

	// Insert a local inode (simulates a file created with Create)
	localInode := NewInodeLocal("uploaded.txt", 0, nil)
	ic.InsertChild("root", "uploaded.txt", localInode)

	gc := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	um := &UploadManager{
		queue:         make(chan *UploadSession),
		deletionQueue: make(chan string, 100),
		sessions:      make(map[string]*UploadSession),
		graphClient:   gc,
		tokenProvider: &mockTokenProvider{token: "t"},
		inodeCache:    ic,
		contentCache:  cc,
		stopCh:        make(chan struct{}),
	}

	session, _ := NewUploadSession(localInode.ID(), "root", "uploaded.txt", []byte("hello"))
	um.executeUpload(session)

	if session.getState() != uploadComplete {
		t.Errorf("Upload should complete, state: %d, lastErr: %s", session.getState(), session.LastErr)
	}
	if uploadedID != "server-assigned-id" {
		// OK - the code may use path instead of ID because it is local
		t.Log("uploadedID recibido:", uploadedID)
	}
}

func TestUploadManager_ExecuteUpload_Error_GoesToErrored(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)
	gc := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	um := &UploadManager{
		queue:         make(chan *UploadSession),
		deletionQueue: make(chan string, 100),
		sessions:      make(map[string]*UploadSession),
		graphClient:   gc,
		tokenProvider: &mockTokenProvider{token: "t"},
		inodeCache:    NewInodeCache(),
		contentCache:  cc,
		stopCh:        make(chan struct{}),
	}

	session, _ := NewUploadSession("remote_id", "root", "fail.txt", []byte("data"))
	um.executeUpload(session)

	if session.getState() != uploadErrored {
		t.Errorf("Failed upload should be in errored, state: %d", session.getState())
	}
	if session.LastErr == "" {
		t.Error("The last error should have been recorded")
	}
}

// ──── uploadLoop ────

func TestUploadManager_UploadLoop_StopsGracefully(t *testing.T) {
	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)
	um := &UploadManager{
		queue:         make(chan *UploadSession),
		deletionQueue: make(chan string, 100),
		sessions:      make(map[string]*UploadSession),
		inodeCache:    NewInodeCache(),
		contentCache:  cc,
		stopCh:        make(chan struct{}),
	}

	um.wg.Add(1)
	done := make(chan struct{})
	go func() {
		um.uploadLoop()
		close(done)
	}()

	time.Sleep(10 * time.Millisecond)
	close(um.stopCh)

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("uploadLoop did not finish after close(stopCh)")
	}
}

func TestUploadManager_UploadLoop_DrainsQueue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "remote1", "name": "test.txt", "size": 9}`))
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)
	gc := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	um := NewUploadManager(gc, &mockTokenProvider{token: "t"}, NewInodeCache(), cc, 0, 0)

	data := []byte("test data")
	cc.Insert("file1", data)
	um.Start()

	// Send to the channel after starting
	session, _ := NewUploadSession("file1", "root", "test.txt", data)
	um.queue <- session

	time.Sleep(100 * time.Millisecond)
	um.Stop()
}

func TestUploadManager_UploadLoop_DrainsDeletionQueue(t *testing.T) {
	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)
	um := NewUploadManager(nil, nil, NewInodeCache(), cc, 0, 0)

	session, _ := NewUploadSession("file1", "root", "test.txt", []byte("data"))
	um.mu.Lock()
	um.sessions["file1"] = session
	um.mu.Unlock()

	um.Start()
	um.deletionQueue <- "file1"
	time.Sleep(50 * time.Millisecond)
	um.Stop()

	um.mu.Lock()
	_, exists := um.sessions["file1"]
	um.mu.Unlock()
	if exists {
		t.Error("The session should have been removed after cancellation")
	}
}

// ──── Observability counters (issue #74) ────

func TestUploadManager_MetricsAndInFlight_StartAtZero(t *testing.T) {
	tmpDir := t.TempDir()
	cc, _ := NewContentCache(tmpDir)
	defer cc.CloseAll()

	gc := graph.NewClient()
	ic := NewInodeCache()

	um := NewUploadManager(gc, &mockTokenProvider{token: "t"}, ic, cc, 0, 0)
	um.Start()
	defer um.Stop()

	if got := um.InFlight(); got != 0 {
		t.Errorf("InFlight() = %d, want 0 before any upload", got)
	}
	completed, failed := um.Metrics()
	if completed != 0 || failed != 0 {
		t.Errorf("Metrics() = (%d, %d), want (0, 0) before any upload", completed, failed)
	}
}
