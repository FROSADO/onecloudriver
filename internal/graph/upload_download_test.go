package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frosado/onecloudriver/internal/types"
)

// =============================================================================
// Upload/Download integration tests with httptest mock Graph API
// =============================================================================

// testTokenProvider returns a fixed token for tests.
type testTokenProvider struct{ token string }

func (t *testTokenProvider) GetAccessToken(_ context.Context) (string, error) {
	return t.token, nil
}

func testClient(server *httptest.Server) *Client {
	return NewClient(
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
}

// =============================================================================
// UploadItem
// =============================================================================

func TestUploadItem_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "content") {
			t.Errorf("expected content endpoint, got %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "hello world" {
			t.Errorf("expected 'hello world', got %q", string(body))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(DriveItem{
			ID:   "file-123",
			Name: "test.txt",
			Size: 11,
		})
	}))
	defer server.Close()

	client := testClient(server)
	item, err := client.UploadItem(context.Background(),
		&testTokenProvider{"token"}, ItemID("folder-1"),
		"test.txt", bytes.NewReader([]byte("hello world")), "",
	)
	if err != nil {
		t.Fatalf("UploadItem failed: %v", err)
	}
	if item.ID != "file-123" {
		t.Errorf("expected ID file-123, got %s", item.ID)
	}
}

func TestUploadItem_EmptyFileName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	client := testClient(server)

	_, err := client.UploadItem(context.Background(),
		&testTokenProvider{"token"}, ItemID("folder-1"),
		"", bytes.NewReader([]byte("data")), "",
	)
	if err == nil || !strings.Contains(err.Error(), "file name cannot be empty") {
		t.Errorf("expected 'file name cannot be empty', got: %v", err)
	}
}

func TestUploadItem_NilContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	client := testClient(server)

	_, err := client.UploadItem(context.Background(),
		&testTokenProvider{"token"}, ItemID("folder-1"),
		"test.txt", nil, "",
	)
	if err == nil || !strings.Contains(err.Error(), "content cannot be nil") {
		t.Errorf("expected 'content cannot be nil', got: %v", err)
	}
}

func TestUploadItem_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{"code": "internalError", "message": "fail"},
		})
	}))
	defer server.Close()

	client := testClient(server)
	_, err := client.UploadItem(context.Background(),
		&testTokenProvider{"token"}, ItemID("folder-1"),
		"test.txt", bytes.NewReader([]byte("data")), "",
	)
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

// TestUploadItem_UnsupportedParent verifies that UploadItem surfaces the error
// from uploadItemResourcePath when the parent is neither ItemID nor ItemPath.
func TestUploadItem_UnsupportedParent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("expected no HTTP request for an unsupported parent type")
	}))
	defer server.Close()

	client := testClient(server)
	_, err := client.UploadItem(context.Background(),
		&testTokenProvider{"token"}, fakeUploadResource("x"),
		"test.txt", bytes.NewReader([]byte("data")), "",
	)
	if err == nil || !strings.Contains(err.Error(), "unsupported parent resource type") {
		t.Errorf("expected 'unsupported parent resource type', got: %v", err)
	}
}

// TestUploadItem_ItemPathParent verifies that uploading into a folder
// addressed by ItemPath builds a valid Graph path-based URL. Regression for
// issue #142: concatenating ItemPath.ResourcePath() (which already contains
// ":/") with ":/" + fileName produced "/me/drive/root:/Documents:/file" and a
// 400 "Resource not found for the segment 'root:'" from Graph.
func TestUploadItem_ItemPathParent(t *testing.T) {
	const wantPath = "/me/drive/root:/Documents/photo.jpg:/content"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != wantPath {
			t.Errorf("expected path %q, got %q", wantPath, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "photo bytes" {
			t.Errorf("expected 'photo bytes', got %q", string(body))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(DriveItem{
			ID:   "file-456",
			Name: "photo.jpg",
			Size: 11,
		})
	}))
	defer server.Close()

	client := testClient(server)
	item, err := client.UploadItem(context.Background(),
		&testTokenProvider{"token"}, ItemPath("/Documents"),
		"photo.jpg", bytes.NewReader([]byte("photo bytes")), "",
	)
	if err != nil {
		t.Fatalf("UploadItem with ItemPath parent failed: %v", err)
	}
	if item.ID != "file-456" {
		t.Errorf("expected ID file-456, got %s", item.ID)
	}
}

// TestUploadItemResourcePath is a table-driven unit test for the child
// address helper introduced by issue #142. It pins the exact URL for both
// parent kinds (ItemID address must not change), the root and nested path
// cases, the per-segment escaping, and the unsupported-type error branch.
func TestUploadItemResourcePath(t *testing.T) {
	tests := []struct {
		name     string
		parent   Resource
		fileName string
		want     string
		wantErr  string
	}{
		{
			name:     "item id keeps the by-ID address unchanged",
			parent:   ItemID("folder-1"),
			fileName: "photo.jpg",
			want:     "/me/drive/items/folder-1:/photo.jpg",
		},
		{
			name:     "root id keeps the by-ID address unchanged",
			parent:   RootID,
			fileName: "photo.jpg",
			want:     "/me/drive/root:/photo.jpg",
		},
		{
			name:     "item path folder appends a plain segment",
			parent:   ItemPath("/Documents"),
			fileName: "photo.jpg",
			want:     "/me/drive/root:/Documents/photo.jpg",
		},
		{
			name:     "item path drive root",
			parent:   ItemPath("/"),
			fileName: "photo.jpg",
			want:     "/me/drive/root:/photo.jpg",
		},
		{
			name:     "item path nested folder",
			parent:   ItemPath("/a/b"),
			fileName: "photo.jpg",
			want:     "/me/drive/root:/a/b/photo.jpg",
		},
		{
			name:     "folder and file segments are escaped per segment",
			parent:   ItemPath("/My Docs"),
			fileName: "my file (1).jpg",
			want:     "/me/drive/root:/My%20Docs/my%20file%20%281%29.jpg",
		},
		{
			name:     "unsupported resource type",
			parent:   fakeUploadResource("x"),
			fileName: "photo.jpg",
			wantErr:  "unsupported parent resource type",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := uploadItemResourcePath(tt.parent, tt.fileName)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

// fakeUploadResource is a Resource that is neither ItemID nor ItemPath, used
// to exercise the helper's unsupported-type branch.
type fakeUploadResource string

func (f fakeUploadResource) ResourcePath() string            { return "/me/drive/root:" + string(f) }
func (f fakeUploadResource) IsEmpty() bool                   { return false }
func (f fakeUploadResource) ParentReference() map[string]any { return nil }

// =============================================================================
// UploadItemStream
// =============================================================================

func TestUploadItemStream_Success(t *testing.T) {
	uploadURL := ""

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "createUploadSession") {
			// Step 1: Create upload session
			uploadURL = server.URL + "/upload/session-123"
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(createUploadSessionResponse{UploadURL: uploadURL})
			return
		}

		if r.URL.Path == "/upload/session-123" {
			// Step 2: Upload chunks
			contentRange := r.Header.Get("Content-Range")
			if strings.Contains(contentRange, fmt.Sprintf("/%d", 11)) {
				// Last chunk → return 201 with DriveItem
				w.WriteHeader(http.StatusCreated)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(DriveItem{ID: "large-file-1", Name: "big.bin", Size: 11})
			} else {
				// Intermediate chunk → 202 Accepted
				w.WriteHeader(http.StatusAccepted)
			}
			return
		}
	}))
	defer server.Close()

	client := testClient(server)
	content := bytes.NewReader([]byte("hello world"))
	item, err := client.UploadItemStream(context.Background(),
		&testTokenProvider{"token"}, ItemID("folder-1"),
		"big.bin", content, 11, "",
	)
	if err != nil {
		t.Fatalf("UploadItemStream failed: %v", err)
	}
	if item.ID != "large-file-1" {
		t.Errorf("expected ID large-file-1, got %s", item.ID)
	}
}

func TestUploadItemStream_ZeroSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	client := testClient(server)
	_, err := client.UploadItemStream(context.Background(),
		&testTokenProvider{"token"}, ItemID("folder-1"),
		"test.bin", bytes.NewReader(nil), 0, "",
	)
	if err == nil || !strings.Contains(err.Error(), "file size must be positive") {
		t.Errorf("expected 'file size must be positive', got: %v", err)
	}
}

func TestUploadItemStream_NoUploadURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(createUploadSessionResponse{}) // empty UploadURL
	}))
	defer server.Close()

	client := testClient(server)
	_, err := client.UploadItemStream(context.Background(),
		&testTokenProvider{"token"}, ItemID("folder-1"),
		"big.bin", bytes.NewReader([]byte("data")), 4, "",
	)
	if err == nil || !strings.Contains(err.Error(), "does not contain uploadUrl") {
		t.Errorf("expected 'does not contain uploadUrl', got: %v", err)
	}
}

// TestUploadItemStream_ItemPathParent verifies that creating an upload
// session inside a folder addressed by ItemPath builds a valid Graph
// path-based URL. Regression for issue #142: the double ":/" made Graph
// reject the createUploadSession request with HTTP 400.
func TestUploadItemStream_ItemPathParent(t *testing.T) {
	const wantSessionPath = "/me/drive/root:/Documents/big.bin:/createUploadSession"

	uploadURL := ""
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "createUploadSession") {
			if r.URL.Path != wantSessionPath {
				t.Errorf("expected session path %q, got %q", wantSessionPath, r.URL.Path)
			}
			uploadURL = server.URL + "/upload/session-itempath"
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(createUploadSessionResponse{UploadURL: uploadURL})
			return
		}

		if r.URL.Path == "/upload/session-itempath" {
			w.WriteHeader(http.StatusCreated)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(DriveItem{ID: "large-file-2", Name: "big.bin", Size: 11})
			return
		}

		t.Errorf("unexpected request path %q", r.URL.Path)
	}))
	defer server.Close()

	client := testClient(server)
	item, err := client.UploadItemStream(context.Background(),
		&testTokenProvider{"token"}, ItemPath("/Documents"),
		"big.bin", bytes.NewReader([]byte("hello world")), 11, "",
	)
	if err != nil {
		t.Fatalf("UploadItemStream with ItemPath parent failed: %v", err)
	}
	if item.ID != "large-file-2" {
		t.Errorf("expected ID large-file-2, got %s", item.ID)
	}
}

// TestUploadItemStream_UnsupportedParent verifies that UploadItemStream
// surfaces the error from uploadItemResourcePath when the parent is neither
// ItemID nor ItemPath.
func TestUploadItemStream_UnsupportedParent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("expected no HTTP request for an unsupported parent type")
	}))
	defer server.Close()

	client := testClient(server)
	_, err := client.UploadItemStream(context.Background(),
		&testTokenProvider{"token"}, fakeUploadResource("x"),
		"big.bin", bytes.NewReader([]byte("data")), 4, "",
	)
	if err == nil || !strings.Contains(err.Error(), "unsupported parent resource type") {
		t.Errorf("expected 'unsupported parent resource type', got: %v", err)
	}
}

func TestUploadItemStream_CancelledContext(t *testing.T) {
	uploadURL := ""
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "createUploadSession") {
			uploadURL = server.URL + "/upload/session-1"
			json.NewEncoder(w).Encode(createUploadSessionResponse{UploadURL: uploadURL})
			return
		}
	}))
	defer server.Close()

	client := testClient(server)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before any chunk upload

	content := bytes.NewReader(bytes.Repeat([]byte("x"), int(chunkSize*2)))
	_, err := client.UploadItemStream(ctx,
		&testTokenProvider{"token"}, ItemID("folder-1"),
		"big.bin", content, chunkSize*2, "",
	)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

// =============================================================================
// GetItemContent / GetItemContentStream
// =============================================================================

func TestGetItemContent_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "content") {
			// Download endpoint — set headers BEFORE WriteHeader
			w.Header().Set("Content-Range", "bytes 0-4/5")
			w.WriteHeader(http.StatusPartialContent)
			w.Write([]byte("hello"))
			return
		}
		// Metadata endpoint
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(DriveItem{
			ID:   "file-1",
			Name: "test.txt",
			Size: 5,
		})
	}))
	defer server.Close()

	client := testClient(server)
	tp := &testTokenProvider{"token"}

	data, err := client.GetItemContent(context.Background(), tp, ItemID("file-1"))
	if err != nil {
		t.Fatalf("GetItemContent failed: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("expected 'hello', got %q", string(data))
	}
}

func TestGetItemContentStream_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "content") {
			contentRange := r.Header.Get("Range")
			if strings.Contains(contentRange, "bytes=0-9") {
				w.Header().Set("Content-Range", "bytes 0-9/10")
				w.WriteHeader(http.StatusPartialContent)
				w.Write([]byte("1234567890"))
			} else {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			}
			return
		}
		// Metadata
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(DriveItem{
			ID:   "file-1",
			Name: "data.bin",
			Size: 10,
		})
	}))
	defer server.Close()

	client := testClient(server)
	tp := &testTokenProvider{"token"}
	var buf bytes.Buffer

	n, err := client.GetItemContentStream(context.Background(), tp, ItemID("file-1"), &buf)
	if err != nil {
		t.Fatalf("GetItemContentStream failed: %v", err)
	}
	if n != 10 {
		t.Errorf("expected 10 bytes, got %d", n)
	}
	if buf.String() != "1234567890" {
		t.Errorf("expected '1234567890', got %q", buf.String())
	}
}

func TestGetItemContent_MetadataError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{"code": "itemNotFound"},
		})
	}))
	defer server.Close()

	client := testClient(server)
	_, err := client.GetItemContent(context.Background(),
		&testTokenProvider{"token"}, ItemID("nonexistent"),
	)
	if err == nil {
		t.Fatal("expected error for missing item")
	}
	if !strings.Contains(err.Error(), "error getting item metadata") {
		t.Errorf("expected metadata error, got: %v", err)
	}
}

// =============================================================================
// GetUser
// =============================================================================

func TestGetUser_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/me" {
			t.Errorf("expected /me, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(User{
			ID:                "user-123",
			UserPrincipalName: "test@outlook.com",
			DisplayName:       "Test User",
		})
	}))
	defer server.Close()

	client := testClient(server)
	user, err := client.GetUser(context.Background(), &testTokenProvider{"token"})
	if err != nil {
		t.Fatalf("GetUser failed: %v", err)
	}
	if user.UserPrincipalName != "test@outlook.com" {
		t.Errorf("expected test@outlook.com, got %s", user.UserPrincipalName)
	}
	if user.DisplayName != "Test User" {
		t.Errorf("expected Test User, got %s", user.DisplayName)
	}
}

func TestGetUser_EmptyID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(User{DisplayName: "No ID"}) // missing ID
	}))
	defer server.Close()

	client := testClient(server)
	_, err := client.GetUser(context.Background(), &testTokenProvider{"token"})
	if err == nil {
		t.Fatal("expected error for user without ID")
	}
	if !strings.Contains(err.Error(), "does not contain a valid user ID") {
		t.Errorf("expected 'does not contain a valid user ID', got: %v", err)
	}
}

// =============================================================================
// ListDriveRoot
// =============================================================================

func TestListDriveRoot_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "children") {
			t.Errorf("expected children endpoint, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value": [
			{"id": "f1", "name": "file1.txt", "size": 100},
			{"id": "f2", "name": "folder1", "folder": {"childCount": 5}}
		]}`))
	}))
	defer server.Close()

	client := testClient(server)
	items, err := client.ListDriveRoot(context.Background(), &testTokenProvider{"token"})
	if err != nil {
		t.Fatalf("ListDriveRoot failed: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
	if items[0].Name != "file1.txt" {
		t.Errorf("expected file1.txt, got %s", items[0].Name)
	}
}

// =============================================================================
// TokenProvider interface compliance check for test
// =============================================================================

var _ types.TokenProvider = (*testTokenProvider)(nil)
