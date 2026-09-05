package control

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

// fakeProvider implements InfoProvider for testing.
type fakeProvider struct {
	info Info
}

func (f *fakeProvider) Info() Info {
	return f.info
}

// waitDial blocks until a Unix socket accepts connections or the timeout
// expires. Used instead of time.Sleep so tests are deterministic: the control
// server starts serving right after NewServer returns, and the listener is
// already bound, but the accept goroutine may not have scheduled yet.
func waitDial(t *testing.T, sockPath string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("unix", sockPath, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("control socket %s is not accepting connections: %v", sockPath, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// rawClient returns an HTTP client that dials the given Unix socket, for tests
// that need direct control over the request (method, path).
func rawClient(sockPath string) *http.Client {
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", sockPath)
			},
		},
		Timeout: 2 * time.Second,
	}
}

// sampleInfo returns a fully populated Info used by server/client tests.
func sampleInfo(cacheDir string) Info {
	return Info{
		API:     APIInfo{Name: "onecloudriver-control", Version: 1},
		Daemon:  DaemonInfo{PID: 999, GoVersion: "spoofed"}, // must be overridden by the server
		Account: AccountInfo{Name: "test@outlook.com"},
		Mount: MountInfo{
			State:      "running",
			Mountpoint: "/home/test/OneDrive",
			CacheDir:   cacheDir,
		},
		Config: ConfigInfo{
			CacheTTL:           "60s",
			CacheMaxEntries:    2000,
			CacheMaxSize:       "0",
			DeltaInterval:      "5m0s",
			MaxUploadsInFlight: 5,
			MaxUploadRetries:   5,
			GraphRetries:       3,
			HTTPTimeout:        "15s",
			PreWarmDepth:       2,
		},
	}
}

// rawServerCloser closes an ad-hoc HTTP server and removes its socket file.
type rawServerCloser struct {
	srv  *http.Server
	sock string
}

func (c *rawServerCloser) Close() error {
	err := c.srv.Close()
	_ = os.Remove(c.sock)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// newRawStatusServer runs a minimal HTTP server on a Unix socket that answers
// every request with the given status code. Used to exercise client error
// mapping (404/500) without going through the control Server.
func newRawStatusServer(sockPath string, status int) (io.Closer, error) {
	if err := os.Remove(sockPath); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		_ = srv.Serve(ln)
	}()
	return &rawServerCloser{srv: srv, sock: sockPath}, nil
}

// roundTripperFunc adapts a function to http.RoundTripper so tests can inject
// arbitrary transport errors.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
