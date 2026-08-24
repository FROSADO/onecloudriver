// Package obs provides lightweight, dependency-free observability for the
// mount daemon: structured counters exposed via expvar and a local debug HTTP
// server serving /debug/vars and /debug/pprof (net/http/pprof).
//
// It deliberately avoids a full telemetry stack (Prometheus, OpenTelemetry,
// ...): this is a desktop single-user tool (PLAN_1 §8.4), so the goal is just
// enough introspection to troubleshoot a running mount by curling the local
// endpoint. Nothing is served unless the user explicitly enables it with
// `mount --debug` (or --debug-addr on a custom loopback/interface address).
package obs

import (
	"expvar"
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof" //nolint:gosec // G108: pprof is intentionally exposed, but only on the opt-in loopback --debug server (issue #74)

	"time"

	"github.com/rs/zerolog/log"
)

// Register publishes a lazily-evaluated counter under name. fn is called on
// every /debug/vars scrape, so counters always reflect live state (no stale
// snapshots). Registering the same name twice replaces the previous value, as
// with expvar.Publish.
func Register(name string, fn func() any) {
	expvar.Publish(name, expvar.Func(fn))
}

// StartDebugServer serves expvar (/debug/vars) and pprof (/debug/pprof) on
// addr. The address is bound here (rather than inside the goroutine) so a bind
// failure surfaces as an error to the caller instead of being silently ignored.
// The accepted address is returned (resolved to the real bound endpoint, which
// matters when passing a port 0) and the listener is closed when the returned
// function is called, so tests can start and tear down servers deterministically.
//
// The default address is the loopback interface (127.0.0.1:6060) so the debug
// endpoint is never exposed on a public interface unless the user explicitly
// passes a different --debug-addr. The HTTP server configures a ReadHeaderTimeout
// to bound Slowloris-style queries (gosec G112/G114).
func StartDebugServer(addr string) (string, func(), error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return "", nil, fmt.Errorf("debug server: could not listen on %s: %w", addr, err)
	}

	server := &http.Server{
		Handler:           http.DefaultServeMux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Info().
		Str("addr", listener.Addr().String()).
		Msg("Debug server: expvar on /debug/vars and pprof on /debug/pprof")

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Warn().Err(err).Str("addr", listener.Addr().String()).Msg("Debug server terminated")
		}
	}()

	// stop closes the listener, causing Serve to return; the HTTP server is
	// closed too so idle connections do not linger.
	stop := func() {
		_ = listener.Close()
		_ = server.Close()
	}

	return listener.Addr().String(), stop, nil
}
