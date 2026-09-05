package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"
)

// Server is an HTTP server listening on a Unix socket for local control.
type Server struct {
	mu        sync.Mutex
	httpSrv   *http.Server
	sockPath  string
	provider  InfoProvider
	startedAt time.Time
	binaryVer string
}

// NewServer creates and starts a new control server on the given Unix socket
// path. If sockPath is empty the server is disabled (returns nil, nil).
// The provider supplies state information for the /v1/info endpoint.
func NewServer(sockPath string, provider InfoProvider, binaryVersion string) (*Server, error) {
	if sockPath == "" {
		return nil, nil // disabled
	}
	if provider == nil {
		return nil, fmt.Errorf("control server: provider must not be nil")
	}

	// Remove a stale socket file left by a previous, crashed process. A live
	// instance cannot share this path: the cache dir is held by an exclusive
	// BoltDB lock, so removing the file is safe.
	if err := os.Remove(sockPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("removing stale socket: %w", err)
	}

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("listening on unix socket %s: %w", sockPath, err)
	}

	// The socket inherits the process umask on bind, so force owner-only
	// permissions explicitly: only the user who ran the mount may connect.
	if err := os.Chmod(sockPath, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("setting socket permissions: %w", err)
	}

	s := &Server{
		sockPath:  sockPath,
		provider:  provider,
		startedAt: time.Now(),
		binaryVer: binaryVersion,
	}

	s.httpSrv = &http.Server{
		Handler:           s.mux(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Serve in the background. Serve returns http.ErrServerClosed on a normal
	// Close(), which is not an error worth reporting. The server is captured in
	// a local variable: Close() may nil the s.httpSrv field, and the goroutine
	// must keep serving on the original instance until Shutdown returns.
	httpSrv := s.httpSrv
	go func() {
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "control server error: %v\n", err)
		}
	}()

	return s, nil
}

// Close shuts down the server and removes the socket file. It is idempotent
// and safe to call on a nil receiver (disabled server).
func (s *Server) Close() error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.httpSrv == nil {
		return nil // already closed
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := s.httpSrv.Shutdown(ctx)
	s.httpSrv = nil
	if osErr := os.Remove(s.sockPath); osErr != nil && !os.IsNotExist(osErr) && err == nil {
		err = osErr
	}
	if err != nil {
		return fmt.Errorf("shutting down control server: %w", err)
	}
	return nil
}

// Addr returns the Unix socket path.
func (s *Server) Addr() string {
	if s == nil {
		return ""
	}
	return s.sockPath
}

func (s *Server) mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/info", s.handleInfo)
	mux.HandleFunc("/", s.handleNotFound)
	return mux
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	info := s.provider.Info()
	// The runtime fields are owned by the server: the provider cannot spoof them.
	info.Daemon.PID = os.Getpid()
	info.Daemon.StartedAt = s.startedAt.UTC().Format(time.RFC3339)
	info.Daemon.GoVersion = runtime.Version()
	info.Daemon.BinaryVersion = s.binaryVer

	body, err := json.Marshal(info)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to encode response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *Server) handleNotFound(w http.ResponseWriter, _ *http.Request) {
	writeJSONError(w, http.StatusNotFound, "not found")
}

// writeJSONError writes a JSON {"error":{"message":...}} body with the given
// status. Encoding failures after WriteHeader are intentionally ignored.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	body, err := json.Marshal(map[string]any{
		"error": map[string]string{"message": message},
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
