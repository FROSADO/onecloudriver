package control

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServer_InfoEndpoint(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "control.sock")

	server, err := NewServer(sockPath, &fakeProvider{info: sampleInfo(tmpDir)}, "v0.1.5-test")
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer server.Close()

	waitDial(t, sockPath)

	client := NewClient(sockPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	info, err := client.Info(ctx)
	if err != nil {
		t.Fatalf("client.Info failed: %v", err)
	}

	if info.API.Name != "onecloudriver-control" || info.API.Version != 1 {
		t.Errorf("unexpected API info: %+v", info.API)
	}
	if info.Account.Name != "test@outlook.com" {
		t.Errorf("unexpected account: %q", info.Account.Name)
	}
	if info.Mount.State != "running" || info.Mount.Mountpoint != "/home/test/OneDrive" || info.Mount.CacheDir != tmpDir {
		t.Errorf("unexpected mount info: %+v", info.Mount)
	}

	// The server must own the runtime fields and override anything the
	// provider sets (sampleInfo spoofs PID 999 and GoVersion).
	if info.Daemon.PID != os.Getpid() {
		t.Errorf("expected PID %d, got %d", os.Getpid(), info.Daemon.PID)
	}
	if info.Daemon.GoVersion == "" || info.Daemon.GoVersion == "spoofed" {
		t.Errorf("expected a real GoVersion, got %q", info.Daemon.GoVersion)
	}
	if _, err := time.Parse(time.RFC3339, info.Daemon.StartedAt); err != nil {
		t.Errorf("startedAt is not RFC3339: %q (%v)", info.Daemon.StartedAt, err)
	}
	if info.Daemon.BinaryVersion != "v0.1.5-test" {
		t.Errorf("unexpected binary version: %q", info.Daemon.BinaryVersion)
	}

	if info.Config.CacheTTL != "60s" || info.Config.CacheMaxEntries != 2000 || info.Config.DeltaInterval != "5m0s" {
		t.Errorf("unexpected config info: %+v", info.Config)
	}
}

func TestServer_Disabled(t *testing.T) {
	server, err := NewServer("", &fakeProvider{}, "")
	if err != nil {
		t.Fatalf("NewServer with empty path should not error, got: %v", err)
	}
	if server != nil {
		t.Fatal("NewServer with empty path should return a nil server")
	}
}

func TestServer_NilProvider(t *testing.T) {
	tmpDir := t.TempDir()
	server, err := NewServer(filepath.Join(tmpDir, "control.sock"), nil, "")
	if server != nil {
		t.Fatal("expected nil server for nil provider")
	}
	if err == nil {
		t.Fatal("expected error for nil provider")
	}
}

func TestServer_UnknownRoute404(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "control.sock")

	server, err := NewServer(sockPath, &fakeProvider{info: sampleInfo(tmpDir)}, "")
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer server.Close()

	waitDial(t, sockPath)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/v1/unknown", nil)
	resp, err := rawClient(sockPath).Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for unknown path, got %d", resp.StatusCode)
	}
}

func TestServer_MethodNotAllowed405(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "control.sock")

	server, err := NewServer(sockPath, &fakeProvider{info: sampleInfo(tmpDir)}, "")
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer server.Close()

	waitDial(t, sockPath)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/v1/info", nil)
	resp, err := rawClient(sockPath).Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for POST, got %d", resp.StatusCode)
	}
}

func TestServer_SocketPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "control.sock")

	server, err := NewServer(sockPath, &fakeProvider{info: sampleInfo(tmpDir)}, "")
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer server.Close()

	waitDial(t, sockPath)

	info, err := os.Stat(sockPath)
	if err != nil {
		t.Fatalf("os.Stat failed: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Errorf("expected a socket file, got mode %v", info.Mode())
	}
	if perm := info.Mode().Perm() & 0o077; perm != 0 {
		t.Errorf("expected socket permissions 0600, got %o", info.Mode().Perm())
	}
}

func TestServer_StaleSocketCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "control.sock")

	// A stale regular file where the socket should be (crashed previous run).
	if err := os.WriteFile(sockPath, []byte("stale"), 0o600); err != nil {
		t.Fatalf("creating stale file failed: %v", err)
	}

	server, err := NewServer(sockPath, &fakeProvider{info: sampleInfo(tmpDir)}, "")
	if err != nil {
		t.Fatalf("NewServer failed with stale socket: %v", err)
	}
	defer server.Close()

	waitDial(t, sockPath)

	client := NewClient(sockPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := client.Info(ctx); err != nil {
		t.Fatalf("client.Info failed after stale cleanup: %v", err)
	}
}

func TestServer_Close(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "control.sock")

	server, err := NewServer(sockPath, &fakeProvider{info: sampleInfo(tmpDir)}, "")
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	waitDial(t, sockPath)

	if _, err := os.Stat(sockPath); err != nil {
		t.Fatalf("socket should exist before Close: %v", err)
	}

	if err := server.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Errorf("socket should be removed after Close")
	}

	// Double close is a no-op, not an error.
	if err := server.Close(); err != nil {
		t.Errorf("double Close should not error: %v", err)
	}
}

func TestServer_CloseNilSafe(t *testing.T) {
	var server *Server
	if err := server.Close(); err != nil {
		t.Errorf("Close on nil server should not error: %v", err)
	}

	disabled, err := NewServer("", &fakeProvider{}, "")
	if err != nil || disabled != nil {
		t.Fatalf("expected nil, nil from disabled NewServer, got %v, %v", disabled, err)
	}
	if err := disabled.Close(); err != nil {
		t.Errorf("Close on disabled server should not error: %v", err)
	}
}

func TestServer_Addr(t *testing.T) {
	var nilServer *Server
	if got := nilServer.Addr(); got != "" {
		t.Errorf("Addr on nil server should be empty, got %q", got)
	}

	disabled, _ := NewServer("", &fakeProvider{}, "")
	if disabled != nil {
		if got := disabled.Addr(); got != "" {
			t.Errorf("Addr on disabled server should be empty, got %q", got)
		}
	}

	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "control.sock")
	server, err := NewServer(sockPath, &fakeProvider{info: sampleInfo(tmpDir)}, "")
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer server.Close()

	if got := server.Addr(); got != sockPath {
		t.Errorf("Addr = %q, want %q", got, sockPath)
	}
}

func TestServer_BindError(t *testing.T) {
	// The parent directory does not exist, so the bind must fail.
	sockPath := filepath.Join(t.TempDir(), "missing", "control.sock")
	server, err := NewServer(sockPath, &fakeProvider{info: sampleInfo("")}, "")
	if server != nil {
		t.Fatal("expected nil server on bind failure")
	}
	if err == nil {
		t.Fatal("expected error on bind failure")
	}
}
