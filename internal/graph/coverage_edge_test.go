package graph

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// ModTimeString — models.go (0% → 100%)
// =============================================================================

func TestModTimeString_NilModTime(t *testing.T) {
	item := &DriveItem{ModTime: nil}
	if s := item.ModTimeString(); s != "N/A" {
		t.Errorf("expected 'N/A', got %q", s)
	}
}

func TestModTimeString_WithModTime(t *testing.T) {
	tm := time.Date(2024, 3, 15, 10, 30, 0, 0, time.UTC)
	item := &DriveItem{ModTime: &tm}
	expected := "2024-03-15 10:30:00"
	if s := item.ModTimeString(); s != expected {
		t.Errorf("expected %q, got %q", expected, s)
	}
}

// =============================================================================
// downloadRange — download.go (78.9% → 100%)
// =============================================================================

func TestDownloadRange_IncompleteChunk(t *testing.T) {
	content := []byte("short")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(content)-1, len(content)))
		w.WriteHeader(http.StatusPartialContent)
		// Write less than expected (Content-Range says len(content) but we write 2)
		w.Write(content[:2])
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	var buf testWriter
	n, err := client.downloadRange(context.Background(), tokenProvider, server.URL+"/content", 0, int64(len(content))-1, &buf)

	if err == nil {
		t.Fatal("expected error for incomplete chunk")
	}
	if !strings.Contains(err.Error(), "incomplete chunk") {
		t.Errorf("expected 'incomplete chunk', got: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 bytes written, got %d", n)
	}
}

func TestDownloadRange_MissingContentRange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte("hello"))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	var buf testWriter
	_, err := client.downloadRange(context.Background(), tokenProvider, server.URL+"/content", 0, 4, &buf)

	if err == nil {
		t.Fatal("expected error for missing Content-Range header")
	}
	if !strings.Contains(err.Error(), "Content-Range") {
		t.Errorf("expected 'Content-Range', got: %v", err)
	}
}

func TestDownloadRange_UnexpectedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // 200 instead of 206
		w.Write([]byte("hello"))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	var buf testWriter
	_, err := client.downloadRange(context.Background(), tokenProvider, server.URL+"/content", 0, 4, &buf)

	if err == nil {
		t.Fatal("expected error for unexpected status")
	}
	if !strings.Contains(err.Error(), "expected 206") {
		t.Errorf("expected 'expected 206', got: %v", err)
	}
}

// =============================================================================
// PollDelta — delta.go (75.0% → 100%)
// =============================================================================

func TestPollDelta_RelativeURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value": [], "@odata.deltaLink": "/v1.0/me/drive/root/delta?token=abc"}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	// Pass a relative URL (without http prefix)
	_, nextLink, continueToNext, err := client.PollDelta(context.Background(), tokenProvider, "/me/drive/root/delta")

	if err != nil {
		t.Fatalf("PollDelta failed: %v", err)
	}
	if continueToNext {
		t.Error("expected cont=false for deltaLink (last page)")
	}
	if nextLink == "" {
		t.Error("expected non-empty nextLink")
	}
}

func TestPollDelta_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not-valid-json`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	_, _, _, err := client.PollDelta(context.Background(), tokenProvider, "")

	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "error parsing delta response") {
		t.Errorf("expected 'error parsing delta response', got: %v", err)
	}
}

func TestPollDelta_HTTPErrorBadStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	_, _, _, err := client.PollDelta(context.Background(), tokenProvider, "")

	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "error in delta response") {
		t.Errorf("expected 'error in delta response', got: %v", err)
	}
}

// =============================================================================
// UploadItemStream — upload.go (81.4% → ~95%)
// =============================================================================

func TestUploadItemStream_ChunkHTTPError(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "createUploadSession"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"uploadUrl": "http://` + r.Host + `/upload"}`))
		case strings.Contains(r.URL.Path, "/upload"):
			callCount++
			if callCount == 1 {
				// First chunk: return 500 error
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error": {"message": "internal error"}}`))
			} else {
				// Second chunk: success
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				w.Write([]byte(`{"id": "file123", "name": "large.bin", "size": 400000}`))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(WithBaseURL(server.URL), WithHTTPClient(server.Client()), WithRetry(0))
	tokenProvider := &mockTokenProvider{token: "test_token"}

	content := io.NopCloser(strings.NewReader(strings.Repeat("x", 400000)))
	_, err := client.UploadItemStream(context.Background(), tokenProvider, ItemID("folder123"), "large.bin", content, 400000, "")

	if err == nil {
		t.Fatal("expected error for chunk HTTP 500")
	}
	if !strings.Contains(err.Error(), "error uploading chunk") {
		t.Errorf("expected 'error uploading chunk', got: %v", err)
	}
}

func TestUploadItemStream_NoDriveItemInLastChunk(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "createUploadSession"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"uploadUrl": "http://` + r.Host + `/upload"}`))
		case strings.Contains(r.URL.Path, "/upload"):
			// Return 202 (Accepted) for every chunk — never returns 201/200 with DriveItem
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(WithBaseURL(server.URL), WithHTTPClient(server.Client()), WithRetry(0))
	tokenProvider := &mockTokenProvider{token: "test_token"}

	// File fits in one 320 KiB chunk
	content := io.NopCloser(strings.NewReader(strings.Repeat("x", 100000)))
	_, err := client.UploadItemStream(context.Background(), tokenProvider, ItemID("folder123"), "small.bin", content, 100000, "")

	if err == nil {
		t.Fatal("expected error when no DriveItem in last chunk")
	}
	if !strings.Contains(err.Error(), "DriveItem not received") {
		t.Errorf("expected 'DriveItem not received', got: %v", err)
	}
}

func TestUploadItemStream_ReadError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "createUploadSession") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"uploadUrl": "http://` + r.Host + `/upload"}`))
		} else {
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer server.Close()

	client := NewClient(WithBaseURL(server.URL), WithHTTPClient(server.Client()), WithRetry(0))
	tokenProvider := &mockTokenProvider{token: "test_token"}

	// Reader that fails after some bytes
	errReader := &errorReader{data: strings.Repeat("x", 320000), failAt: 100}
	_, err := client.UploadItemStream(context.Background(), tokenProvider, ItemID("folder123"), "file.bin", errReader, 400000, "")

	if err == nil {
		t.Fatal("expected error when reader fails")
	}
	if !strings.Contains(err.Error(), "error reading chunk") {
		t.Errorf("expected 'error reading chunk', got: %v", err)
	}
}

// =============================================================================
// cancelUploadSession — upload.go (71.4% → 100%)
// =============================================================================

func TestCancelUploadSession_Success(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			called = true
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	client.cancelUploadSession(server.URL + "/upload")

	if !called {
		t.Error("expected DELETE request to be sent")
	}
}

func TestCancelUploadSession_NetworkError(_ *testing.T) {
	// Use a closed server to simulate network error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close() // close immediately

	client := &Client{BaseURL: "http://127.0.0.1:1", HTTPClient: server.Client()}
	// Should not panic — cancelUploadSession handles errors silently
	client.cancelUploadSession(server.URL + "/upload")
}

// =============================================================================
// RetryDoer.Do — client.go (88.9% → 100%)
// =============================================================================

func TestRetryDoer_MaxRetriesExhausted(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusServiceUnavailable) // 503 — retryable
	}))
	defer server.Close()

	// Create a RetryDoer with only 1 retry (max 2 total attempts)
	httpClient := server.Client()
	retryDoer := NewRetryDoer(httpClient, 1)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/test", nil)
	resp, err := retryDoer.Do(req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", resp.StatusCode)
	}
	if callCount != 2 { // initial + 1 retry
		t.Errorf("expected 2 calls, got %d", callCount)
	}
	resp.Body.Close()
}

// =============================================================================
// UploadItem — upload.go (88.9% → 100%)
// =============================================================================

func TestUploadItem_BadJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`not-valid-json!!!!`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	content := strings.NewReader("hello world")
	_, err := client.UploadItem(context.Background(), tokenProvider, ItemID("folder123"), "file.txt", content, "")

	if err == nil {
		t.Fatal("expected error for bad JSON response")
	}
	if !strings.Contains(err.Error(), "error parsing Graph response") {
		t.Errorf("expected 'error parsing Graph response', got: %v", err)
	}
}

// =============================================================================
// Helpers
// =============================================================================

// testWriter implements io.Writer for capturing written bytes in tests.
type testWriter struct {
	buf []byte
}

func (tw *testWriter) Write(p []byte) (int, error) {
	tw.buf = append(tw.buf, p...)
	return len(p), nil
}

// errorReader is an io.Reader that fails after reading failAt bytes.
type errorReader struct {
	data   string
	failAt int
	read   int
}

func (er *errorReader) Read(p []byte) (int, error) {
	if er.read >= er.failAt {
		return 0, fmt.Errorf("simulated read error at byte %d", er.read)
	}
	n := copy(p, er.data[er.read:])
	er.read += n
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}
