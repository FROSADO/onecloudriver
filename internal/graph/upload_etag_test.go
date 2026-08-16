package graph

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestClient_UploadItem_IfMatchHeader verifies that UploadItem sends the
// If-Match header when a non-empty etag is provided, and omits it otherwise.
func TestClient_UploadItem_IfMatchHeader(t *testing.T) {
	tests := []struct {
		name      string
		etag      string
		wantMatch string
	}{
		{name: "with etag", etag: `"etag-123"`, wantMatch: `"etag-123"`},
		{name: "without etag", etag: "", wantMatch: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("If-Match"); got != tt.wantMatch {
					t.Errorf("expected If-Match %q, got %q", tt.wantMatch, got)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				w.Write([]byte(`{"id":"new","name":"a.txt"}`))
			}))
			defer server.Close()

			client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
			_, err := client.UploadItem(context.Background(), &mockTokenProvider{token: "t"}, RootID, "a.txt", strings.NewReader("x"), tt.etag)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestClient_UploadItem_PreconditionFailed verifies that a 412 response is
// surfaced as a typed error detectable with errors.Is(err, ErrPreconditionFailed).
func TestClient_UploadItem_PreconditionFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPreconditionFailed)
		w.Write([]byte(`{"error":{"code":"notAllowed","message":"Precondition failed"}}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	_, err := client.UploadItem(context.Background(), &mockTokenProvider{token: "t"}, RootID, "a.txt", strings.NewReader("x"), `"etag-stale"`)
	if err == nil {
		t.Fatal("expected an error for HTTP 412")
	}
	if !errors.Is(err, ErrPreconditionFailed) {
		t.Errorf("expected ErrPreconditionFailed, got: %v", err)
	}
}

// TestClient_UploadItemStream_IfMatchHeader verifies that UploadItemStream
// sends the If-Match header on the createUploadSession request.
func TestClient_UploadItemStream_IfMatchHeader(t *testing.T) {
	const wantMatch = `"etag-456"`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "createUploadSession") {
			if got := r.Header.Get("If-Match"); got != wantMatch {
				t.Errorf("expected If-Match %q on createUploadSession, got %q", wantMatch, got)
			}
			// Fail the session creation with 412 to also exercise the typed error.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPreconditionFailed)
			w.Write([]byte(`{"error":{"code":"notAllowed","message":"Precondition failed"}}`))
			return
		}
		t.Errorf("unexpected request: %s", r.URL.Path)
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	_, err := client.UploadItemStream(context.Background(), &mockTokenProvider{token: "t"}, ItemID("folder1"), "big.bin", strings.NewReader("data"), 4, wantMatch)
	if err == nil {
		t.Fatal("expected an error for HTTP 412")
	}
	if !errors.Is(err, ErrPreconditionFailed) {
		t.Errorf("expected ErrPreconditionFailed, got: %v", err)
	}
}

// TestClient_UploadItemStream_NoIfMatchWithoutETag ensures the header is not
// sent when the etag is empty, and that a normal session flow still works.
func TestClient_UploadItemStream_NoIfMatchWithoutETag(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "createUploadSession"):
			if got := r.Header.Get("If-Match"); got != "" {
				t.Errorf("If-Match header not expected, got %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"uploadUrl": server.URL + "/upload"})
		case strings.Contains(r.URL.Path, "/upload"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id":"big1","name":"big.bin","size":4}`))
		}
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	item, err := client.UploadItemStream(context.Background(), &mockTokenProvider{token: "t"}, ItemID("folder1"), "big.bin", strings.NewReader("data"), 4, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.ID != "big1" {
		t.Errorf("expected ID big1, got %s", item.ID)
	}
}

// TestClient_OverwriteItem_UsesContentByID verifies that OverwriteItem targets
// the item's own /content endpoint (not a parent:/name:/content path) and sends
// the If-Match header.
func TestClient_OverwriteItem_UsesContentByID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/me/drive/items/file1/content" {
			t.Errorf("expected /me/drive/items/file1/content, got %s", r.URL.Path)
		}
		if got := r.Header.Get("If-Match"); got != `"etag-123"` {
			t.Errorf("expected If-Match %q, got %q", `"etag-123"`, got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"file1","name":"a.txt","eTag":"\"new\""}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	item, err := client.OverwriteItem(context.Background(), &mockTokenProvider{token: "t"}, "file1", strings.NewReader("x"), `"etag-123"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.ID != "file1" {
		t.Errorf("expected ID file1, got %s", item.ID)
	}
}

// TestClient_OverwriteItemStream_UsesCreateUploadSessionByID verifies that
// OverwriteItemStream creates the upload session on the item's own endpoint.
func TestClient_OverwriteItemStream_UsesCreateUploadSessionByID(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/me/drive/items/file1/createUploadSession":
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"uploadUrl":"` + server.URL + `/upload"}`))
		case r.URL.Path == "/upload":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id":"file1","name":"big.bin","size":4}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	item, err := client.OverwriteItemStream(context.Background(), &mockTokenProvider{token: "t"}, "file1", strings.NewReader("data"), 4, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.ID != "file1" {
		t.Errorf("expected ID file1, got %s", item.ID)
	}
}
