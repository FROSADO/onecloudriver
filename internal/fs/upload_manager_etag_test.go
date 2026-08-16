package fs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
)

// TestConflictName verifies the `_conflict_<timestamp>` suffix used to preserve
// a conflicting remote version. The suffix is inserted before the extension so
// the preserved copy stays openable by its usual application.
func TestConflictName(t *testing.T) {
	ts := time.Date(2026, 8, 16, 15, 4, 5, 0, time.UTC)
	stamp := "2026-08-16_15-04-05"

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "with extension", in: "report.md", want: "report_conflict_" + stamp + ".md"},
		{name: "no extension", in: "README", want: "README_conflict_" + stamp},
		{name: "hidden file", in: ".gitignore", want: ".gitignore_conflict_" + stamp},
		{name: "multiple dots", in: "archive.tar.gz", want: "archive.tar_conflict_" + stamp + ".gz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := conflictName(tt.in, ts); got != tt.want {
				t.Errorf("conflictName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestUploadManager_ConflictResolution_IfMatch412 verifies the "local wins,
// preserve remote" policy: an upload of an existing file that gets HTTP 412
// renames the remote item with a `_conflict_` suffix and re-uploads the local
// content as a fresh item under the original name.
func TestUploadManager_ConflictResolution_IfMatch412(t *testing.T) {
	var (
		firstIfMatch string
		renameName   string
		secondPut    bool
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/items/file1/content"):
			// First attempt: overwrite by ID (item's own /content endpoint)
			// with If-Match → conflict.
			firstIfMatch = r.Header.Get("If-Match")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPreconditionFailed)
			w.Write([]byte(`{"error":{"code":"notAllowed","message":"Precondition failed"}}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/me/drive/items/file1":
			// Preserve the remote version under the conflict suffix.
			var body struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			renameName = body.Name
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"id":"file1","name":"doc.txt_conflict","eTag":"\"new-remote\""}`))
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/root:"):
			// Second attempt: local wins, new item under the original name.
			if got := r.Header.Get("If-Match"); got != "" {
				t.Errorf("second upload should not send If-Match, got %q", got)
			}
			secondPut = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id":"file2","name":"doc.txt","size":4,"eTag":"\"new-local\""}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Existing file: real remote ID + a known ETag.
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
		&ContentCache{},
		0, 0,
	)

	session, err := NewUploadSession("file1", "root", "doc.txt", []byte("hola"))
	if err != nil {
		t.Fatal(err)
	}

	um.executeUpload(session)

	if firstIfMatch != `"old-etag"` {
		t.Errorf("expected first upload If-Match %q, got %q", `"old-etag"`, firstIfMatch)
	}
	if !strings.Contains(renameName, "_conflict_") {
		t.Errorf("expected remote renamed with a _conflict_ suffix, got %q", renameName)
	}
	if !secondPut {
		t.Error("expected a second upload (local wins) after the conflict")
	}
	if ic.Get("file1") != nil {
		t.Error("expected the old remote ID to be released after the conflict re-upload")
	}
	moved := ic.Get("file2")
	if moved == nil {
		t.Fatal("expected the inode to move to the new item ID")
	}
	if moved.DriveItem.ETag != `"new-local"` {
		t.Errorf("expected updated ETag %q, got %q", `"new-local"`, moved.DriveItem.ETag)
	}
	if session.getState() != uploadComplete {
		t.Errorf("expected session to be complete, got state %v", session.getState())
	}
}
