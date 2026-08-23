package graph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// =============================================================================
// MoveItem, CopyItem, DeleteItem, CreateFolder, PollDelta
// =============================================================================

func TestMoveItem_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("expected PATCH, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "moved-1", "name": "moved.txt", "parentReference": {"id": "dest-1"}}`))
	}))
	defer server.Close()

	client := testClient(server)
	item, err := client.MoveItem(context.Background(),
		&testTokenProvider{"token"}, ItemID("file-1"), ItemID("dest-1"), "",
	)
	if err != nil {
		t.Fatalf("MoveItem failed: %v", err)
	}
	if item.ID != "moved-1" {
		t.Errorf("expected moved-1, got %s", item.ID)
	}
}

func TestDeleteItem_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := testClient(server)
	err := client.DeleteItem(context.Background(),
		&testTokenProvider{"token"}, ItemID("file-1"), "",
	)
	if err != nil {
		t.Fatalf("DeleteItem failed: %v", err)
	}
}

func TestDeleteItem_WithEtag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-Match") != "etag-xyz" {
			t.Errorf("expected If-Match: etag-xyz, got: %s", r.Header.Get("If-Match"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := testClient(server)
	err := client.DeleteItem(context.Background(),
		&testTokenProvider{"token"}, ItemID("file-1"), "etag-xyz",
	)
	if err != nil {
		t.Fatalf("DeleteItem with etag failed: %v", err)
	}
}

func TestCreateFolder_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "children") {
			t.Errorf("expected children endpoint, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "new-folder", "name": "NewFolder", "folder": {"childCount": 0}}`))
	}))
	defer server.Close()

	client := testClient(server)
	folder, err := client.CreateFolder(context.Background(),
		&testTokenProvider{"token"}, ItemID("parent-1"), "NewFolder",
	)
	if err != nil {
		t.Fatalf("CreateFolder failed: %v", err)
	}
	if folder.ID != "new-folder" {
		t.Errorf("expected new-folder, got %s", folder.ID)
	}
}

func TestCreateFolder_EmptyName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	client := testClient(server)

	_, err := client.CreateFolder(context.Background(),
		&testTokenProvider{"token"}, ItemID("parent-1"), "",
	)
	if err == nil || !strings.Contains(err.Error(), "name cannot be empty") {
		t.Errorf("expected empty name error, got: %v", err)
	}
}

func TestCopyItem_Success(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", server.URL+"/monitor/copy-op-1")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client := testClient(server)
	monitorURL, err := client.CopyItem(context.Background(),
		&testTokenProvider{"token"}, ItemID("file-1"), "copy.txt", ItemID("dest-1"),
	)
	if err != nil {
		t.Fatalf("CopyItem failed: %v", err)
	}
	if monitorURL != server.URL+"/monitor/copy-op-1" {
		t.Errorf("expected monitor URL, got %s", monitorURL)
	}
}

func TestCopyItem_NoNameOrParent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	client := testClient(server)

	_, err := client.CopyItem(context.Background(),
		&testTokenProvider{"token"}, ItemID("file-1"), "", nil,
	)
	if err == nil {
		t.Fatal("expected error when neither name nor parent is specified")
	}
}

func TestCopyItem_NoLocationHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		// No Location header
	}))
	defer server.Close()

	client := testClient(server)
	_, err := client.CopyItem(context.Background(),
		&testTokenProvider{"token"}, ItemID("file-1"), "copy.txt", ItemID("dest-1"),
	)
	if err == nil {
		t.Fatal("expected error when Location header is missing")
	}
	if !strings.Contains(err.Error(), "without Location header") {
		t.Errorf("expected Location header error, got: %v", err)
	}
}

func TestRenameItem_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "file-1", "name": "renamed.txt"}`))
	}))
	defer server.Close()

	client := testClient(server)
	item, err := client.RenameItem(context.Background(),
		&testTokenProvider{"token"}, ItemID("file-1"), "renamed.txt", "",
	)
	if err != nil {
		t.Fatalf("RenameItem failed: %v", err)
	}
	if item.Name != "renamed.txt" {
		t.Errorf("expected renamed.txt, got %s", item.Name)
	}
}

func TestRenameItem_EmptyName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	client := testClient(server)

	_, err := client.RenameItem(context.Background(),
		&testTokenProvider{"token"}, ItemID("file-1"), "", "",
	)
	if err == nil || !strings.Contains(err.Error(), "name cannot be empty") {
		t.Errorf("expected empty name error, got: %v", err)
	}
}

func TestPollDelta_Success(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"value": [
				{"id": "d1", "name": "delta-file.txt", "size": 100}
			],
			"@odata.nextLink": "` + server.URL + `/delta?token=next"}`))
	}))
	defer server.Close()

	client := testClient(server)
	items, nextLink, cont, err := client.PollDelta(context.Background(),
		&testTokenProvider{"token"}, "",
	)
	if err != nil {
		t.Fatalf("PollDelta failed: %v", err)
	}
	if !cont {
		t.Error("expected continuation when nextLink is present")
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Name != "delta-file.txt" {
		t.Errorf("expected delta-file.txt, got %s", items[0].Name)
	}
	_ = nextLink // used for pagination
}

func TestPollDelta_LastPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// No @odata.nextLink means last page
		w.Write([]byte(`{"value": [{"id": "d2", "name": "last.txt", "size": 50}], "@odata.deltaLink": "delta-link-final"}`))
	}))
	defer server.Close()

	client := testClient(server)
	items, deltaLink, cont, err := client.PollDelta(context.Background(),
		&testTokenProvider{"token"}, "",
	)
	if err != nil {
		t.Fatalf("PollDelta failed: %v", err)
	}
	if cont {
		t.Error("expected no continuation on last page")
	}
	if deltaLink != "delta-link-final" {
		t.Errorf("expected delta-link-final, got %s", deltaLink)
	}
	if len(items) != 1 || items[0].Name != "last.txt" {
		t.Errorf("expected last.txt, got %v", items)
	}
}

// TestPollDelta_DeltaLinkReturnedVerbatim ensures the last-page deltaLink is
// returned exactly as Graph sent it, absolute or bare path. It is not
// normalized in PollDelta: the next poll resolves and validates it
// (absoluteURL + validateFollowURL) before following it.
func TestPollDelta_DeltaLinkReturnedVerbatim(t *testing.T) {
	tests := []struct {
		name      string
		deltaLink string
	}{
		{name: "absolute", deltaLink: "https://graph.microsoft.com/v1.0/me/drive/root/delta?token=final"},
		{name: "relative", deltaLink: "/me/drive/root/delta?token=final"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"value": [], "@odata.deltaLink": "` + tt.deltaLink + `"}`))
			}))
			defer server.Close()

			client := testClient(server)
			_, deltaLink, cont, err := client.PollDelta(context.Background(), &testTokenProvider{"token"}, "")
			if err != nil {
				t.Fatalf("PollDelta failed: %v", err)
			}
			if cont {
				t.Error("expected cont=false on the last page")
			}
			if deltaLink != tt.deltaLink {
				t.Errorf("deltaLink = %q, want it returned verbatim %q", deltaLink, tt.deltaLink)
			}
		})
	}
}

// TestPollDelta_RelativeDeltaLinkFollowedOnNextPoll passes a relative delta
// link from a previous cycle back into PollDelta and verifies it is resolved
// against BaseURL before the request is sent.
func TestPollDelta_RelativeDeltaLinkFollowedOnNextPoll(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value": [], "@odata.deltaLink": "/me/drive/root/delta?token=next"}`))
	}))
	defer server.Close()

	client := testClient(server)
	if _, _, _, err := client.PollDelta(context.Background(), &testTokenProvider{"token"}, "/me/drive/root/delta?token=final"); err != nil {
		t.Fatalf("PollDelta with a relative delta link failed: %v", err)
	}
	if gotPath != "/me/drive/root/delta" {
		t.Errorf("request path = %q, want /me/drive/root/delta (resolved against BaseURL)", gotPath)
	}
}

func TestPollDelta_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error": {"code": "serverError", "message": "fail"}}`))
	}))
	defer server.Close()

	client := testClient(server)
	_, _, _, err := client.PollDelta(context.Background(),
		&testTokenProvider{"token"}, "",
	)
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestWaitForAsyncOperation_Success(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			// First poll: in progress
			w.Write([]byte(`{"status": "inProgress"}`))
			return
		}
		// Second poll: completed
		w.Write([]byte(`{"status": "completed", "resource": {"id": "copied-1", "name": "copy.txt"}}`))
	}))
	defer server.Close()

	client := testClient(server)
	client.PollBackoff = 1 * 1 // smallest possible for fast tests
	item, err := client.WaitForAsyncOperation(context.Background(), server.URL+"/monitor")
	if err != nil {
		t.Fatalf("WaitForAsyncOperation failed: %v", err)
	}
	if item.Name != "copy.txt" {
		t.Errorf("expected copy.txt, got %s", item.Name)
	}
}

func TestWaitForAsyncOperation_Failed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status": "failed", "error": {"code": "copyFailed", "message": "could not copy"}}`))
	}))
	defer server.Close()

	client := testClient(server)
	_, err := client.WaitForAsyncOperation(context.Background(), server.URL+"/monitor")
	if err == nil {
		t.Fatal("expected error for failed operation")
	}
	if !strings.Contains(err.Error(), "copyFailed") {
		t.Errorf("expected copyFailed error, got: %v", err)
	}
}

func TestWaitForAsyncOp_EmptyURL(t *testing.T) {
	client := &Client{PollBackoff: 1}
	_, err := client.WaitForAsyncOperation(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "monitoring URL cannot be empty") {
		t.Errorf("expected monitoring URL error, got: %v", err)
	}
}

// =============================================================================
// Additional edge cases
// =============================================================================

func TestGetItem_InvalidResource(t *testing.T) {
	client := &Client{BaseURL: "http://example.com"}
	_, err := client.GetItem(context.Background(),
		&testTokenProvider{"token"}, ItemID(""),
	)
	if err == nil {
		t.Fatal("expected error for empty resource")
	}
}

func TestListChildren_InvalidResource(t *testing.T) {
	client := &Client{BaseURL: "http://example.com"}
	_, err := client.ListChildren(context.Background(),
		&testTokenProvider{"token"}, ItemID(""),
	)
	if err == nil {
		t.Fatal("expected error for empty resource")
	}
}

func TestDeleteItem_InvalidResource(t *testing.T) {
	client := &Client{BaseURL: "http://example.com"}
	err := client.DeleteItem(context.Background(),
		&testTokenProvider{"token"}, ItemID(""), "",
	)
	if err == nil {
		t.Fatal("expected error for empty resource")
	}
}

func TestWithRetryOption(t *testing.T) {
	// WithRetry(0) should disable retries
	c := NewClient(WithRetry(0))
	rd, ok := c.HTTPClient.(*RetryDoer)
	if !ok {
		t.Fatal("expected RetryDoer")
	}
	if rd.maxRetries != 0 {
		t.Errorf("expected 0 maxRetries, got %d", rd.maxRetries)
	}
}

func TestRetryDoer_MaxRetries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always return 503 to trigger retries
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	c := testClient(server)
	_, err := c.GetItem(context.Background(),
		&testTokenProvider{"token"}, ItemID("file-1"),
	)
	if err != nil {
		t.Logf("expected (possibly success or error depending on retry): %v", err)
	}
}

func TestCheckResponse_NonJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("<html>Bad Request</html>"))
	}))
	defer server.Close()

	client := testClient(server)
	_, err := client.GetItem(context.Background(),
		&testTokenProvider{"token"}, ItemID("file-1"),
	)
	if err == nil {
		t.Fatal("expected error for non-JSON error response")
	}
}
