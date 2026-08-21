package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/spf13/cobra"
)

func setupGraphCommand(t *testing.T, name string, handler http.Handler) (*cobra.Command, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	setupManager(t, "test@outlook.com")
	cmd := findSubcommand(rootCmd, name)
	if cmd == nil {
		t.Fatalf("command %q not found", name)
	}
	cmd.SetContext(context.Background())
	rootCmd.PersistentPreRun(cmd, nil)
	cmd.SetContext(contextWithClient(cmd.Context(), graph.NewClient(
		graph.WithBaseURL(server.URL),
		graph.WithHTTPClient(server.Client()),
		graph.WithRetry(0),
	)))
	resetFlagValues(cmd)
	return cmd, server
}

func setCommandFlags(t *testing.T, cmd *cobra.Command, values map[string]string) {
	t.Helper()
	for name, value := range values {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatalf("set --%s: %v", name, err)
		}
	}
}

func TestItemCommands_SuccessPaths(t *testing.T) {
	t.Run("mkdir", func(t *testing.T) {
		var request *http.Request
		cmd, server := setupGraphCommand(t, "mkdir", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			request = r
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(graph.DriveItem{ID: "folder-id", Name: "Photos", Folder: &graph.Folder{}})
		}))
		defer server.Close()
		setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com", "id": "root", "name": "Photos"})
		output := captureStdoutForTest(t, func() error { return cmd.RunE(cmd, nil) })
		if request.Method != http.MethodPost || request.URL.Path != "/me/drive/root/children" {
			t.Fatalf("request = %v %s, want POST /me/drive/root/children", request.Method, request.URL.Path)
		}
		if !strings.Contains(output, "Folder created: Photos (ID: folder-id)") {
			t.Errorf("output = %q", output)
		}
	})

	t.Run("rename", func(t *testing.T) {
		var request *http.Request
		cmd, server := setupGraphCommand(t, "rename", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			request = r
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(graph.DriveItem{ID: "item-id", Name: "new.txt"})
		}))
		defer server.Close()
		setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com", "id": "item-id", "name": "new.txt", "etag": "\"v1\""})
		output := captureStdoutForTest(t, func() error { return cmd.RunE(cmd, nil) })
		if request.Method != http.MethodPatch || request.Header.Get("If-Match") != "\"v1\"" {
			t.Fatalf("request = %v, If-Match=%q, want PATCH with etag", request.Method, request.Header.Get("If-Match"))
		}
		if !strings.Contains(output, "Item renamed to: new.txt (ID: item-id)") {
			t.Errorf("output = %q", output)
		}
	})

	t.Run("list", func(t *testing.T) {
		var request *http.Request
		cmd, server := setupGraphCommand(t, "list", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			request = r
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"value": []graph.DriveItem{{ID: "file-id", Name: "notes.txt", Size: 7}},
			})
		}))
		defer server.Close()
		setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com"})
		output := captureStdoutForTest(t, func() error { return cmd.RunE(cmd, nil) })
		if request.Method != http.MethodGet || request.URL.Path != "/me/drive/root/children" {
			t.Fatalf("request = %v %s, want GET /me/drive/root/children", request.Method, request.URL.Path)
		}
		if !strings.Contains(output, "notes.txt") {
			t.Errorf("output = %q", output)
		}
	})

	t.Run("info", func(t *testing.T) {
		var request *http.Request
		cmd, server := setupGraphCommand(t, "info", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			request = r
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(graph.DriveItem{ID: "item-id", Name: "notes.txt", Size: 7})
		}))
		defer server.Close()
		setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com", "id": "item-id"})
		output := captureStdoutForTest(t, func() error { return cmd.RunE(cmd, nil) })
		if request.Method != http.MethodGet || request.URL.Path != "/me/drive/items/item-id" {
			t.Fatalf("request = %v %s, want GET /me/drive/items/item-id", request.Method, request.URL.Path)
		}
		if !strings.Contains(output, "notes.txt") {
			t.Errorf("output = %q", output)
		}
	})

	t.Run("rm", func(t *testing.T) {
		var request *http.Request
		cmd, server := setupGraphCommand(t, "rm", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			request = r
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()
		setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com", "id": "item-id", "force": "true", "etag": "\"v2\""})
		output := captureStdoutForTest(t, func() error { return cmd.RunE(cmd, nil) })
		if request.Method != http.MethodDelete || request.Header.Get("If-Match") != "\"v2\"" {
			t.Fatalf("request = %v, If-Match=%q, want DELETE with etag", request.Method, request.Header.Get("If-Match"))
		}
		if !strings.Contains(output, "successfully deleted") {
			t.Errorf("output = %q", output)
		}
	})

	t.Run("mv", func(t *testing.T) {
		var request *http.Request
		cmd, server := setupGraphCommand(t, "mv", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			request = r
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(graph.DriveItem{ID: "item-id", Name: "moved.txt"})
		}))
		defer server.Close()
		setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com", "id": "item-id", "dest-id": "folder-id", "etag": "\"v3\""})
		output := captureStdoutForTest(t, func() error { return cmd.RunE(cmd, nil) })
		if request.Method != http.MethodPatch || request.Header.Get("If-Match") != "\"v3\"" {
			t.Fatalf("request = %v, If-Match=%q, want PATCH with etag", request.Method, request.Header.Get("If-Match"))
		}
		if !strings.Contains(output, "moved successfully") {
			t.Errorf("output = %q", output)
		}
	})

	t.Run("copy", func(t *testing.T) {
		var request *http.Request
		cmd, server := setupGraphCommand(t, "copy", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			request = r
			w.Header().Set("Location", "https://example.test/monitor/1")
			w.WriteHeader(http.StatusAccepted)
		}))
		defer server.Close()
		setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com", "id": "item-id", "name": "copy.txt", "dest-id": "folder-id"})
		output := captureStdoutForTest(t, func() error { return cmd.RunE(cmd, nil) })
		if request.Method != http.MethodPost || request.URL.Path != "/me/drive/items/item-id/copy" {
			t.Fatalf("request = %v %s, want POST copy endpoint", request.Method, request.URL.Path)
		}
		if !strings.Contains(output, "https://example.test/monitor/1") {
			t.Errorf("output = %q", output)
		}
	})

	t.Run("upload", func(t *testing.T) {
		var request *http.Request
		localFile := filepath.Join(t.TempDir(), "notes.txt")
		if err := os.WriteFile(localFile, []byte("upload me"), 0600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		cmd, server := setupGraphCommand(t, "upload", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			request = r
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(graph.DriveItem{ID: "uploaded-id", Name: "notes.txt", Size: 9})
		}))
		defer server.Close()
		setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com", "id": "folder-id", "file": localFile})
		output := captureStdoutForTest(t, func() error { return cmd.RunE(cmd, nil) })
		if request.Method != http.MethodPut || request.URL.Path != "/me/drive/items/folder-id:/notes.txt:/content" {
			t.Fatalf("request = %v %s, want PUT upload endpoint", request.Method, request.URL.Path)
		}
		if !strings.Contains(output, "File uploaded: notes.txt (ID: uploaded-id, 9 bytes)") {
			t.Errorf("output = %q", output)
		}
	})

	t.Run("download", func(t *testing.T) {
		var paths []string
		cmd, server := setupGraphCommand(t, "download", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			paths = append(paths, r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/me/drive/items/item-id" {
				_ = json.NewEncoder(w).Encode(graph.DriveItem{ID: "item-id", Name: "notes.txt", Size: 5})
				return
			}
			w.Header().Set("Content-Range", "bytes 0-4/5")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("hello"))
		}))
		defer server.Close()
		outputPath := filepath.Join(t.TempDir(), "downloaded.txt")
		setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com", "id": "item-id", "output": outputPath})
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatalf("download: %v", err)
		}
		data, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("ReadFile(download): %v", err)
		}
		if string(data) != "hello" {
			t.Errorf("downloaded data = %q, want hello", data)
		}
		if len(paths) != 2 || paths[0] != "/me/drive/items/item-id" || paths[1] != "/me/drive/items/item-id/content" {
			t.Errorf("request paths = %#v", paths)
		}
	})
}

func TestMkdirCmd_GraphError(t *testing.T) {
	cmd, server := setupGraphCommand(t, "mkdir", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "folder creation failed", http.StatusBadRequest)
	}))
	defer server.Close()
	setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com", "id": "root", "name": "Photos"})
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "error creating folder:") {
		t.Fatalf("error = %v, want folder creation context", err)
	}
}

func TestListCmd_GraphError(t *testing.T) {
	cmd, server := setupGraphCommand(t, "list", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "list failed", http.StatusInternalServerError)
	}))
	defer server.Close()
	setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com"})
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "error listing files:") {
		t.Fatalf("error = %v, want listing context", err)
	}
}
