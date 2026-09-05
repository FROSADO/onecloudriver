package control

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestClient_Info_Success(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "control.sock")

	server, err := NewServer(sockPath, &fakeProvider{info: sampleInfo(tmpDir)}, "v1.0.0")
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
		t.Fatalf("Info failed: %v", err)
	}
	if info.Account.Name != "test@outlook.com" {
		t.Errorf("unexpected account name: %q", info.Account.Name)
	}
}

func TestClient_Info_NotRunning(t *testing.T) {
	client := NewClient(filepath.Join(t.TempDir(), "does-not-exist.sock"))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := client.Info(ctx)
	if err == nil {
		t.Fatal("expected error for nonexistent socket")
	}
	if !errors.Is(err, ErrNotRunning) {
		t.Errorf("expected ErrNotRunning, got: %v", err)
	}
}

// TestClient_Info_StaleSocket covers a socket file that exists but has no
// listener behind it (a crashed process that did not clean up). Connecting to
// it yields ECONNREFUSED, which the client must report as ErrNotRunning.
func TestClient_Info_StaleSocket(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "stale.sock")

	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("syscall.Socket failed: %v", err)
	}
	defer syscall.Close(fd)

	if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: sockPath}); err != nil {
		t.Fatalf("syscall.Bind failed: %v", err)
	}
	defer os.Remove(sockPath)

	client := NewClient(sockPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = client.Info(ctx)
	if err == nil {
		t.Fatal("expected error for stale socket")
	}
	if !errors.Is(err, ErrNotRunning) {
		t.Errorf("expected ErrNotRunning for stale socket, got: %v", err)
	}
}

func TestClient_Info_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "control.sock")

	srv, err := newRawStatusServer(sockPath, 404)
	if err != nil {
		t.Fatalf("newRawStatusServer failed: %v", err)
	}
	defer srv.Close()

	waitDial(t, sockPath)

	client := NewClient(sockPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = client.Info(ctx)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestClient_Info_ServerError(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "control.sock")

	srv, err := newRawStatusServer(sockPath, 500)
	if err != nil {
		t.Fatalf("newRawStatusServer failed: %v", err)
	}
	defer srv.Close()

	waitDial(t, sockPath)

	client := NewClient(sockPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = client.Info(ctx)
	if err == nil || !strings.Contains(err.Error(), "unexpected status") {
		t.Errorf("expected 'unexpected status' error, got: %v", err)
	}
}

func TestClient_Info_GenericTransportError(t *testing.T) {
	client := NewClient(filepath.Join(t.TempDir(), "unused.sock"))
	client.httpCli.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("boom")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := client.Info(ctx)
	if err == nil || !strings.Contains(err.Error(), "requesting info") {
		t.Errorf("expected 'requesting info' error, got: %v", err)
	}
}
