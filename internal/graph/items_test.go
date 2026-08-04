package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ExampleClient_GetItem demonstrates how to get a DriveItem from OneDrive
// usando un servidor httptest para simular Microsoft Graph.
func ExampleClient_GetItem() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "item123", "name": "documento.pdf", "size": 2048000}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	item, err := client.GetItem(context.Background(), tokenProvider, ItemID("item123"))
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println(item.Name)
	fmt.Println(item.Size)

	// Output:
	// documento.pdf
	// 2048000
}

// ExampleClient_CreateFolder demonstrates how to create a folder in OneDrive.
func ExampleClient_CreateFolder() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "newfolder", "name": "Nueva Carpeta", "folder": {"childCount": 0}}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	folder, err := client.CreateFolder(context.Background(), tokenProvider, RootID, "Nueva Carpeta")
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println(folder.Name)
	fmt.Println(folder.IsFolder())

	// Output:
	// Nueva Carpeta
	// true
}

// ExampleDriveItem_IsFolder demonstrates how to distinguish between files and folders.
func ExampleDriveItem_IsFolder() {
	folder := &DriveItem{Name: "Documentos", Folder: &Folder{ChildCount: 5}}
	file := &DriveItem{Name: "foto.jpg", File: &File{}}

	fmt.Println(folder.Name, folder.IsFolder())
	fmt.Println(file.Name, file.IsFolder())

	// Output:
	// Documentos true
	// foto.jpg false
}

// TestClient_GetItem_Success tests getting a DriveItem by ID and by path.
func TestClient_GetItem_Success(t *testing.T) {
	tests := []struct {
		name         string
		resource     Resource
		expectedPath string
		expectedID   string
		expectedName string
		expectedSize uint64
	}{
		{
			name:         "by ID",
			resource:     ItemID("item123"),
			expectedPath: "/me/drive/items/item123",
			expectedID:   "item123",
			expectedName: "documento.pdf",
			expectedSize: 2048000,
		},
		{
			name:         "by Path",
			resource:     ItemPath("/Documentos/foto.jpg"),
			expectedPath: "/me/drive/root:/Documentos/foto.jpg",
			expectedID:   "file456",
			expectedName: "foto.jpg",
			expectedSize: 1048576,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.expectedPath {
					t.Errorf("Ruta esperada %s, obtenida %s", tt.expectedPath, r.URL.Path)
				}

				if r.Header.Get("Authorization") != "Bearer test_token" {
					t.Error("Token no enviado correctamente")
				}

				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"id": %q, "name": %q, "size": %d}`, tt.expectedID, tt.expectedName, tt.expectedSize)
			}))
			defer server.Close()

			client := &Client{
				BaseURL:    server.URL,
				HTTPClient: server.Client(),
			}

			tokenProvider := &mockTokenProvider{token: "test_token"}

			item, err := client.GetItem(context.Background(), tokenProvider, tt.resource)
			if err != nil {
				t.Fatalf("Error inesperado: %v", err)
			}

			if item.ID != tt.expectedID {
				t.Errorf("ID incorrecto: %s", item.ID)
			}
			if item.Name != tt.expectedName {
				t.Errorf("Nombre incorrecto: %s", item.Name)
			}
			if item.Size != tt.expectedSize {
				t.Errorf("Incorrect size: expected %d, got %d", tt.expectedSize, item.Size)
			}
			if item.IsFolder() {
				t.Error("The item should not be a folder")
			}
		})
	}
}

// TestClient_GetItem_NotFound prueba el manejo de un 404 de Graph
func TestClient_GetItem_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{
			"error": {
				"code": "itemNotFound",
				"message": "The resource could not be found."
			}
		}`))
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	tokenProvider := &mockTokenProvider{token: "test_token"}

	_, err := client.GetItem(context.Background(), tokenProvider, ItemID("nonexistent"))
	if err == nil {
		t.Fatal("Se esperaba un error 404, pero se obtuvo nil")
	}
	if !errors.Is(err, ErrItemNotFound) {
		t.Errorf("Error esperado ErrItemNotFound, obtenido: %v", err)
	}
}

// TestClient_EmptyResource verifies that all methods reject an empty Resource
// con el mensaje de error esperado.
func TestClient_EmptyResource(t *testing.T) {
	tokenProvider := &mockTokenProvider{token: "test_token"}

	tests := []struct {
		name string
		call func(c *Client) error
	}{
		{
			name: "ListChildren",
			call: func(c *Client) error {
				_, err := c.ListChildren(context.Background(), tokenProvider, ItemPath(""))
				return err
			},
		},
		{
			name: "GetItem by ID",
			call: func(c *Client) error {
				_, err := c.GetItem(context.Background(), tokenProvider, ItemID(""))
				return err
			},
		},
		{
			name: "GetItem by Path",
			call: func(c *Client) error {
				_, err := c.GetItem(context.Background(), tokenProvider, ItemPath(""))
				return err
			},
		},
		{
			name: "GetItemContent",
			call: func(c *Client) error {
				_, err := c.GetItemContent(context.Background(), tokenProvider, ItemID(""))
				return err
			},
		},
		{
			name: "GetItemContentStream",
			call: func(c *Client) error {
				var buf bytes.Buffer
				_, err := c.GetItemContentStream(context.Background(), tokenProvider, ItemID(""), &buf)
				return err
			},
		},
		{
			name: "CreateFolder",
			call: func(c *Client) error {
				_, err := c.CreateFolder(context.Background(), tokenProvider, ItemID(""), "test")
				return err
			},
		},
		{
			name: "DeleteItem",
			call: func(c *Client) error {
				return c.DeleteItem(context.Background(), tokenProvider, ItemID(""), "")
			},
		},
		{
			name: "RenameItem",
			call: func(c *Client) error {
				_, err := c.RenameItem(context.Background(), tokenProvider, ItemID(""), "new", "")
				return err
			},
		},
		{
			name: "MoveItem",
			call: func(c *Client) error {
				_, err := c.MoveItem(context.Background(), tokenProvider, ItemID(""), ItemID("dest"), "")
				return err
			},
		},
		{
			name: "MoveItem (newParent empty)",
			call: func(c *Client) error {
				_, err := c.MoveItem(context.Background(), tokenProvider, ItemID("src"), ItemID(""), "")
				return err
			},
		},
		{
			name: "CopyItem",
			call: func(c *Client) error {
				_, err := c.CopyItem(context.Background(), tokenProvider, ItemID(""), "copia", nil)
				return err
			},
		},
		{
			name: "UploadItem",
			call: func(c *Client) error {
				_, err := c.UploadItem(context.Background(), tokenProvider, ItemID(""), "test.txt", strings.NewReader("x"))
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(NewClient())
			if err == nil {
				t.Fatal("An error was expected with an empty resource")
			}
			if !errors.Is(err, ErrEmptyResource) {
				t.Errorf("Error esperado ErrEmptyResource, obtenido: %v", err)
			}
		})
	}
}

// TestClient_GetItemByPath_NoLeadingSlash tests that the method adds "/" if missing
func TestClient_GetItem_ByPath_NoLeadingSlash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedPath := "/me/drive/root:/Documentos/foto.jpg"
		if r.URL.Path != expectedPath {
			t.Errorf("Ruta esperada %s, obtenida %s", expectedPath, r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id": "file456",
			"name": "foto.jpg",
			"size": 1048576
		}`))
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	tokenProvider := &mockTokenProvider{token: "test_token"}

	// Pasamos el path sin barra inicial
	item, err := client.GetItem(context.Background(), tokenProvider, ItemPath("Documentos/foto.jpg"))
	if err != nil {
		t.Fatalf("Error inesperado: %v", err)
	}

	if item.ID != "file456" {
		t.Errorf("ID incorrecto: %s", item.ID)
	}
}

// TestClient_CreateFolder_Success tests folder creation by ID and by path.
func TestClient_CreateFolder_Success(t *testing.T) {
	tests := []struct {
		name         string
		parent       Resource
		expectedPath string
		folderName   string
	}{
		{
			name:         "by ID",
			parent:       ItemID("folder123"),
			expectedPath: "/me/drive/items/folder123/children",
			folderName:   "Nueva Carpeta",
		},
		{
			name:         "by Path",
			parent:       ItemPath("/Documentos"),
			expectedPath: "/me/drive/root:/Documentos:/children",
			folderName:   "Subcarpeta",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("Expected method POST, got %s", r.Method)
				}
				if r.URL.Path != tt.expectedPath {
					t.Errorf("Ruta esperada %s, obtenida %s", tt.expectedPath, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test_token" {
					t.Error("Token no enviado correctamente")
				}

				// Verificar el body
				var body map[string]interface{}
				json.NewDecoder(r.Body).Decode(&body)
				if body["name"] != tt.folderName {
					t.Errorf("Nombre esperado %q, obtenido %v", tt.folderName, body["name"])
				}
				if _, ok := body["folder"]; !ok {
					t.Error("El body debe contener el campo 'folder'")
				}

				w.WriteHeader(http.StatusCreated)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{
					"id":   "newfolder",
					"name": tt.folderName,
					"folder": map[string]interface{}{
						"childCount": 0,
					},
				})
			}))
			defer server.Close()

			client := &Client{
				BaseURL:    server.URL,
				HTTPClient: server.Client(),
			}

			tokenProvider := &mockTokenProvider{token: "test_token"}

			folder, err := client.CreateFolder(context.Background(), tokenProvider, tt.parent, tt.folderName)
			if err != nil {
				t.Fatalf("Error inesperado: %v", err)
			}

			if folder.Name != tt.folderName {
				t.Errorf("Nombre incorrecto: %s", folder.Name)
			}
			if !folder.IsFolder() {
				t.Error("The created item should be a folder")
			}
		})
	}
}

// TestClient_DeleteItem_Success tests deleting items by ID and by path.
func TestClient_DeleteItem_Success(t *testing.T) {
	tests := []struct {
		name         string
		resource     Resource
		expectedPath string
	}{
		{
			name:         "by ID",
			resource:     ItemID("file123"),
			expectedPath: "/me/drive/items/file123",
		},
		{
			name:         "by Path",
			resource:     ItemPath("/Docs/archivo.txt"),
			expectedPath: "/me/drive/root:/Docs/archivo.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete {
					t.Errorf("Expected method DELETE, got %s", r.Method)
				}
				if r.URL.Path != tt.expectedPath {
					t.Errorf("Ruta esperada %s, obtenida %s", tt.expectedPath, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test_token" {
					t.Error("Token no enviado correctamente")
				}

				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()

			client := &Client{
				BaseURL:    server.URL,
				HTTPClient: server.Client(),
			}

			tokenProvider := &mockTokenProvider{token: "test_token"}

			err := client.DeleteItem(context.Background(), tokenProvider, tt.resource, "")
			if err != nil {
				t.Fatalf("Error inesperado: %v", err)
			}
		})
	}
}

// TestClient_RenameItem_Success prueba el renombrado de items por ID y por ruta.
func TestClient_RenameItem_Success(t *testing.T) {
	tests := []struct {
		name         string
		resource     Resource
		expectedPath string
		newName      string
	}{
		{
			name:         "by ID",
			resource:     ItemID("file123"),
			expectedPath: "/me/drive/items/file123",
			newName:      "renombrado.pdf",
		},
		{
			name:         "by Path",
			resource:     ItemPath("/Docs/viejo.txt"),
			expectedPath: "/me/drive/root:/Docs/viejo.txt",
			newName:      "nuevo.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPatch {
					t.Errorf("Expected method PATCH, got %s", r.Method)
				}
				if r.URL.Path != tt.expectedPath {
					t.Errorf("Ruta esperada %s, obtenida %s", tt.expectedPath, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test_token" {
					t.Error("Token no enviado correctamente")
				}

				// Verificar el body
				var body map[string]interface{}
				json.NewDecoder(r.Body).Decode(&body)
				if body["name"] != tt.newName {
					t.Errorf("Nombre esperado %q, obtenido %v", tt.newName, body["name"])
				}

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{
					"id":   "file123",
					"name": tt.newName,
					"size": 1024,
				})
			}))
			defer server.Close()

			client := &Client{
				BaseURL:    server.URL,
				HTTPClient: server.Client(),
			}

			tokenProvider := &mockTokenProvider{token: "test_token"}

			item, err := client.RenameItem(context.Background(), tokenProvider, tt.resource, tt.newName, "")
			if err != nil {
				t.Fatalf("Error inesperado: %v", err)
			}

			if item.Name != tt.newName {
				t.Errorf("Nombre incorrecto: esperado %q, obtenido %q", tt.newName, item.Name)
			}
		})
	}
}

// TestClient_EmptyName verifies that CreateFolder and RenameItem reject an empty name.
func TestClient_EmptyName(t *testing.T) {
	tokenProvider := &mockTokenProvider{token: "test_token"}

	tests := []struct {
		name string
		call func(c *Client) error
	}{
		{
			name: "CreateFolder",
			call: func(c *Client) error {
				_, err := c.CreateFolder(context.Background(), tokenProvider, ItemID("folder123"), "")
				return err
			},
		},
		{
			name: "RenameItem",
			call: func(c *Client) error {
				_, err := c.RenameItem(context.Background(), tokenProvider, ItemID("file123"), "", "")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(NewClient())
			if err == nil {
				t.Fatal("An error was expected with an empty name")
			}
		})
	}
}

// TestClient_MoveItem_Success prueba el movimiento de items con destino por ID y por ruta.
func TestClient_MoveItem_Success(t *testing.T) {
	tests := []struct {
		name              string
		item              Resource
		expectedItemPath  string
		newParent         Resource
		expectedParentKey string
		expectedParentVal string
	}{
		{
			name:              "by ID to ID",
			item:              ItemID("file123"),
			expectedItemPath:  "/me/drive/items/file123",
			newParent:         ItemID("folder456"),
			expectedParentKey: "id",
			expectedParentVal: "folder456",
		},
		{
			name:              "by Path to Path",
			item:              ItemPath("/Docs/viejo.txt"),
			expectedItemPath:  "/me/drive/root:/Docs/viejo.txt",
			newParent:         ItemPath("/Archivo"),
			expectedParentKey: "path",
			expectedParentVal: "/Archivo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPatch {
					t.Errorf("Expected method PATCH, got %s", r.Method)
				}
				if r.URL.Path != tt.expectedItemPath {
					t.Errorf("Ruta esperada %s, obtenida %s", tt.expectedItemPath, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test_token" {
					t.Error("Token no enviado correctamente")
				}

				var body map[string]interface{}
				json.NewDecoder(r.Body).Decode(&body)
				parentRef, ok := body["parentReference"].(map[string]interface{})
				if !ok {
					t.Fatal("El body debe contener 'parentReference'")
				}
				if parentRef[tt.expectedParentKey] != tt.expectedParentVal {
					t.Errorf("parentReference.%s esperado %q, obtenido %v", tt.expectedParentKey, tt.expectedParentVal, parentRef[tt.expectedParentKey])
				}

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{
					"id":   "file123",
					"name": "viejo.txt",
					"parentReference": map[string]interface{}{
						tt.expectedParentKey: tt.expectedParentVal,
					},
				})
			}))
			defer server.Close()

			client := &Client{
				BaseURL:    server.URL,
				HTTPClient: server.Client(),
			}

			tokenProvider := &mockTokenProvider{token: "test_token"}

			moved, err := client.MoveItem(context.Background(), tokenProvider, tt.item, tt.newParent, "")
			if err != nil {
				t.Fatalf("Error inesperado: %v", err)
			}

			if moved.Parent == nil {
				t.Fatal("The moved item should have a parentReference")
			}
		})
	}
}

// TestClient_CopyItem_Success tests asynchronous copying of items by ID and by path.
func TestClient_CopyItem_Success(t *testing.T) {
	tests := []struct {
		name              string
		item              Resource
		expectedPath      string
		newName           string
		newParent         Resource
		expectedParentKey string
		expectedParentVal string
	}{
		{
			name:              "by ID",
			item:              ItemID("file123"),
			expectedPath:      "/me/drive/items/file123/copy",
			newName:           "copia.pdf",
			newParent:         ItemID("folder456"),
			expectedParentKey: "id",
			expectedParentVal: "folder456",
		},
		{
			name:              "by Path",
			item:              ItemPath("/Docs/original.txt"),
			expectedPath:      "/me/drive/root:/Docs/original.txt:/copy",
			newName:           "",
			newParent:         ItemPath("/Backup"),
			expectedParentKey: "path",
			expectedParentVal: "/Backup",
		},
		{
			name:              "name only (same folder)",
			item:              ItemID("file123"),
			expectedPath:      "/me/drive/items/file123/copy",
			newName:           "duplicado.pdf",
			newParent:         nil,
			expectedParentKey: "",
			expectedParentVal: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("Expected method POST, got %s", r.Method)
				}
				if r.URL.Path != tt.expectedPath {
					t.Errorf("Ruta esperada %s, obtenida %s", tt.expectedPath, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test_token" {
					t.Error("Token no enviado correctamente")
				}

				var body map[string]interface{}
				json.NewDecoder(r.Body).Decode(&body)

				if tt.newName != "" && body["name"] != tt.newName {
					t.Errorf("name esperado %q, obtenido %v", tt.newName, body["name"])
				}
				if tt.newName == "" && body["name"] != nil {
					t.Errorf("name should not be present, got %v", body["name"])
				}

				if tt.newParent != nil {
					parentRef, _ := body["parentReference"].(map[string]interface{})
					if parentRef[tt.expectedParentKey] != tt.expectedParentVal {
						t.Errorf("parentReference.%s esperado %q, obtenido %v", tt.expectedParentKey, tt.expectedParentVal, parentRef[tt.expectedParentKey])
					}
				} else if body["parentReference"] != nil {
					t.Errorf("parentReference should not be present")
				}

				w.Header().Set("Location", "https://graph.microsoft.com/v1.0/monitor/copy123")
				w.WriteHeader(http.StatusAccepted)
			}))
			defer server.Close()

			client := &Client{
				BaseURL:    server.URL,
				HTTPClient: server.Client(),
			}

			tokenProvider := &mockTokenProvider{token: "test_token"}

			monitorURL, err := client.CopyItem(context.Background(), tokenProvider, tt.item, tt.newName, tt.newParent)
			if err != nil {
				t.Fatalf("Error inesperado: %v", err)
			}

			if monitorURL == "" {
				t.Error("The monitor URL should not be empty")
			}
		})
	}
}

// TestClient_RenameItem_WithETag verifies that RenameItem sends the header
// If-Match when a non-empty etag is passed.
func TestClient_RenameItem_WithETag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("Expected method PATCH, got %s", r.Method)
		}
		if r.Header.Get("If-Match") != `"etag-value-123"` {
			t.Errorf("If-Match esperado %q, obtenido %q", `"etag-value-123"`, r.Header.Get("If-Match"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":   "file123",
			"name": "renombrado.pdf",
			"size": 1024,
		})
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	item, err := client.RenameItem(context.Background(), tokenProvider, ItemID("file123"), "renombrado.pdf", `"etag-value-123"`)
	if err != nil {
		t.Fatalf("Error inesperado: %v", err)
	}
	if item.Name != "renombrado.pdf" {
		t.Errorf("Nombre incorrecto: %s", item.Name)
	}
}

// TestClient_RenameItem_WithoutETag verifies that RenameItem does NOT send
// If-Match when the etag is empty.
func TestClient_RenameItem_WithoutETag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-Match") != "" {
			t.Errorf("If-Match header was not expected, but it was sent: %s", r.Header.Get("If-Match"))
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "file123", "name": "ok.txt", "size": 10}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	_, err := client.RenameItem(context.Background(), tokenProvider, ItemID("file123"), "ok.txt", "")
	if err != nil {
		t.Fatalf("Error inesperado: %v", err)
	}
}

// TestClient_MoveItem_WithETag verifies that MoveItem sends the If-Match header.
func TestClient_MoveItem_WithETag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-Match") != `"etag-move"` {
			t.Errorf("If-Match esperado %q, obtenido %q", `"etag-move"`, r.Header.Get("If-Match"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":              "file123",
			"name":            "viejo.txt",
			"parentReference": map[string]interface{}{"id": "dest"},
		})
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	moved, err := client.MoveItem(context.Background(), tokenProvider, ItemID("file123"), ItemID("dest"), `"etag-move"`)
	if err != nil {
		t.Fatalf("Error inesperado: %v", err)
	}
	if moved.Parent == nil {
		t.Fatal("The moved item should have a parentReference")
	}
}

// TestClient_DeleteItem_WithETag verifies that DeleteItem sends the If-Match header.
func TestClient_DeleteItem_WithETag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-Match") != `"etag-del"` {
			t.Errorf("If-Match esperado %q, obtenido %q", `"etag-del"`, r.Header.Get("If-Match"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	err := client.DeleteItem(context.Background(), tokenProvider, ItemID("file123"), `"etag-del"`)
	if err != nil {
		t.Fatalf("Error inesperado: %v", err)
	}
}

// TestClient_DeleteItem_WithoutETag verifies that DeleteItem does NOT send
// If-Match when the etag is empty.
func TestClient_DeleteItem_WithoutETag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-Match") != "" {
			t.Errorf("If-Match header was not expected, but it was sent: %s", r.Header.Get("If-Match"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	err := client.DeleteItem(context.Background(), tokenProvider, ItemID("file123"), "")
	if err != nil {
		t.Fatalf("Error inesperado: %v", err)
	}
}

// TestClient_MalformedGraphResponse verifica que las respuestas no-JSON
// de Graph se manejan correctamente como errores.
func TestClient_MalformedGraphResponse(t *testing.T) {
	// Probar con error 500 y respuesta no-JSON
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("<html>Internal Server Error</html>"))
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	_, err := client.GetItem(context.Background(), tokenProvider, ItemID("any"))
	if err == nil {
		t.Fatal("Se esperaba un error con respuesta no-JSON")
	}

	// Verificar que el mensaje contiene "respuesta no JSON"
	if !strings.Contains(err.Error(), "respuesta no JSON") &&
		!strings.Contains(err.Error(), "error parseando") {
		t.Errorf("Error inesperado: %v", err)
	}
}

// TestClient_CopyItem_PollMonitor_Success verifica el flujo completo de
// asynchronous copy: CopyItem → poll monitor URL → success with DriveItem.
//
// Simula el comportamiento real de Microsoft Graph:
//  1. POST /copy returns 202 Accepted + Location (monitor URL)
//  2. GET monitor URL (1ª llamada): 202 Accepted con status "inProgress"
//  3. GET monitor URL (2ª llamada): 200 OK con el DriveItem resultante
func TestClient_CopyItem_PollMonitor_Success(t *testing.T) {
	var monitorCalls atomic.Int32

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/copy"):
			// Copy endpoint: returns 202 with the monitor URL
			w.Header().Set("Location", server.URL+"/monitor/copy123")
			w.WriteHeader(http.StatusAccepted)

		case r.URL.Path == "/monitor/copy123":
			call := monitorCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")

			if call == 1 {
				// First call: operation in progress
				w.WriteHeader(http.StatusAccepted)
				json.NewEncoder(w).Encode(map[string]any{
					"status":             "inProgress",
					"percentageComplete": 42.0,
				})
			} else {
				// Second call: operation completed → DriveItem
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]any{
					"id":   "copiedFile789",
					"name": "copia.pdf",
					"size": 2048000,
				})
			}

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}
	tokenProvider := &mockTokenProvider{token: "test_token"}
	ctx := context.Background()

	// Step 1: Start the asynchronous copy
	monitorURL, err := client.CopyItem(ctx, tokenProvider, ItemID("file123"), "copia.pdf", ItemID("folder456"))
	if err != nil {
		t.Fatalf("CopyItem failed: %v", err)
	}
	if monitorURL == "" {
		t.Fatal("A non-empty monitor URL was expected")
	}

	// Paso 2: Poll #1 — debe devolver "inProgress"
	resp1, err := doJSONRequest[map[string]any](ctx, client, http.MethodGet, monitorURL, nil, nil, tokenProvider)
	if err != nil {
		t.Fatalf("Poll #1 failed: %v", err)
	}
	if status, _ := (*resp1)["status"].(string); status != "inProgress" {
		t.Errorf("Poll #1: status esperado 'inProgress', obtenido %q", status)
	}

	// Paso 3: Poll #2 — debe devolver el DriveItem completo
	resp2, err := doJSONRequest[map[string]any](ctx, client, http.MethodGet, monitorURL, nil, nil, tokenProvider)
	if err != nil {
		t.Fatalf("Poll #2 failed: %v", err)
	}
	if id, _ := (*resp2)["id"].(string); id != "copiedFile789" {
		t.Errorf("Poll #2: id esperado 'copiedFile789', obtenido %q", id)
	}
	if name, _ := (*resp2)["name"].(string); name != "copia.pdf" {
		t.Errorf("Poll #2: name esperado 'copia.pdf', obtenido %q", name)
	}

	// Verificar que se hicieron exactamente 2 polls
	if n := monitorCalls.Load(); n != 2 {
		t.Errorf("Se esperaban 2 llamadas al monitor, se hicieron %d", n)
	}
}

// TestClient_CopyItem_PollMonitor_Failed verifica el flujo de copia
// asynchronous when the server-side operation fails after starting.
//
// Simula:
//  1. POST /copy devuelve 202 Accepted + Location
//  2. GET monitor URL (1ª llamada): 202 Accepted "inProgress"
//  3. GET monitor URL (2ª llamada): 200 OK con status "failed"
func TestClient_CopyItem_PollMonitor_Failed(t *testing.T) {
	var monitorCalls atomic.Int32

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/copy"):
			w.Header().Set("Location", server.URL+"/monitor/copy456")
			w.WriteHeader(http.StatusAccepted)

		case r.URL.Path == "/monitor/copy456":
			call := monitorCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")

			if call == 1 {
				// Progreso normal
				w.WriteHeader(http.StatusAccepted)
				json.NewEncoder(w).Encode(map[string]any{
					"status":             "inProgress",
					"percentageComplete": 78.0,
				})
			} else {
				// Asynchronous failure: status "failed" with error details
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]any{
					"status": "failed",
					"error": map[string]any{
						"code":    "nameAlreadyExists",
						"message": "Ya existe un archivo con el nombre 'copia.pdf'",
					},
				})
			}

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}
	tokenProvider := &mockTokenProvider{token: "test_token"}
	ctx := context.Background()

	// Step 1: Start the asynchronous copy
	monitorURL, err := client.CopyItem(ctx, tokenProvider, ItemID("file123"), "copia.pdf", ItemID("folder456"))
	if err != nil {
		t.Fatalf("CopyItem failed: %v", err)
	}

	// Paso 2: Poll #1 — inProgress
	resp1, err := doJSONRequest[map[string]any](ctx, client, http.MethodGet, monitorURL, nil, nil, tokenProvider)
	if err != nil {
		t.Fatalf("Poll #1 failed: %v", err)
	}
	if status, _ := (*resp1)["status"].(string); status != "inProgress" {
		t.Errorf("Poll #1: status esperado 'inProgress', obtenido %q", status)
	}

	// Paso 3: Poll #2 — debe indicar fallo
	resp2, err := doJSONRequest[map[string]any](ctx, client, http.MethodGet, monitorURL, nil, nil, tokenProvider)
	if err != nil {
		t.Fatalf("Poll #2 failed: %v", err)
	}

	status, _ := (*resp2)["status"].(string)
	if status != "failed" {
		t.Errorf("Poll #2: status esperado 'failed', obtenido %q", status)
	}

	errInfo, _ := (*resp2)["error"].(map[string]any)
	if errInfo == nil {
		t.Fatal("Poll #2: se esperaba campo 'error' en la respuesta")
	}
	if code, _ := errInfo["code"].(string); code != "nameAlreadyExists" {
		t.Errorf("Poll #2: error.code esperado 'nameAlreadyExists', obtenido %q", code)
	}

	// Verificar que se hicieron exactamente 2 polls
	if n := monitorCalls.Load(); n != 2 {
		t.Errorf("Se esperaban 2 llamadas al monitor, se hicieron %d", n)
	}
}

// TestClient_CopyItem_PollMonitor_ContextCancelled verifica que se puede
// cancel the polling loop via context without waiting for an HTTP failure.
func TestClient_CopyItem_PollMonitor_ContextCancelled(t *testing.T) {
	var monitorCalls atomic.Int32

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/copy"):
			w.Header().Set("Location", server.URL+"/monitor/copy789")
			w.WriteHeader(http.StatusAccepted)

		case r.URL.Path == "/monitor/copy789":
			monitorCalls.Add(1)
			// Simulates an operation always in progress (never finishes)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]any{
				"status":             "inProgress",
				"percentageComplete": 10.0,
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	// Context with cancellation
	ctx, cancel := context.WithCancel(context.Background())

	// Paso 1: Iniciar copia
	monitorURL, err := client.CopyItem(ctx, tokenProvider, ItemID("file123"), "copia.pdf", nil)
	if err != nil {
		cancel()
		t.Fatalf("CopyItem failed: %v", err)
	}

	// Paso 2: Primer poll (debe funcionar)
	_, err = doJSONRequest[map[string]any](ctx, client, http.MethodGet, monitorURL, nil, nil, tokenProvider)
	if err != nil {
		cancel()
		t.Fatalf("Poll #1 failed: %v", err)
	}

	// Cancelar el contexto
	cancel()

	// Step 3: Second poll with cancelled context (must fail fast)
	start := time.Now()
	_, err = doJSONRequest[map[string]any](ctx, client, http.MethodGet, monitorURL, nil, nil, tokenProvider)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Se esperaba un error por contexto cancelado")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Error esperado context.Canceled, obtenido: %v", err)
	}

	// The cancellation must be fast (< 1s)
	if elapsed > time.Second {
		t.Errorf("Cancellation took too long: %v", elapsed)
	}

	// Solo 1 poll exitoso
	if n := monitorCalls.Load(); n != 1 {
		t.Errorf("Se esperaba 1 llamada al monitor, se hicieron %d", n)
	}
}

// TestClient_MalformedGraphResponse_ValidJSONNoErrorField verifica que
// a valid JSON without an "error" field also produces a descriptive error.
func TestClient_MalformedGraphResponse_ValidJSONNoErrorField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"otra_cosa": "sin campo error"}`))
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	_, err := client.GetItem(context.Background(), tokenProvider, ItemID("any"))
	if err == nil {
		t.Fatal("Se esperaba un error")
	}
	if !strings.Contains(err.Error(), "sin campo 'error'") {
		t.Errorf("Error inesperado: %v", err)
	}
}

// =============================================================================
// Tests para WaitForAsyncOperation
// =============================================================================

// newTestClient crea un Client con PollBackoff de 1ms para evitar delays en tests.
func newTestClient(baseURL string, httpClient HTTPDoer) *Client {
	return &Client{
		BaseURL:     baseURL,
		HTTPClient:  httpClient,
		PollBackoff: 1 * time.Millisecond,
	}
}

// TestWaitForAsyncOperation_EmptyURL verifies that an empty URL produces an immediate error.
func TestWaitForAsyncOperation_EmptyURL(t *testing.T) {
	client := newTestClient("http://localhost", nil)
	_, err := client.WaitForAsyncOperation(context.Background(), "")
	if err == nil {
		t.Fatal("An error was expected with an empty URL")
	}
	if !strings.Contains(err.Error(), "cannot be empty") {
		t.Errorf("Unexpected error: %v", err)
	}
}

// TestWaitForAsyncOperation_ContextAlreadyCancelled verifica que un contexto
// ya cancelado produce error sin hacer llamadas HTTP.
func TestWaitForAsyncOperation_ContextAlreadyCancelled(t *testing.T) {
	var calls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()

	client := newTestClient(server.URL, server.Client())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.WaitForAsyncOperation(ctx, server.URL+"/monitor/test")
	if err == nil {
		t.Fatal("Se esperaba un error por contexto cancelado")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Error esperado context.Canceled, obtenido: %v", err)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("Se esperaban 0 llamadas HTTP, se hicieron %d", n)
	}
}

// TestWaitForAsyncOperation_SuccessImmediate verifica que si el primer poll
// devuelve "completed", se retorna el DriveItem sin backoff.
func TestWaitForAsyncOperation_SuccessImmediate(t *testing.T) {
	var calls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "completed",
			"resource": map[string]any{
				"id":   "item-abc",
				"name": "resultado.pdf",
				"size": 4096,
			},
		})
	}))
	defer server.Close()

	client := newTestClient(server.URL, server.Client())

	item, err := client.WaitForAsyncOperation(context.Background(), server.URL+"/monitor/test")
	if err != nil {
		t.Fatalf("Error inesperado: %v", err)
	}
	if item.ID != "item-abc" {
		t.Errorf("ID esperado 'item-abc', obtenido %q", item.ID)
	}
	if item.Name != "resultado.pdf" {
		t.Errorf("Name esperado 'resultado.pdf', obtenido %q", item.Name)
	}
	if item.Size != 4096 {
		t.Errorf("Size esperado 4096, obtenido %d", item.Size)
	}
	if calls.Load() != 1 {
		t.Errorf("Se esperaba 1 llamada, se hicieron %d", calls.Load())
	}
}

// TestWaitForAsyncOperation_SuccessAfterBackoff verifica el flujo completo:
// inProgress → backoff → completed.
func TestWaitForAsyncOperation_SuccessAfterBackoff(t *testing.T) {
	var calls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if call == 1 {
			json.NewEncoder(w).Encode(map[string]any{
				"status":             "inProgress",
				"percentageComplete": 42.0,
			})
		} else {
			json.NewEncoder(w).Encode(map[string]any{
				"status": "completed",
				"resource": map[string]any{
					"id":   "copied-789",
					"name": "copia.pdf",
					"size": 2048000,
				},
			})
		}
	}))
	defer server.Close()

	client := newTestClient(server.URL, server.Client())

	item, err := client.WaitForAsyncOperation(context.Background(), server.URL+"/monitor/test")
	if err != nil {
		t.Fatalf("Error inesperado: %v", err)
	}
	if item.ID != "copied-789" {
		t.Errorf("ID esperado 'copied-789', obtenido %q", item.ID)
	}
	if item.Name != "copia.pdf" {
		t.Errorf("Name esperado 'copia.pdf', obtenido %q", item.Name)
	}
	if calls.Load() != 2 {
		t.Errorf("Se esperaban 2 llamadas, se hicieron %d", calls.Load())
	}
}

// TestWaitForAsyncOperation_FailedWithError verifica que un status "failed"
// con detalles de error devuelve un *GraphError.
func TestWaitForAsyncOperation_FailedWithError(t *testing.T) {
	var calls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if call == 1 {
			json.NewEncoder(w).Encode(map[string]any{
				"status": "inProgress",
			})
		} else {
			json.NewEncoder(w).Encode(map[string]any{
				"status": "failed",
				"error": map[string]any{
					"code":    "nameAlreadyExists",
					"message": "Ya existe un archivo con ese nombre",
				},
			})
		}
	}))
	defer server.Close()

	client := newTestClient(server.URL, server.Client())

	_, err := client.WaitForAsyncOperation(context.Background(), server.URL+"/monitor/test")
	if err == nil {
		t.Fatal("An error was expected for a failed operation")
	}

	var graphErr *GraphError
	if !errors.As(err, &graphErr) {
		t.Fatalf("Se esperaba *GraphError, obtenido %T: %v", err, err)
	}
	if graphErr.Code != "nameAlreadyExists" {
		t.Errorf("Code esperado 'nameAlreadyExists', obtenido %q", graphErr.Code)
	}
	if graphErr.Message != "Ya existe un archivo con ese nombre" {
		t.Errorf("Message esperado 'Ya existe un archivo con ese nombre', obtenido %q", graphErr.Message)
	}
	if calls.Load() != 2 {
		t.Errorf("Se esperaban 2 llamadas, se hicieron %d", calls.Load())
	}
}

// TestWaitForAsyncOperation_FailedNoDetails verifica que status "failed"
// sin campo error devuelve un error descriptivo.
func TestWaitForAsyncOperation_FailedNoDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "failed",
		})
	}))
	defer server.Close()

	client := newTestClient(server.URL, server.Client())

	_, err := client.WaitForAsyncOperation(context.Background(), server.URL+"/monitor/test")
	if err == nil {
		t.Fatal("Se esperaba un error")
	}
	if !strings.Contains(err.Error(), "failed without detail") {
		t.Errorf("Unexpected error: %v", err)
	}
}

// TestWaitForAsyncOperation_CompletedNoResource verifica que status "completed"
// sin resource devuelve un error descriptivo.
func TestWaitForAsyncOperation_CompletedNoResource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "completed",
		})
	}))
	defer server.Close()

	client := newTestClient(server.URL, server.Client())

	_, err := client.WaitForAsyncOperation(context.Background(), server.URL+"/monitor/test")
	if err == nil {
		t.Fatal("Se esperaba un error")
	}
	if !strings.Contains(err.Error(), "without returned resource") {
		t.Errorf("Unexpected error: %v", err)
	}
}

// TestWaitForAsyncOperation_UnknownStatus verifica que un status no reconocido
// produce un error descriptivo.
func TestWaitForAsyncOperation_UnknownStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "cancelled",
		})
	}))
	defer server.Close()

	client := newTestClient(server.URL, server.Client())

	_, err := client.WaitForAsyncOperation(context.Background(), server.URL+"/monitor/test")
	if err == nil {
		t.Fatal("Se esperaba un error por status desconocido")
	}
	if !strings.Contains(err.Error(), "unknown operation status") {
		t.Errorf("Error inesperado: %v", err)
	}
}

// TestWaitForAsyncOperation_NetworkError verifica que un error de HTTPClient.Do
// se envuelve correctamente.
func TestWaitForAsyncOperation_NetworkError(t *testing.T) {
	mock := &mockHTTPDoer{
		doFn: func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	client := newTestClient("http://localhost", mock)

	_, err := client.WaitForAsyncOperation(context.Background(), "http://localhost/monitor/test")
	if err == nil {
		t.Fatal("Se esperaba un error de red")
	}
	if !strings.Contains(err.Error(), "error de red al monitorizar") {
		t.Errorf("Error inesperado: %v", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("The error should contain the cause: %v", err)
	}
}

// TestWaitForAsyncOperation_InvalidJSON verifica que un cuerpo no-JSON
// produce un error de parseo.
func TestWaitForAsyncOperation_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("esto no es json"))
	}))
	defer server.Close()

	client := newTestClient(server.URL, server.Client())

	_, err := client.WaitForAsyncOperation(context.Background(), server.URL+"/monitor/test")
	if err == nil {
		t.Fatal("Se esperaba un error de parseo JSON")
	}
	if !strings.Contains(err.Error(), "error parsing operation status") {
		t.Errorf("Error inesperado: %v", err)
	}
}

// TestWaitForAsyncOperation_CtxCancelDuringBackoff verifies that the cancellation
// de contexto durante el backoff interrumpe el bucle y devuelve context.Canceled.
func TestWaitForAsyncOperation_CtxCancelDuringBackoff(t *testing.T) {
	var calls atomic.Int32
	firstPollDone := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "inProgress",
		})
		// Signal after the first poll so that the goroutine cancels the context
		if n == 1 {
			close(firstPollDone)
		}
	}))
	defer server.Close()

	client := newTestClient(server.URL, server.Client())
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel the context right after the first poll finishes.
	go func() {
		<-firstPollDone
		cancel()
	}()

	_, err := client.WaitForAsyncOperation(ctx, server.URL+"/monitor/test")
	if err == nil {
		t.Fatal("Se esperaba un error por contexto cancelado")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Error esperado context.Canceled, obtenido: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("Se esperaba 1 llamada, se hicieron %d", calls.Load())
	}
}

// TestWaitForAsyncOperation_MultipleBackoffSteps verifica que el backoff
// crece exponencialmente: 1ms → 2ms → 4ms → ... hasta maxBackoff.
func TestWaitForAsyncOperation_MultipleBackoffSteps(t *testing.T) {
	var calls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if call < 4 {
			json.NewEncoder(w).Encode(map[string]any{
				"status": "inProgress",
			})
		} else {
			json.NewEncoder(w).Encode(map[string]any{
				"status": "completed",
				"resource": map[string]any{
					"id":   "final-001",
					"name": "final.pdf",
					"size": 100,
				},
			})
		}
	}))
	defer server.Close()

	client := newTestClient(server.URL, server.Client())

	item, err := client.WaitForAsyncOperation(context.Background(), server.URL+"/monitor/test")
	if err != nil {
		t.Fatalf("Error inesperado: %v", err)
	}
	if item.ID != "final-001" {
		t.Errorf("ID esperado 'final-001', obtenido %q", item.ID)
	}
	// 3 inProgress + 1 completed = 4 llamadas
	if calls.Load() != 4 {
		t.Errorf("Se esperaban 4 llamadas, se hicieron %d", calls.Load())
	}
}
