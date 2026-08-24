package obs

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

// findFreePort reserves and releases a loopback TCP port, returning it so the
// caller can bind it via StartDebugServer. There is a small race (another
// process could grab the port between close and bind), but for local tests it
// is negligible and far simpler than threading a listener through the API.
func findFreePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestStartDebugServer_ServesCounters(t *testing.T) {
	Register("test_counter", func() any { return 42 })
	Register("test_string", func() any { return "hello" })

	addr, stop, err := StartDebugServer(findFreePort(t))
	if err != nil {
		t.Fatalf("StartDebugServer: %v", err)
	}
	defer stop()

	resp, err := http.Get("http://" + addr + "/debug/vars")
	if err != nil {
		t.Fatalf("GET /debug/vars: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var vars map[string]any
	if err := json.Unmarshal(body, &vars); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, body)
	}

	if got, ok := vars["test_counter"].(float64); !ok || got != 42 {
		t.Errorf("test_counter = %v (%T), want 42", vars["test_counter"], vars["test_counter"])
	}
	if got, ok := vars["test_string"].(string); !ok || got != "hello" {
		t.Errorf("test_string = %v (%T), want hello", vars["test_string"], vars["test_string"])
	}
}

func TestStartDebugServer_ServesPprof(t *testing.T) {
	addr, stop, err := StartDebugServer(findFreePort(t))
	if err != nil {
		t.Fatalf("StartDebugServer: %v", err)
	}
	defer stop()

	resp, err := http.Get("http://" + addr + "/debug/pprof/")
	if err != nil {
		t.Fatalf("GET /debug/pprof/: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/debug/pprof/ status = %d, want 200", resp.StatusCode)
	}
}

func TestStartDebugServer_LoopsBackByDefault(t *testing.T) {
	// The default address documented for --debug-addr is loopback, but this
	// verifies that an explicit default-ish address resolves to the loopback
	// interface only (never 0.0.0.0/: all-interfaces).
	for _, addr := range []string{"127.0.0.1:0", "localhost:0"} {
		bound, stop, err := StartDebugServer(addr)
		if err != nil {
			t.Fatalf("StartDebugServer(%s): %v", addr, err)
		}
		host, _, _ := strings.Cut(bound, ":")
		if host != "127.0.0.1" && host != "::1" && host != "localhost" {
			t.Errorf("StartDebugServer(%s) bound host %q, want loopback only", addr, host)
		}
		stop()
	}
}

func TestStartDebugServer_PropagatesBindFailure(t *testing.T) {
	// Occupy a port, then try to start the debug server on it: it must fail.
	free := findFreePort(t)
	blocker, err := net.Listen("tcp", free)
	if err != nil {
		t.Fatalf("net.Listen(blocker): %v", err)
	}
	defer blocker.Close()

	if _, _, err := StartDebugServer(free); err == nil {
		t.Fatal("StartDebugServer on an occupied port should fail")
	}
}
