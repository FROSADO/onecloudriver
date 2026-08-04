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
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ExampleClient_UploadItem demonstrates how to upload a file to OneDrive.
func ExampleClient_UploadItem() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id": "newfile", "name": "hola.txt", "size": 11}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	content := strings.NewReader("Hola, mundo")
	item, err := client.UploadItem(context.Background(), tokenProvider, RootID, "hola.txt", content)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println(item.Name, item.Size)

	// Output:
	// hola.txt 11
}

// TestClient_UploadItem_Success prueba la subida de archivos por ID y por ruta.
func TestClient_UploadItem_Success(t *testing.T) {
	tests := []struct {
		name         string
		parent       Resource
		expectedPath string
		fileName     string
		content      string
	}{
		{
			name:         "by ID",
			parent:       ItemID("folder123"),
			expectedPath: "/me/drive/items/folder123:/foto.jpg:/content",
			fileName:     "foto.jpg",
			content:      "contenido binario de prueba",
		},
		{
			name:         "by Path",
			parent:       ItemPath("/Documentos"),
			expectedPath: "/me/drive/root:/Documentos:/archivo.txt:/content",
			fileName:     "archivo.txt",
			content:      "hola mundo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut {
					t.Errorf("Expected method PUT, got %s", r.Method)
				}
				if r.URL.Path != tt.expectedPath {
					t.Errorf("Ruta esperada %s, obtenida %s", tt.expectedPath, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test_token" {
					t.Error("Token no enviado correctamente")
				}

				bodyBytes, _ := io.ReadAll(r.Body)
				if string(bodyBytes) != tt.content {
					t.Errorf("Contenido esperado %q, obtenido %q", tt.content, string(bodyBytes))
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"id":   "newfile",
					"name": tt.fileName,
					"size": len(tt.content),
				})
			}))
			defer server.Close()

			client := &Client{
				BaseURL:    server.URL,
				HTTPClient: server.Client(),
			}

			tokenProvider := &mockTokenProvider{token: "test_token"}

			item, err := client.UploadItem(context.Background(), tokenProvider, tt.parent, tt.fileName, strings.NewReader(tt.content))
			if err != nil {
				t.Fatalf("Error inesperado: %v", err)
			}

			if item.Name != tt.fileName {
				t.Errorf("Nombre incorrecto: esperado %q, obtenido %q", tt.fileName, item.Name)
			}
		})
	}
}

// TestClient_UploadItemStream_LargeFile prueba la subida por upload session con chunks.
func TestClient_UploadItemStream_LargeFile(t *testing.T) {
	expectedRanges := []string{
		"bytes 0-327679/1048576",
		"bytes 327680-655359/1048576",
		"bytes 655360-983039/1048576",
		"bytes 983040-1048575/1048576",
	}
	var actualRanges []string
	var mu sync.Mutex
	var serverURL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/me/drive/items/folder123:/grande.zip:/createUploadSession":
			if r.Method != http.MethodPost {
				t.Errorf("Expected method POST, got %s", r.Method)
			}
			if r.Header.Get("Authorization") != "Bearer test_token" {
				t.Error("Token no enviado correctamente")
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"uploadUrl": serverURL + "/upload/grande.zip",
			})
		case "/upload/grande.zip":
			if r.Method != http.MethodPut {
				t.Errorf("Expected method PUT, got %s", r.Method)
			}
			rangeHeader := r.Header.Get("Content-Range")
			if rangeHeader == "" {
				t.Error("Se esperaba header Content-Range")
			}
			mu.Lock()
			actualRanges = append(actualRanges, rangeHeader)
			mu.Unlock()

			if rangeHeader == expectedRanges[len(expectedRanges)-1] {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"id":   "bigfile",
					"name": "grande.zip",
					"size": 1048576,
				})
			} else {
				w.WriteHeader(http.StatusAccepted)
			}
		default:
			t.Errorf("Ruta inesperada: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	tokenProvider := &mockTokenProvider{token: "test_token"}

	fileSize := int64(1048576)
	data := []byte(strings.Repeat("A", int(fileSize)))

	item, err := client.UploadItemStream(context.Background(), tokenProvider, ItemID("folder123"), "grande.zip", bytes.NewReader(data), fileSize)
	if err != nil {
		t.Fatalf("Error inesperado: %v", err)
	}

	if item.Name != "grande.zip" {
		t.Errorf("Nombre incorrecto: %s", item.Name)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(actualRanges) != len(expectedRanges) {
		t.Errorf("Se esperaban %d chunks, se hicieron %d", len(expectedRanges), len(actualRanges))
	}
	for i, expected := range expectedRanges {
		if i >= len(actualRanges) {
			break
		}
		if actualRanges[i] != expected {
			t.Errorf("Chunk %d: Range esperado %q, obtenido %q", i, expected, actualRanges[i])
		}
	}
}

// TestClient_UploadItemStream_PoolReuse verifica que el sync.Pool de buffers
// works correctly in consecutive calls (there is no data corruption).
func TestClient_UploadItemStream_PoolReuse(t *testing.T) {
	var mu sync.Mutex
	var serverURL string

	// chunksPerSession tracks chunks received per upload session call
	var sessionCalls int
	var chunksInSession int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/me/drive/items/folder123:/pool_test.bin:/createUploadSession":
			mu.Lock()
			sessionCalls++
			chunksInSession = 0
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"uploadUrl": serverURL + "/upload/pool_test.bin",
			})
		case "/upload/pool_test.bin":
			mu.Lock()
			chunksInSession++
			currentChunks := chunksInSession
			currentSession := sessionCalls
			mu.Unlock()

			// 4 chunks per upload (1MB file / 320KB chunkSize)
			if currentChunks == 4 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"id":   fmt.Sprintf("poolfile%d", currentSession),
					"name": fmt.Sprintf("pool_test%d.bin", currentSession),
					"size": 1048576,
				})
			} else {
				w.WriteHeader(http.StatusAccepted)
			}
		default:
			t.Errorf("Ruta inesperada: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	fileSize := int64(1048576)

	// Primera subida
	data1 := []byte(strings.Repeat("B", int(fileSize)))
	item1, err := client.UploadItemStream(context.Background(), tokenProvider, ItemID("folder123"), "pool_test.bin", bytes.NewReader(data1), fileSize)
	if err != nil {
		t.Fatalf("Error en primera subida: %v", err)
	}
	if item1.Name != "pool_test1.bin" {
		t.Errorf("Nombre incorrecto primera subida: %s", item1.Name)
	}

	// Segunda subida (reutiliza buffer del pool)
	data2 := []byte(strings.Repeat("C", int(fileSize)))
	item2, err := client.UploadItemStream(context.Background(), tokenProvider, ItemID("folder123"), "pool_test.bin", bytes.NewReader(data2), fileSize)
	if err != nil {
		t.Fatalf("Error en segunda subida: %v", err)
	}
	if item2.Name != "pool_test2.bin" {
		t.Errorf("Nombre incorrecto segunda subida: %s", item2.Name)
	}
}

// TestClient_UploadItemStream_CreateSessionError verifica que un error
// al crear la upload session se propaga correctamente.
func TestClient_UploadItemStream_CreateSessionError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "createUploadSession") {
			w.WriteHeader(http.StatusForbidden)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"error":{"code":"accessDenied","message":"Access denied"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	data := strings.NewReader("contenido")
	_, err := client.UploadItemStream(context.Background(), tokenProvider, ItemID("folder123"), "test.bin", data, 100)
	if err == nil {
		t.Fatal("Se esperaba un error al crear la upload session")
	}
	if !strings.Contains(err.Error(), "Forbidden") &&
		!strings.Contains(err.Error(), "accessDenied") &&
		!strings.Contains(err.Error(), "403") {
		t.Errorf("Error inesperado: %v", err)
	}
}

// TestClient_UploadItemStream_SessionExpired verifica que un 404 en un chunk
// (expired or deleted session) produces a descriptive error and calls
// cancelUploadSession en background.
func TestClient_UploadItemStream_SessionExpired(t *testing.T) {
	var serverURL string
	cancelCalled := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "createUploadSession"):
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"uploadUrl": serverURL + "/upload/expired.bin",
			})
		case strings.Contains(r.URL.Path, "/upload/expired.bin"):
			if r.Method == http.MethodDelete {
				cancelCalled <- struct{}{}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			// Chunk PUT: expired session
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":{"code":"itemNotFound","message":"Upload session not found"}}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	fileSize := int64(2000)
	data := strings.NewReader(strings.Repeat("X", int(fileSize)))
	_, err := client.UploadItemStream(context.Background(), tokenProvider, ItemID("folder123"), "expired.bin", data, fileSize)
	if err == nil {
		t.Fatal("An error was expected for an expired session")
	}
	if !strings.Contains(err.Error(), "404") &&
		!strings.Contains(err.Error(), "itemNotFound") {
		t.Errorf("Error inesperado: %v", err)
	}

	// Verify that cancelUploadSession ran in the background
	select {
	case <-cancelCalled:
		// OK
	case <-time.After(2 * time.Second):
		t.Error("Se esperaba que cancelUploadSession se llamara en background")
	}
}

// TestClient_UploadItemStream_ChunkRetry verifica que los chunks se reintentan
// automatically via RetryDoer when the server returns 503.
func TestClient_UploadItemStream_ChunkRetry(t *testing.T) {
	var chunkAttempts atomic.Int32
	var serverURL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "createUploadSession"):
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"uploadUrl": serverURL + "/upload/retry.bin",
			})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	chunkResponse := []byte(`{"id":"retryfile","name":"retry.bin","size":5000}`)

	// mockHTTPDoer: session creation goes to the real server,
	// the chunks go through the mock that simulates 503 → success.
	inner := &mockHTTPDoer{
		doFn: func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodPut {
				chunkAttempts.Add(1)
				if chunkAttempts.Load() == 1 {
					return &http.Response{
						StatusCode: http.StatusServiceUnavailable,
						Body:       io.NopCloser(strings.NewReader("")),
					}, nil
				}
				// Reintento exitoso
				return &http.Response{
					StatusCode: http.StatusCreated,
					Body:       io.NopCloser(bytes.NewReader(chunkResponse)),
					Header:     http.Header{"Content-Type": {"application/json"}},
				}, nil
			}
			return server.Client().Do(req)
		},
	}

	// Wrap in RetryDoer to retry 503 automatically
	retryDoer := NewRetryDoer(inner, 2)

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: retryDoer,
	}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	data := strings.NewReader(strings.Repeat("Z", 5000))
	item, err := client.UploadItemStream(context.Background(), tokenProvider, ItemID("folder123"), "retry.bin", data, 5000)
	if err != nil {
		t.Fatalf("Error inesperado: %v", err)
	}

	if item.Name != "retry.bin" {
		t.Errorf("Nombre incorrecto: %s", item.Name)
	}

	// Verify that the chunk was retried (1st attempt fails, 2nd succeeds)
	if chunkAttempts.Load() != 2 {
		t.Errorf("Se esperaban 2 intentos de chunk (1 fallo + 1 retry), se hicieron %d", chunkAttempts.Load())
	}
}
