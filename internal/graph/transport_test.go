package graph

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestClient_TransportConnectionReuse verifies that N sequential requests over
// the tuned transport reuse a single connection instead of opening one per
// request (the acceptance criterion of issue #70).
func TestClient_TransportConnectionReuse(t *testing.T) {
	var conns int32

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	server.Config.ConnState = func(_ net.Conn, cs http.ConnState) {
		if cs == http.StateNew {
			atomic.AddInt32(&conns, 1)
		}
	}
	server.Start()
	defer server.Close()

	tr := &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     30 * time.Second,
	}
	client := NewClient(WithBaseURL(server.URL), WithTransport(tr), WithRetry(0))

	const n = 5
	for i := 0; i < n; i++ {
		req, err := http.NewRequest(http.MethodGet, client.URL("/me/drive/root", nil), nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.HTTPClient.Do(req)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		// Drain and close the body so the connection returns to the idle pool.
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	if got := atomic.LoadInt32(&conns); got != 1 {
		t.Fatalf("expected 1 connection reused across %d requests, got %d", n, got)
	}
}

// TestWithTransport_SetsTransport verifies the option replaces the transport
// of an *http.Client-backed Client.
func TestWithTransport_SetsTransport(t *testing.T) {
	tr := &http.Transport{}
	c := &Client{HTTPClient: &http.Client{Timeout: 15 * time.Second}}

	WithTransport(tr)(c)

	hc, ok := c.HTTPClient.(*http.Client)
	if !ok {
		t.Fatal("expected *http.Client")
	}
	if hc.Transport != tr {
		t.Fatalf("transport = %v, want %v", hc.Transport, tr)
	}
}

// TestWithTransport_NoopForNonHTTPClient verifies the option is a no-op (and
// does not panic) when the HTTPClient is not an *http.Client (e.g. a mock).
func TestWithTransport_NoopForNonHTTPClient(t *testing.T) {
	mock := &mockHTTPDoer{doFn: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	}}
	c := &Client{HTTPClient: mock}

	WithTransport(&http.Transport{})(c)

	if c.HTTPClient != mock {
		t.Fatal("WithTransport should not replace a non-*http.Client")
	}
}

// TestWithTimeout_SetsResponseHeaderTimeout verifies WithTimeout also applies
// the timeout to the transport's ResponseHeaderTimeout (issue #70).
func TestWithTimeout_SetsResponseHeaderTimeout(t *testing.T) {
	tr := &http.Transport{}
	c := &Client{HTTPClient: &http.Client{Transport: tr}}

	WithTimeout(30 * time.Second)(c)

	hc, ok := c.HTTPClient.(*http.Client)
	if !ok {
		t.Fatal("expected *http.Client")
	}
	if hc.Timeout != 30*time.Second {
		t.Errorf("client timeout = %v, want 30s", hc.Timeout)
	}
	if tr.ResponseHeaderTimeout != 30*time.Second {
		t.Errorf("transport ResponseHeaderTimeout = %v, want 30s", tr.ResponseHeaderTimeout)
	}
}

// TestNewClient_UsesTunedTransport verifies NewClient gives each client its own
// tuned transport (never the shared http.DefaultTransport), so mutating options
// like WithTimeout don't affect other clients.
func TestNewClient_UsesTunedTransport(t *testing.T) {
	rd, ok := NewClient(WithRetry(0)).HTTPClient.(*RetryDoer)
	if !ok {
		t.Fatal("expected RetryDoer wrapping")
	}
	hc, ok := rd.inner.(*http.Client)
	if !ok {
		t.Fatal("expected *http.Client inside RetryDoer")
	}
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}
	if tr == http.DefaultTransport {
		t.Error("NewClient must use its own transport, not the shared http.DefaultTransport")
	}
	if tr.MaxIdleConnsPerHost < 2 {
		t.Errorf("MaxIdleConnsPerHost = %d, want > default (2)", tr.MaxIdleConnsPerHost)
	}
}
