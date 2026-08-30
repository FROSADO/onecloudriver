package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/frosado/onecloudriver/internal/graph"
)

// jsonChildren encodes a list of DriveItems as a Graph page, the shape
// ListDriveRoot/ListChildren expect ({"value": [...]}).
func jsonChildren(items []graph.DriveItem) map[string]any {
	return map[string]any{"value": items}
}

// TestListCmd_NoFlags_ListsRoot is the regression guard for the current
// behavior: with no --id/--path the command lists the drive root.
func TestListCmd_NoFlags_ListsRoot(t *testing.T) {
	var paths []string
	cmd, server := setupGraphCommand(t, "list", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jsonChildren([]graph.DriveItem{{ID: "file-id", Name: "notes.txt", Size: 7}}))
	}))
	defer server.Close()
	setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com"})
	output := captureStdoutForTest(t, func() error { return cmd.RunE(cmd, nil) })
	if len(paths) != 1 || paths[0] != "/me/drive/root/children" {
		t.Fatalf("paths = %#v, want [GET /me/drive/root/children]", paths)
	}
	if !strings.Contains(output, "notes.txt") {
		t.Errorf("output = %q, want notes.txt", output)
	}
}

// TestListCmd_ById_ListsFolder verifies the two-step resolution for a folder
// addressed by ID: GetItem first, then ListChildren by the item's own ID.
func TestListCmd_ById_ListsFolder(t *testing.T) {
	var paths []string
	cmd, server := setupGraphCommand(t, "list", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/me/drive/items/folder-id":
			_ = json.NewEncoder(w).Encode(graph.DriveItem{ID: "folder-id", Name: "Photos", Folder: &graph.Folder{}})
		case "/me/drive/items/folder-id/children":
			_ = json.NewEncoder(w).Encode(jsonChildren([]graph.DriveItem{{ID: "file-id", Name: "photo.jpg", Size: 7}}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com", "id": "folder-id"})
	output := captureStdoutForTest(t, func() error { return cmd.RunE(cmd, nil) })
	want := []string{"/me/drive/items/folder-id", "/me/drive/items/folder-id/children"}
	if len(paths) != 2 || paths[0] != want[0] || paths[1] != want[1] {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
	if !strings.Contains(output, "photo.jpg") {
		t.Errorf("output = %q, want photo.jpg", output)
	}
}

// TestListCmd_ByPath_ListsFolder verifies folder resolution by path: GetItem
// hits /me/drive/root:/<path> and ListChildren then goes by the item's ID.
func TestListCmd_ByPath_ListsFolder(t *testing.T) {
	var paths []string
	cmd, server := setupGraphCommand(t, "list", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/me/drive/root:/Documents":
			_ = json.NewEncoder(w).Encode(graph.DriveItem{ID: "folder-id", Name: "Documents", Folder: &graph.Folder{}})
		case "/me/drive/items/folder-id/children":
			_ = json.NewEncoder(w).Encode(jsonChildren([]graph.DriveItem{{ID: "doc-id", Name: "doc.txt", Size: 3}}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com", "path": "/Documents"})
	output := captureStdoutForTest(t, func() error { return cmd.RunE(cmd, nil) })
	want := []string{"/me/drive/root:/Documents", "/me/drive/items/folder-id/children"}
	if len(paths) != 2 || paths[0] != want[0] || paths[1] != want[1] {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
	if !strings.Contains(output, "doc.txt") {
		t.Errorf("output = %q, want doc.txt", output)
	}
}

// TestListCmd_FileTarget_ListsParentFolder verifies that targeting a file
// lists the folder that contains it, via the item's parentReference.
func TestListCmd_FileTarget_ListsParentFolder(t *testing.T) {
	var paths []string
	cmd, server := setupGraphCommand(t, "list", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/me/drive/items/file-id":
			// A file: no Folder field, but a Parent reference.
			_ = json.NewEncoder(w).Encode(graph.DriveItem{
				ID: "file-id", Name: "notes.txt", Size: 7,
				Parent: &graph.DriveItemParent{ID: "parent-id"},
			})
		case "/me/drive/items/parent-id/children":
			_ = json.NewEncoder(w).Encode(jsonChildren([]graph.DriveItem{{ID: "sibling-id", Name: "sibling.txt", Size: 3}}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com", "id": "file-id"})
	output := captureStdoutForTest(t, func() error { return cmd.RunE(cmd, nil) })
	want := []string{"/me/drive/items/file-id", "/me/drive/items/parent-id/children"}
	if len(paths) != 2 || paths[0] != want[0] || paths[1] != want[1] {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
	if !strings.Contains(output, "sibling.txt") {
		t.Errorf("output = %q, want sibling.txt", output)
	}
}

// TestListCmd_ExplicitRootPath verifies that --path / resolves to the root
// folder and lists its children.
func TestListCmd_ExplicitRootPath(t *testing.T) {
	var paths []string
	cmd, server := setupGraphCommand(t, "list", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/me/drive/root":
			_ = json.NewEncoder(w).Encode(graph.DriveItem{ID: "root-folder-id", Name: "root", Folder: &graph.Folder{}})
		case "/me/drive/items/root-folder-id/children":
			_ = json.NewEncoder(w).Encode(jsonChildren([]graph.DriveItem{{ID: "file-id", Name: "top.txt", Size: 1}}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com", "path": "/"})
	output := captureStdoutForTest(t, func() error { return cmd.RunE(cmd, nil) })
	want := []string{"/me/drive/root", "/me/drive/items/root-folder-id/children"}
	if len(paths) != 2 || paths[0] != want[0] || paths[1] != want[1] {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
	if !strings.Contains(output, "top.txt") {
		t.Errorf("output = %q, want top.txt", output)
	}
}

// TestListCmd_EmptyRoot verifies the root-specific empty message.
func TestListCmd_EmptyRoot(t *testing.T) {
	cmd, server := setupGraphCommand(t, "list", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jsonChildren(nil))
	}))
	defer server.Close()
	setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com"})
	output := captureStdoutForTest(t, func() error { return cmd.RunE(cmd, nil) })
	if !strings.Contains(output, "The root folder is empty.") {
		t.Errorf("output = %q, want 'The root folder is empty.'", output)
	}
}

// TestListCmd_EmptyFolder verifies the targeted-folder empty message.
func TestListCmd_EmptyFolder(t *testing.T) {
	cmd, server := setupGraphCommand(t, "list", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/me/drive/items/folder-id":
			_ = json.NewEncoder(w).Encode(graph.DriveItem{ID: "folder-id", Name: "Photos", Folder: &graph.Folder{}})
		case "/me/drive/items/folder-id/children":
			_ = json.NewEncoder(w).Encode(jsonChildren(nil))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com", "id": "folder-id"})
	output := captureStdoutForTest(t, func() error { return cmd.RunE(cmd, nil) })
	if !strings.Contains(output, "The folder is empty.") {
		t.Errorf("output = %q, want 'The folder is empty.'", output)
	}
}

// TestListCmd_FileTargetWithoutParent verifies the error path when a file
// target has no parentReference: we cannot list a containing folder.
func TestListCmd_FileTargetWithoutParent(t *testing.T) {
	cmd, server := setupGraphCommand(t, "list", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// A file with no Parent field at all.
		_ = json.NewEncoder(w).Encode(graph.DriveItem{ID: "file-id", Name: "notes.txt", Size: 7})
	}))
	defer server.Close()
	setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com", "id": "file-id"})
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot determine the parent folder") {
		t.Fatalf("error = %v, want 'cannot determine the parent folder'", err)
	}
}

// TestListCmd_GetItemError verifies that a Graph error while resolving the
// target folder propagates with the listing context (folder path, not root).
func TestListCmd_GetItemError(t *testing.T) {
	cmd, server := setupGraphCommand(t, "list", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "get item failed", http.StatusInternalServerError)
	}))
	defer server.Close()
	setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com", "id": "folder-id"})
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "error listing files:") {
		t.Fatalf("error = %v, want listing context", err)
	}
}

// TestListCmd_BothIDAndPathError verifies the mutual-exclusion validation
// fails before any Graph request is made.
func TestListCmd_BothIDAndPathError(t *testing.T) {
	setupManager(t, "test@outlook.com")
	err := execCmd("list", "--account", "test@outlook.com", "--id", "folder-id", "--path", "/Documents")
	if err == nil {
		t.Fatal("expected error when both --id and --path are specified")
	}
	if !strings.Contains(err.Error(), "exactly one of --id or --path") {
		t.Errorf("expected 'exactly one of --id or --path', got: %v", err)
	}
}
