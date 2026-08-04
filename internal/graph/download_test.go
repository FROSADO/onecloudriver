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
		content := []byte("contenido descargado")
		switch {
		case !strings.Contains(r.URL.Path, "/content"):
			// Metadata
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id": "file123", "name": "archivo.txt", "size": %d}`, len(content))
		default:
			// Contenido
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
	// contenido descargado
}

// TestClient_GetItemContent_Success prueba la descarga de contenido binario por ID y por ruta.
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
						t.Error("Token no enviado correctamente")
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
					t.Errorf("Ruta inesperada: %s", r.URL.Path)
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
				t.Fatalf("Error inesperado: %v", err)
			}

			if string(content) != tt.expectedContent {
				t.Errorf("Contenido incorrecto: %s", string(content))
			}
		})
	}
}

// TestClient_GetItemContentStream_Success prueba la descarga streaming por ID y por ruta.
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
						t.Error("Token no enviado correctamente")
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
					t.Errorf("Ruta inesperada: %s", r.URL.Path)
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
				t.Fatalf("Error inesperado: %v", err)
			}

			if n != int64(len(tt.expectedContent)) {
				t.Errorf("Bytes escritos: esperado %d, obtenido %d", len(tt.expectedContent), n)
			}
			if buf.String() != tt.expectedContent {
				t.Errorf("Contenido incorrecto: %s", buf.String())
			}
		})
	}
}

// TestClient_GetItemContentStream_LargeFile prueba la descarga por chunks de un archivo grande (>10 MB)
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
			// Escribir chunkLen bytes (sin alocar todo en memoria: escribir por bloques)
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
			t.Errorf("Ruta inesperada: %s", r.URL.Path)
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
		t.Fatalf("Error inesperado: %v", err)
	}

	// Verificar que se hicieron 3 chunks y los Range headers son correctos
	if len(actualRanges) != 3 {
		t.Errorf("Se esperaban 3 chunks, se hicieron %d", len(actualRanges))
	}
	for i, expected := range expectedRanges {
		if i >= len(actualRanges) {
			break
		}
		if actualRanges[i] != expected {
			t.Errorf("Chunk %d: Range esperado %q, obtenido %q", i, expected, actualRanges[i])
		}
	}

	if n != totalFileSize {
		t.Errorf("Bytes escritos: esperado %d, obtenido %d", totalFileSize, n)
	}
	if int64(buf.Len()) != totalFileSize {
		t.Errorf("Buffer size: esperado %d, obtenido %d", totalFileSize, buf.Len())
	}
}

// TestClient_GetItemContentStream_NetworkError verifica que un error de red
// durante la descarga se propaga correctamente y no se traga.
func TestClient_GetItemContentStream_NetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Metadata: responde normalmente
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "file123", "name": "test.bin", "size": 100}`))
	}))
	defer server.Close()

	// mockHTTPDoer que falla solo en peticiones de contenido
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
		t.Fatal("Se esperaba un error de red")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("Error inesperado: %v", err)
	}
}

// TestClient_GetItemContentStream_Timeout verifica que un contexto con deadline
// vencido durante la descarga devuelve context.DeadlineExceeded.
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
		t.Fatal("Se esperaba un error por timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) &&
		!strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("Error inesperado: %v", err)
	}
}

// TestClient_GetItemContentStream_RangeNotSatisfiable verifies that the code
// de error 416 (Range Not Satisfiable) de OneDrive se maneja correctamente.
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
		t.Errorf("Error inesperado: %v", err)
	}
}

// TestClient_GetItemContentStream_ZeroByteFile verifica que la descarga de
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
		t.Fatalf("Error inesperado: %v", err)
	}

	if n != 0 {
		t.Errorf("Bytes escritos: esperado 0, obtenido %d", n)
	}
	if buf.Len() != 0 {
		t.Errorf("Buffer size: esperado 0, obtenido %d", buf.Len())
	}

	// Verify that a content request was NEVER made
	if contentRequested {
		t.Error("A content request should not have been made for an empty file")
	}
}
