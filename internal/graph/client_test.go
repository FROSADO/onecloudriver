package graph

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClient_NewClient(t *testing.T) {
	client := NewClient()
	if client.BaseURL == "" {
		t.Error("El valor de BaseURL no puede estar vacio")

	}
	if client.HTTPClient == nil {
		t.Error("Se espera un HTTPClient en el cliente")
	}

}

func TestResourcePathByPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "root slash", in: "/", want: "/me/drive/root"},
		{name: "empty string", in: "", want: "/me/drive/root"},
		{name: "dot", in: ".", want: "/me/drive/root"},
		{name: "simple folder", in: "/Documentos", want: "/me/drive/root:/Documentos"},
		{name: "trailing slash", in: "Documentos/", want: "/me/drive/root:/Documentos"},
		{name: "double slashes", in: "//Documentos//foto.jpg", want: "/me/drive/root:/Documentos/foto.jpg"},
		{name: "nested file", in: "/Documentos/foto.jpg", want: "/me/drive/root:/Documentos/foto.jpg"},
		{name: "unicode and spaces", in: "/My Photos/año.jpg", want: "/me/drive/root:/My%20Photos/a%C3%B1o.jpg"},
		{name: "parent ref resolved", in: "/a/../b", want: "/me/drive/root:/b"},
		{name: "hash in name", in: "/informe #1.pdf", want: "/me/drive/root:/informe%20%231.pdf"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResourcePathByPath(tt.in); got != tt.want {
				t.Errorf("ResourcePathByPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestChildrenPath(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "root by path", got: ChildrenPathByPath("/"), want: "/me/drive/root/children"},
		{name: "folder by path", got: ChildrenPathByPath("/Docs"), want: "/me/drive/root:/Docs:/children"},
		{name: "by ID", got: ChildrenPathByID("123"), want: "/me/drive/items/123/children"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestContentPath(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "root by path", got: ContentPathByPath("/"), want: "/me/drive/root/content"},
		{name: "folder by path", got: ContentPathByPath("/Docs"), want: "/me/drive/root:/Docs:/content"},
		{name: "by ID", got: ContentPathByID("123"), want: "/me/drive/items/123/content"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

// mockHTTPDoer implements HTTPDoer with an injectable function for tests.
type mockHTTPDoer struct {
	doFn func(req *http.Request) (*http.Response, error)
}

func (m *mockHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	return m.doFn(req)
}

// TestRetryDoer_WithRetryOption prueba que el RetryDoer reintenta en 429/503
// with exponential backoff and that eventually succeeds.
func TestRetryDoer_WithRetryOption(t *testing.T) {
	var callCount atomic.Int32

	inner := &mockHTTPDoer{
		doFn: func(req *http.Request) (*http.Response, error) {
			callCount.Add(1)
			if callCount.Load() <= 2 {
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		},
	}

	retry := NewRetryDoer(inner, 2)

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	resp, err := retry.Do(req)
	if err != nil {
		t.Fatalf("Error inesperado: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status esperado 200, obtenido %d", resp.StatusCode)
	}
	if callCount.Load() != 3 {
		t.Errorf("Se esperaban 3 llamadas (1 original + 2 retries), se hicieron %d", callCount.Load())
	}
}

// ExampleNewClient demonstrates how to create a Graph client with options.
func ExampleNewClient() {
	// Default client (production)
	_ = NewClient()

	// Cliente con URL base personalizada (tests con httptest)
	_ = NewClient(WithBaseURL("http://localhost:8080"))

	// Cliente con timeout extendido para operaciones largas
	_ = NewClient(WithTimeout(60 * time.Second))

	// Client with automatic retries
	_ = NewClient(WithRetry(3))

	// Output:

}

// ExampleItemID demonstrates how to address OneDrive resources by ID.
func ExampleItemID() {
	// Item by unique ID
	item := ItemID("01BYE5RZ6QN3VXWNFTJID2X6GJZ5YUVGYY")
	fmt.Println(item.ResourcePath())

	// Special ID for the root folder
	root := RootID
	fmt.Println(root.ResourcePath())

	// ParentReference para MoveItem/CopyItem
	ref := root.ParentReference()
	fmt.Println(ref["id"])

	// Output:
	// /me/drive/items/01BYE5RZ6QN3VXWNFTJID2X6GJZ5YUVGYY
	// /me/drive/root
	// root
}

// ExampleItemPath demonstrates how to address OneDrive resources by path.
func ExampleItemPath() {
	// Item por ruta dentro del drive
	item := ItemPath("/Documentos/foto.jpg")
	fmt.Println(item.ResourcePath())

	// ParentReference para MoveItem/CopyItem
	ref := item.ParentReference()
	fmt.Println(ref["path"])

	// Output:
	// /me/drive/root:/Documentos/foto.jpg
	// /Documentos/foto.jpg
}

// ExampleResourcePathByID demonstrates building paths by ID.
func ExampleResourcePathByID() {
	fmt.Println(ResourcePathByID("root"))
	fmt.Println(ResourcePathByID("01ABC123"))

	// Output:
	// /me/drive/root
	// /me/drive/items/01ABC123
}

// ExampleResourcePathByPath demonstrates building paths by path.
func ExampleResourcePathByPath() {
	fmt.Println(ResourcePathByPath("/"))
	fmt.Println(ResourcePathByPath("/Documentos/foto.jpg"))
	fmt.Println(ResourcePathByPath("a/../b"))

	// Output:
	// /me/drive/root
	// /me/drive/root:/Documentos/foto.jpg
	// /me/drive/root:/b
}

// TestRetryDoer_ExhaustedRetries tests that RetryDoer returns the last
// respuesta de error cuando se agotan los reintentos.
func TestRetryDoer_ExhaustedRetries(t *testing.T) {
	var callCount atomic.Int32

	inner := &mockHTTPDoer{
		doFn: func(req *http.Request) (*http.Response, error) {
			callCount.Add(1)
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		},
	}

	retry := NewRetryDoer(inner, 1) // solo 1 reintento

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	resp, err := retry.Do(req)
	if err != nil {
		t.Fatalf("Error inesperado: %v", err)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("Status esperado 429, obtenido %d", resp.StatusCode)
	}
	if callCount.Load() != 2 {
		t.Errorf("Se esperaban 2 llamadas (1 original + 1 retry), se hicieron %d", callCount.Load())
	}
}

// TestRetryDoer_NetworkError_RetriesAndSucceeds prueba que el RetryDoer
// retries after transient network errors (timeout) and eventually succeeds.
func TestRetryDoer_NetworkError_RetriesAndSucceeds(t *testing.T) {
	var callCount atomic.Int32

	inner := &mockHTTPDoer{
		doFn: func(req *http.Request) (*http.Response, error) {
			callCount.Add(1)
			if callCount.Load() <= 2 {
				// Simular timeout de red
				return nil, &net.OpError{Op: "dial", Err: fmt.Errorf("i/o timeout")}
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		},
	}

	retry := NewRetryDoer(inner, 3)

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	resp, err := retry.Do(req)
	if err != nil {
		t.Fatalf("Error inesperado: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status esperado 200, obtenido %d", resp.StatusCode)
	}
	if callCount.Load() != 3 {
		t.Errorf("Expected 3 calls (2 errors + 1 success), got %d", callCount.Load())
	}
}

// TestRetryDoer_NetworkError_ExhaustsRetries tests that the last
// error de red cuando se agotan los reintentos.
func TestRetryDoer_NetworkError_ExhaustsRetries(t *testing.T) {
	var callCount atomic.Int32

	inner := &mockHTTPDoer{
		doFn: func(req *http.Request) (*http.Response, error) {
			callCount.Add(1)
			return nil, &net.OpError{Op: "dial", Err: fmt.Errorf("i/o timeout")}
		},
	}

	retry := NewRetryDoer(inner, 2) // 2 reintentos = 3 intentos totales

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	_, err := retry.Do(req)
	if err == nil {
		t.Fatal("Se esperaba un error")
	}
	if callCount.Load() != 3 {
		t.Errorf("Se esperaban 3 llamadas (1 original + 2 retries), se hicieron %d", callCount.Load())
	}
}

// TestRetryDoer_NonRetryableError prueba que errores permanentes (no de red)
// NO se reintentan.
func TestRetryDoer_NonRetryableError(t *testing.T) {
	var callCount atomic.Int32

	inner := &mockHTTPDoer{
		doFn: func(req *http.Request) (*http.Response, error) {
			callCount.Add(1)
			return nil, fmt.Errorf("permanent error: invalid TLS certificate")
		},
	}

	retry := NewRetryDoer(inner, 3)

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	_, err := retry.Do(req)
	if err == nil {
		t.Fatal("Se esperaba un error")
	}
	if callCount.Load() != 1 {
		t.Errorf("Error no reintentable: se esperaba 1 llamada, se hicieron %d", callCount.Load())
	}
}

// TestRetryDoer_RetryAfterHeader prueba que RetryDoer respeta el header
// Retry-After en lugar del exponential backoff.
func TestRetryDoer_RetryAfterHeader(t *testing.T) {
	var callCount atomic.Int32
	start := time.Now()

	inner := &mockHTTPDoer{
		doFn: func(req *http.Request) (*http.Response, error) {
			callCount.Add(1)
			if callCount.Load() == 1 {
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Header:     http.Header{"Retry-After": []string{"1"}},
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		},
	}

	retry := NewRetryDoer(inner, 3)

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	resp, err := retry.Do(req)
	if err != nil {
		t.Fatalf("Error inesperado: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status esperado 200, obtenido %d", resp.StatusCode)
	}
	elapsed := time.Since(start)
	if elapsed < 1*time.Second {
		t.Errorf("Esperado delay >= 1s (Retry-After), transcurrido %v", elapsed)
	}
}

// testTimeoutError implementa net.Error con Timeout() == true para tests.
type testTimeoutError struct{}

func (e *testTimeoutError) Error() string   { return "timeout" }
func (e *testTimeoutError) Timeout() bool   { return true }
func (e *testTimeoutError) Temporary() bool { return true }

// TestIsNetworkError tests the error classification helper function.
func TestIsNetworkError(t *testing.T) {
	tests := []struct {
		err      error
		expected bool
	}{
		{nil, false},
		{fmt.Errorf("i/o timeout"), true},
		{fmt.Errorf("connection refused"), true},
		{fmt.Errorf("connection reset by peer"), true},
		{fmt.Errorf("no such host"), true},
		{fmt.Errorf("network is unreachable"), true},
		{&net.OpError{Op: "dial", Err: &testTimeoutError{}}, true},
		{fmt.Errorf("invalid TLS certificate"), false},
		{fmt.Errorf("401 Unauthorized"), false},
		{fmt.Errorf("some random error"), false},
	}

	for _, tc := range tests {
		result := isNetworkError(tc.err)
		if result != tc.expected {
			t.Errorf("isNetworkError(%v) = %v, esperado %v", tc.err, result, tc.expected)
		}
	}
}
