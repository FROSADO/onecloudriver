package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
)

// =============================================================================
// AddAccount — Integration test with httptest mock endpoints
// =============================================================================

// findAvailablePort finds a free TCP port on localhost for testing.
func findAvailablePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("findAvailablePort: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return fmt.Sprintf("127.0.0.1:%d", port)
}

// setupMockEndpoints creates two httptest servers for token and Graph API.
func setupMockEndpoints(t *testing.T) (tokenServer, graphServer *httptest.Server) {
	t.Helper()

	graphServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/me") {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":                "user-abc-123",
				"userPrincipalName": "test@outlook.com",
				"displayName":       "Test User",
			})
			return
		}
		http.NotFound(w, r)
	}))

	tokenServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "test-access-token-xxx",
			"refresh_token": "test-refresh-token-yyy",
			"expires_in":    3600,
		})
	}))

	return tokenServer, graphServer
}

// graphRouter intercepts HTTP requests to graph.microsoft.com and redirects
// them to a mock server. All other requests pass through to the fallback.
type graphRouter struct {
	graphURL string
	fallback http.RoundTripper
}

func (g *graphRouter) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.Host, "graph.microsoft.com") {
		// Clone the request and redirect to mock
		newReq := req.Clone(req.Context())
		newReq.URL.Scheme = "http"
		// Extract host:port from graphURL
		u := g.graphURL
		if strings.HasPrefix(u, "http://") {
			u = u[7:]
		} else if strings.HasPrefix(u, "https://") {
			u = u[8:]
		}
		newReq.URL.Host = u
		return g.fallback.RoundTrip(newReq)
	}
	return g.fallback.RoundTrip(req)
}

func TestAddAccount_HeadlessFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full AddAccount flow in CI/short mode (slow with -race)")
	}
	tokenServer, graphServer := setupMockEndpoints(t)
	defer tokenServer.Close()
	defer graphServer.Close()

	// Override http.DefaultTransport to intercept Graph API calls
	origTransport := http.DefaultTransport
	http.DefaultTransport = &graphRouter{graphURL: graphServer.URL, fallback: origTransport}
	t.Cleanup(func() { http.DefaultTransport = origTransport })

	m, err := NewManagerWithDeps(t.TempDir(), &mockKeyring{})
	if err != nil {
		t.Fatalf("NewManagerWithDeps: %v", err)
	}

	// graph.NewClient now uses its own tuned transport (issue #70), which
	// bypasses the http.DefaultTransport override above. Inject a client that
	// routes graph.microsoft.com to the mock server instead.
	m.SetGraphClientFactory(func() *graph.Client {
		return graph.NewClient(graph.WithHTTPClient(
			&http.Client{Transport: &graphRouter{graphURL: graphServer.URL, fallback: origTransport}},
		))
	})

	// Config with port 1 (privileged) so getAuthCodeLocalServer fails → headless
	config := AuthConfig{
		TokenURL:    tokenServer.URL + "/token",
		ClientID:    "test-client-id",
		RedirectURL: "http://127.0.0.1:1/callback",
		CodeURL:     tokenServer.URL + "/authorize",
	}

	// Pin the login session so the pasted redirect can echo a valid state
	session := mustAuthSession(t)
	originalSessionFunc := newAuthSessionFunc
	newAuthSessionFunc = func() (*authSession, error) { return session, nil }
	t.Cleanup(func() { newAuthSessionFunc = originalSessionFunc })

	// Simulate headless: user pastes the redirect URL with auth code
	fakeRedirectURL := "http://127.0.0.1:1/callback?code=mock-auth-code&state=" + session.state
	input := strings.NewReader(fakeRedirectURL + "\n")

	ctx := context.Background()
	acc, err := m.AddAccount(ctx, config, true, input)
	if err != nil {
		t.Fatalf("AddAccount failed: %v", err)
	}

	if acc == nil {
		t.Fatal("AddAccount returned nil account")
	}
	if acc.Name != "test@outlook.com" {
		t.Errorf("expected test@outlook.com, got %s", acc.Name)
	}
	if acc.AccessToken != "test-access-token-xxx" {
		t.Errorf("expected test-access-token-xxx, got %s", acc.AccessToken)
	}
	if acc.RefreshToken != "test-refresh-token-yyy" {
		t.Errorf("expected test-refresh-token-yyy, got %s", acc.RefreshToken)
	}

	_, err = m.GetAccount("test@outlook.com")
	if err != nil {
		t.Errorf("account not found in manager: %v", err)
	}
}

// =============================================================================
// getAuthCodeLocalServer — Local server callback flow
// =============================================================================
// Note: the success-path is covered by TestGetAuthCodeLocalServer_ReceivesCode
// in flow_test.go. The tests below cover error and edge-case paths.

func TestGetAuthCodeLocalServer_ErrorCallback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping server startup test in CI/short mode (slow with -race)")
	}
	port := findAvailablePort(t)
	config := AuthConfig{
		ClientID:    "test-client-id",
		RedirectURL: fmt.Sprintf("http://%s/callback", port),
		CodeURL:     "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
	}

	session := mustAuthSession(t)
	errCh := make(chan error, 1)
	go func() {
		_, err := getAuthCodeLocalServer(config, session)
		errCh <- err
	}()

	// Wait for server readiness with retry loop
	callbackURL := fmt.Sprintf("http://%s/callback?error_description=access_denied&state=%s", port, session.state)
	var resp *http.Response
	var dialErr error
	for i := 0; i < 60; i++ {
		resp, dialErr = http.Get(callbackURL) //nolint:gosec // test-only, URL from verified port
		if dialErr == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if dialErr != nil {
		t.Fatalf("callback request failed: %v", dialErr)
	}
	resp.Body.Close() // drain and close before server shuts down

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error from error callback")
		}
		if !strings.Contains(err.Error(), "microsoft returned error") {
			t.Errorf("expected microsoft error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for error")
	}
}

func TestGetAuthCodeLocalServer_NoCode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping server startup test in CI/short mode (slow with -race)")
	}
	port := findAvailablePort(t)
	config := AuthConfig{
		ClientID:    "test-client-id",
		RedirectURL: fmt.Sprintf("http://%s/callback", port),
		CodeURL:     "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
	}

	session := mustAuthSession(t)
	errCh := make(chan error, 1)
	go func() {
		_, err := getAuthCodeLocalServer(config, session)
		errCh <- err
	}()

	// Wait for server readiness with retry loop
	callbackURL := fmt.Sprintf("http://%s/callback?state=%s", port, session.state)
	var resp *http.Response
	var dialErr error
	for i := 0; i < 60; i++ {
		resp, dialErr = http.Get(callbackURL) //nolint:gosec // test-only, URL from verified port
		if dialErr == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if dialErr != nil {
		t.Fatalf("callback request failed: %v", dialErr)
	}
	resp.Body.Close() // drain and close before server shuts down

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error for callback without code")
		}
		if !strings.Contains(err.Error(), "authorization code not received") {
			t.Errorf("expected 'authorization code not received', got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for error")
	}
}

func TestGetAuthCodeLocalServer_InvalidRedirectURI(t *testing.T) {
	config := AuthConfig{
		RedirectURL: "://invalid",
	}

	_, err := getAuthCodeLocalServer(config, mustAuthSession(t))
	if err == nil {
		t.Fatal("expected error for invalid redirect URI")
	}
	if !strings.Contains(err.Error(), "invalid redirect_uri") {
		t.Errorf("expected 'invalid redirect_uri', got: %v", err)
	}
}

func TestGetAuthCodeLocalServer_CannotBind(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test that relies on privileged port (port 1 may be available in CI root containers)")
	}
	// Port 1 is privileged on Linux — should fail to bind
	config := AuthConfig{
		ClientID:    "test-client-id",
		RedirectURL: "http://127.0.0.1:1/callback",
		CodeURL:     "https://example.com/authorize",
	}

	_, err := getAuthCodeLocalServer(config, mustAuthSession(t))
	if err == nil {
		t.Fatal("expected error when cannot bind to port")
	}
	if !strings.Contains(err.Error(), "could not start local server") {
		t.Errorf("expected 'could not start local server', got: %v", err)
	}
}

// =============================================================================
// getAuthCodeHeadless — Edge cases
// =============================================================================

func TestGetAuthCodeHeadless_NoCodeInURL(t *testing.T) {
	config := AuthConfig{
		ClientID:    "test-client-id",
		RedirectURL: "http://localhost:9090/callback",
		CodeURL:     "https://example.com/authorize",
	}

	session := mustAuthSession(t)

	// User pastes a URL without ?code=
	input := strings.NewReader("http://localhost:9090/callback?state=" + session.state + "\n")
	_, err := getAuthCodeHeadless(config, session, input)
	if err == nil {
		t.Fatal("expected error when code is missing from URL")
	}
	if !strings.Contains(err.Error(), "code' parameter not found") {
		t.Errorf("expected 'code parameter not found', got: %v", err)
	}
}

func TestGetAuthCodeHeadless_EmptyInput(t *testing.T) {
	config := AuthConfig{
		ClientID:    "test-client-id",
		RedirectURL: "http://localhost:9090/callback",
		CodeURL:     "https://example.com/authorize",
	}

	// User provides empty input (just newline)
	input := strings.NewReader("\n")
	_, err := getAuthCodeHeadless(config, mustAuthSession(t), input)
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

// =============================================================================
// AddAccount — Full flow tests
// =============================================================================

func TestAddAccount_TokenExchangeError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full AddAccount flow in CI/short mode (slow with -race)")
	}
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error_description": "the code is invalid or expired",
		})
	}))
	defer tokenServer.Close()

	m, err := NewManagerWithDeps(t.TempDir(), &mockKeyring{})
	if err != nil {
		t.Fatalf("NewManagerWithDeps: %v", err)
	}

	// Override http.DefaultTransport to intercept Graph API calls
	origTransport := http.DefaultTransport
	http.DefaultTransport = &graphRouter{graphURL: tokenServer.URL, fallback: origTransport}
	t.Cleanup(func() { http.DefaultTransport = origTransport })

	config := AuthConfig{
		TokenURL:    tokenServer.URL + "/token",
		ClientID:    "test-client-id",
		RedirectURL: "http://127.0.0.1:1/callback",
	}

	session := mustAuthSession(t)
	originalSessionFunc := newAuthSessionFunc
	newAuthSessionFunc = func() (*authSession, error) { return session, nil }
	t.Cleanup(func() { newAuthSessionFunc = originalSessionFunc })

	fakeRedirectURL := "http://127.0.0.1:1/callback?code=bad-code&state=" + session.state
	input := strings.NewReader(fakeRedirectURL + "\n")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = m.AddAccount(ctx, config, true, input)
	if err == nil {
		t.Fatal("expected error for bad code exchange")
	}
	if !strings.Contains(err.Error(), "failed exchanging code") {
		t.Errorf("expected 'failed exchanging code', got: %v", err)
	}
}
