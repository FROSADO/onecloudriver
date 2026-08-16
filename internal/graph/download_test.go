package graph

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ExampleClient_GetItemContent demonstrates how to download the content of a file.
func ExampleClient_GetItemContent() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content := []byte("downloaded content")
		switch {
		case !strings.Contains(r.URL.Path, "/content"):
			// Metadata
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id": "file123", "name": "file.txt", "size": %d}`, len(content))
		default:
			// Content
			if r.Header.Get("Range") != "" {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(content)-1, len(content)))
				w.WriteHeader(http.StatusPartialContent)
			}
			w.Write(content)
		}
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	data, err := client.GetItemContent(context.Background(), tokenProvider, ItemID("file123"))
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println(string(data))

	// Output:
	// downloaded content
}

// TestClient_GetItemContent_Success tests binary content download by ID and by path.
func TestClient_GetItemContent_Success(t *testing.T) {
	tests := []struct {
		name            string
		resource        Resource
		metadataPath    string
		contentPath     string
		metadataJSON    string
		expectedContent string
	}{
		{
			name:            "by ID",
			resource:        ItemID("file789"),
			metadataPath:    "/me/drive/items/file789",
			contentPath:     "/me/drive/items/file789/content",
			metadataJSON:    `{"id": "file789", "name": "archivo.bin", "size": 29}`,
			expectedContent: "contenido binario del archivo",
		},
		{
			name:            "by Path",
			resource:        ItemPath("/Docs/archivo.txt"),
			metadataPath:    "/me/drive/root:/Docs/archivo.txt",
			contentPath:     "/me/drive/root:/Docs/archivo.txt:/content",
			metadataJSON:    `{"id": "txt123", "name": "archivo.txt", "size": 10}`,
			expectedContent: "hola mundo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case tt.metadataPath:
					w.Header().Set("Content-Type", "application/json")
					w.Write([]byte(tt.metadataJSON))
				case tt.contentPath:
					if r.Header.Get("Authorization") != "Bearer test_token" {
						t.Error("Token was not sent correctly")
					}
					content := []byte(tt.expectedContent)
					w.Header().Set("Content-Type", "application/octet-stream")
					if r.Header.Get("Range") != "" {
						w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(content)-1, len(content)))
						w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
						w.WriteHeader(http.StatusPartialContent)
					}
					w.Write(content)
				default:
					t.Errorf("Unexpected path: %s", r.URL.Path)
				}
			}))
			defer server.Close()

			client := &Client{
				BaseURL:    server.URL,
				HTTPClient: server.Client(),
			}

			tokenProvider := &mockTokenProvider{token: "test_token"}

			content, err := client.GetItemContent(context.Background(), tokenProvider, tt.resource)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if string(content) != tt.expectedContent {
				t.Errorf("Incorrect content: %s", string(content))
			}
		})
	}
}

// TestClient_GetItemContentStream_Success tests streaming download by ID and by path.
func TestClient_GetItemContentStream_Success(t *testing.T) {
	tests := []struct {
		name            string
		resource        Resource
		metadataPath    string
		contentPath     string
		metadataJSON    string
		expectedContent string
	}{
		{
			name:            "by ID",
			resource:        ItemID("file123"),
			metadataPath:    "/me/drive/items/file123",
			contentPath:     "/me/drive/items/file123/content",
			metadataJSON:    `{"id": "file123", "name": "small.txt", "size": 25}`,
			expectedContent: "content of the small file",
		},
		{
			name:            "by Path",
			resource:        ItemPath("/Docs/data.bin"),
			metadataPath:    "/me/drive/root:/Docs/data.bin",
			contentPath:     "/me/drive/root:/Docs/data.bin:/content",
			metadataJSON:    `{"id": "data123", "name": "data.bin", "size": 12}`,
			expectedContent: "binary data!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case tt.metadataPath:
					w.Header().Set("Content-Type", "application/json")
					w.Write([]byte(tt.metadataJSON))
				case tt.contentPath:
					if r.Header.Get("Authorization") != "Bearer test_token" {
						t.Error("Token was not sent correctly")
					}
					content := []byte(tt.expectedContent)
					w.Header().Set("Content-Type", "application/octet-stream")
					if r.Header.Get("Range") != "" {
						w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(content)-1, len(content)))
						w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
						w.WriteHeader(http.StatusPartialContent)
					}
					w.Write(content)
				default:
					t.Errorf("Unexpected path: %s", r.URL.Path)
				}
			}))
			defer server.Close()

			client := &Client{
				BaseURL:    server.URL,
				HTTPClient: server.Client(),
			}

			tokenProvider := &mockTokenProvider{token: "test_token"}

			var buf bytes.Buffer
			n, err := client.GetItemContentStream(context.Background(), tokenProvider, tt.resource, &buf)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if n != int64(len(tt.expectedContent)) {
				t.Errorf("Written bytes: expected %d, got %d", len(tt.expectedContent), n)
			}
			if buf.String() != tt.expectedContent {
				t.Errorf("Incorrect content: %s", buf.String())
			}
		})
	}
}

// TestClient_GetItemContentStream_LargeFile tests chunked download of a large file (>10 MB)
func TestClient_GetItemContentStream_LargeFile(t *testing.T) {
	expectedRanges := []string{
		"bytes=0-10485759",
		"bytes=10485760-20971519",
		"bytes=20971520-26214399",
	}
	var actualRanges []string
	const totalFileSize int64 = 26214400

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/me/drive/items/bigfile":
			// Metadata: 25 MB
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "bigfile", "name": "grande.zip", "size": 26214400}`))
		case "/me/drive/items/bigfile/content":
			rangeHeader := r.Header.Get("Range")
			if rangeHeader == "" {
				t.Error("Range header was expected in the chunk request")
			}
			actualRanges = append(actualRanges, rangeHeader)

			// Parse the Range header to generate the chunk of the correct size
			var chunkStart, chunkEnd int64
			fmt.Sscanf(rangeHeader, "bytes=%d-%d", &chunkStart, &chunkEnd)
			chunkLen := chunkEnd - chunkStart + 1

			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", chunkStart, chunkEnd, totalFileSize))
			w.Header().Set("Content-Length", fmt.Sprintf("%d", chunkLen))
			w.WriteHeader(http.StatusPartialContent)
			// Write chunkLen bytes (without allocating everything in memory: write in blocks)
			buf := make([]byte, 4096)
			for i := range buf {
				buf[i] = 'X'
			}
			var written int64
			for written < chunkLen {
				toWrite := int64(len(buf))
				if written+toWrite > chunkLen {
					toWrite = chunkLen - written
				}
				w.Write(buf[:toWrite])
				written += toWrite
			}
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	tokenProvider := &mockTokenProvider{token: "test_token"}

	var buf bytes.Buffer
	n, err := client.GetItemContentStream(context.Background(), tokenProvider, ItemID("bigfile"), &buf)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify that 3 chunks were made and the Range headers are correct
	if len(actualRanges) != 3 {
		t.Errorf("Expected 3 chunks, got %d", len(actualRanges))
	}
	for i, expected := range expectedRanges {
		if i >= len(actualRanges) {
			break
		}
		if actualRanges[i] != expected {
			t.Errorf("Chunk %d: expected Range %q, got %q", i, expected, actualRanges[i])
		}
	}

	if n != totalFileSize {
		t.Errorf("Written bytes: expected %d, got %d", totalFileSize, n)
	}
	if int64(buf.Len()) != totalFileSize {
		t.Errorf("Buffer size: expected %d, got %d", totalFileSize, buf.Len())
	}
}

// TestClient_GetItemContentStream_NetworkError verifies that a network error
// during the download propagates correctly and is not swallowed.
func TestClient_GetItemContentStream_NetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Metadata: responds normally
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "file123", "name": "test.bin", "size": 100}`))
	}))
	defer server.Close()

	// mockHTTPDoer that fails only on content requests
	inner := &mockHTTPDoer{
		doFn: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/content") {
				return nil, fmt.Errorf("connection refused")
			}
			return server.Client().Do(req)
		},
	}

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: inner,
	}

	tokenProvider := &mockTokenProvider{token: "test_token"}

	var buf bytes.Buffer
	_, err := client.GetItemContentStream(context.Background(), tokenProvider, ItemID("file123"), &buf)
	if err == nil {
		t.Fatal("A network error was expected")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("Unexpected error: %v", err)
	}
}

// TestClient_GetItemContentStream_Timeout verifies that a context with a deadline
// expired during the download returns context.DeadlineExceeded.
func TestClient_GetItemContentStream_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/content"):
			// The content request blocks until the context is cancelled
			<-r.Context().Done()
			return
		default:
			// Metadata: responds fast
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "file123", "name": "lento.bin", "size": 1024}`))
		}
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	tokenProvider := &mockTokenProvider{token: "test_token"}

	var buf bytes.Buffer
	_, err := client.GetItemContentStream(ctx, tokenProvider, ItemID("file123"), &buf)
	if err == nil {
		t.Fatal("Expected a timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) &&
		!strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("Unexpected error: %v", err)
	}
}

// TestClient_GetItemContentStream_RangeNotSatisfiable verifies that the
// error 416 (Range Not Satisfiable) from OneDrive is handled correctly.
func TestClient_GetItemContentStream_RangeNotSatisfiable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/content"):
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			w.Write([]byte(`{
				"error": {
					"code": "invalidRange",
					"message": "The range specified is invalid for the current size of the resource."
				}
			}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "file123", "name": "test.bin", "size": 50}`))
		}
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	tokenProvider := &mockTokenProvider{token: "test_token"}

	var buf bytes.Buffer
	_, err := client.GetItemContentStream(context.Background(), tokenProvider, ItemID("file123"), &buf)
	if err == nil {
		t.Fatal("An error was expected for an invalid range")
	}
	// The error must contain information about the HTTP 416 status code
	if !strings.Contains(err.Error(), "416") &&
		!strings.Contains(err.Error(), "invalidRange") {
		t.Errorf("Unexpected error: %v", err)
	}
}

// TestClient_GetItemContentStream_ZeroByteFile verifies that the download of
// an empty file (0 bytes) returns 0 bytes written without an error.
func TestClient_GetItemContentStream_ZeroByteFile(t *testing.T) {
	contentRequested := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/content") {
			contentRequested = true
		}
		// Metadata: empty file
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "empty", "name": "vacio.bin", "size": 0}`))
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	tokenProvider := &mockTokenProvider{token: "test_token"}

	var buf bytes.Buffer
	n, err := client.GetItemContentStream(context.Background(), tokenProvider, ItemID("empty"), &buf)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if n != 0 {
		t.Errorf("Written bytes: expected 0, got %d", n)
	}
	if buf.Len() != 0 {
		t.Errorf("Buffer size: expected 0, got %d", buf.Len())
	}

	// Verify that a content request was NEVER made
	if contentRequested {
		t.Error("A content request should not have been made for an empty file")
	}
}

// TestClient_GetItemContentStream_HashVerification_Success verifies that a
// download whose body matches the server quickXorHash succeeds (issue #32).
func TestClient_GetItemContentStream_HashVerification_Success(t *testing.T) {
	content := []byte("integrity-verified content")
	contentHash := SumQuickXORHash(content)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/me/drive/items/filehash":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"filehash","name":"x.bin","size":%d,"file":{"hashes":{"quickXorHash":"%s"}}}`, len(content), contentHash)
		case "/me/drive/items/filehash/content":
			if r.Header.Get("Range") != "" {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(content)-1, len(content)))
				w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
				w.WriteHeader(http.StatusPartialContent)
			}
			w.Write(content)
		}
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	var buf bytes.Buffer
	n, err := client.GetItemContentStream(context.Background(), tokenProvider, ItemID("filehash"), &buf)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if n != int64(len(content)) {
		t.Errorf("Written bytes: expected %d, got %d", len(content), n)
	}
	if buf.String() != string(content) {
		t.Errorf("Incorrect content: %q", buf.String())
	}
}

// TestClient_GetItemContentStream_HashVerification_Mismatch verifies that a
// corrupt download (body does not match the server quickXorHash) is rejected.
func TestClient_GetItemContentStream_HashVerification_Mismatch(t *testing.T) {
	content := []byte("corrupted body bytes")
	// Hash of DIFFERENT content than what the server actually returns.
	wrongHash := SumQuickXORHash([]byte("what the server expects"))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/me/drive/items/filebad":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"filebad","name":"x.bin","size":%d,"file":{"hashes":{"quickXorHash":"%s"}}}`, len(content), wrongHash)
		case "/me/drive/items/filebad/content":
			if r.Header.Get("Range") != "" {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(content)-1, len(content)))
				w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
				w.WriteHeader(http.StatusPartialContent)
			}
			w.Write(content)
		}
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	var buf bytes.Buffer
	_, err := client.GetItemContentStream(context.Background(), tokenProvider, ItemID("filebad"), &buf)
	if err == nil {
		t.Fatal("Expected an integrity mismatch error")
	}
	if !strings.Contains(err.Error(), "quickXorHash mismatch") {
		t.Errorf("Unexpected error: %v", err)
	}
}
