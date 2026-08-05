package graph

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// ExampleClient_ListChildren demonstrates how to list the contents of a folder.
func ExampleClient_ListChildren() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"value": [
				{"id": "file1", "name": "documento.pdf", "size": 1024000},
				{"id": "folder1", "name": "Subcarpeta", "folder": {"childCount": 3}}
			]
		}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	items, err := client.ListChildren(context.Background(), tokenProvider, RootID)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	for _, item := range items {
		kind := "file"
		if item.IsFolder() {
			kind = "folder"
		}
		fmt.Printf("%s: %s\n", kind, item.Name)
	}

	// Output:
	// file: documento.pdf
	// folder: Subcarpeta
}

// TestClient_ListDriveRoot_Success tests the parsing of the file list
func TestClient_ListDriveRoot_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/me/drive/root/children" {
			t.Errorf("Expected path /me/drive/root/children, got %s", r.URL.Path)
		}

		// Verify that the token was sent correctly
		auth := r.Header.Get("Authorization")
		if auth != "Bearer fake_token" {
			t.Errorf("Expected token 'Bearer fake_token', got '%s'", auth)
		}

		// We simulate a real OneDrive response with a folder and a file
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"value": [
				{
					"id": "folder1", 
					"name": "Documents", 
					"folder": {"childCount": 5},
					"lastModifiedDateTime": "2024-01-01T00:00:00Z"
				},
				{
					"id": "file1", 
					"name": "photo.jpg", 
					"size": 2048576,
					"file": {"mimeType": "image/jpeg"}
				}
			]
		}`))
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	// Mock TokenProvider for the test
	tokenProvider := &mockTokenProvider{token: "fake_token"}

	items, err := client.ListDriveRoot(context.Background(), tokenProvider)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("Expected 2 items, got %d", len(items))
	}

	// We validate the folder using the IsDir() method
	if !items[0].IsFolder() {
		t.Error("The first item should be detected as a folder")
	}
	if items[0].Name != "Documents" {
		t.Errorf("Incorrect folder name: %s", items[0].Name)
	}

	// We validate the file
	if items[1].IsFolder() {
		t.Error("The second item should not be a folder")
	}
	if items[1].Size != 2048576 {
		t.Errorf("Incorrect file size: %d", items[1].Size)
	}
}

// TestClient_ListChildren_Success tests listing the children of a specific folder
func TestClient_ListChildren_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedPath := "/me/drive/items/folder123/children"
		if r.URL.Path != expectedPath {
			t.Errorf("Expected path %s, got %s", expectedPath, r.URL.Path)
		}

		// Verify authentication
		if r.Header.Get("Authorization") != "Bearer test_token" {
			t.Error("Token was not sent correctly")
		}

		// Response with the folder content
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"value": [
				{
					"id": "file1",
					"name": "documento.pdf",
					"size": 1024000,
					"file": {"mimeType": "application/pdf"}
				},
				{
					"id": "subfolder1",
					"name": "Subcarpeta",
					"folder": {"childCount": 3}
				}
			]
		}`))
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	tokenProvider := &mockTokenProvider{token: "test_token"}

	items, err := client.ListChildren(context.Background(), tokenProvider, ItemID("folder123"))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("Expected 2 items, got %d", len(items))
	}

	// Validate file
	if items[0].IsFolder() {
		t.Error("The first item should be a file")
	}
	if items[0].Name != "documento.pdf" {
		t.Errorf("Incorrect name: %s", items[0].Name)
	}

	// Validate folder
	if !items[1].IsFolder() {
		t.Error("The second item should be a folder")
	}
	if items[1].Folder.ChildCount != 3 {
		t.Errorf("Incorrect child count: %d", items[1].Folder.ChildCount)
	}
}

// TestClient_ListChildren_Pagination tests that pagination works correctly
func TestClient_ListChildren_Pagination(t *testing.T) {
	pageCount := 0
	var serverURL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageCount++
		w.Header().Set("Content-Type", "application/json")

		// First page with nextLink
		if pageCount == 1 {
			// nextLink must be a full URL (that is how Graph returns it)
			nextLink := serverURL + "/me/drive/items/test_folder/children?page=2"
			w.Write([]byte(`{
				"value": [
					{"id": "item1", "name": "Archivo1.txt", "size": 100}
				],
				"@odata.nextLink": "` + nextLink + `"
			}`))
			return
		}

		// Second page without nextLink (last)
		w.Write([]byte(`{
			"value": [
				{"id": "item2", "name": "Archivo2.txt", "size": 200}
			]
		}`))
	}))
	defer server.Close()

	// Capture the server URL to use it in the handler
	serverURL = server.URL

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	tokenProvider := &mockTokenProvider{token: "test_token"}

	items, err := client.ListChildren(context.Background(), tokenProvider, ItemPath("test_folder"))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify that both pages were processed
	if pageCount != 2 {
		t.Errorf("Expected 2 requests (pagination), got %d", pageCount)
	}

	// Verify that all items from both pages were obtained
	if len(items) != 2 {
		t.Fatalf("Expected 2 items (1 from each page), got %d", len(items))
	}

	if items[0].Name != "Archivo1.txt" {
		t.Errorf("First item incorrect: %s", items[0].Name)
	}
	if items[1].Name != "Archivo2.txt" {
		t.Errorf("Second item incorrect: %s", items[1].Name)
	}
}

// TestClient_ListChildren_ContextCanceledDuringPagination verifies that
// cancelling the context during pagination returns context.Canceled
// and does not keep making unnecessary requests.
func TestClient_ListChildren_ContextCanceledDuringPagination(t *testing.T) {
	var pageCount atomic.Int32
	var serverURL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageCount.Add(1)
		w.Header().Set("Content-Type", "application/json")

		// We always return nextLink (infinite loop) to test cancellation
		currentPage := pageCount.Load()
		nextLink := serverURL + fmt.Sprintf("/children?page=%d", currentPage+1)
		w.Write(fmt.Appendf(nil, `{
			"value": [{"id": "item%d", "name": "Archivo%d.txt", "size": 100}],
			"@odata.nextLink": "%s"
		}`, currentPage, currentPage, nextLink))
	}))
	defer server.Close()
	serverURL = server.URL

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a small delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := client.ListChildren(ctx, &mockTokenProvider{token: "test_token"}, ItemPath("folder"))
	if err == nil {
		t.Fatal("Expected error from canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled, got: %v", err)
	}

	// Cancellation should stop pagination within a few pages
	// (the server is local, so it can process many within 50ms)
	if pageCount.Load() > 2000 {
		t.Errorf("Pagination did not stop in time: %d pages", pageCount.Load())
	}
}
