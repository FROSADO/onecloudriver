package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// AddAccount — Integration test with httptest mock endpoints
// =============================================================================

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

	// Config with port 1 (privileged) so getAuthCodeLocalServer fails → headless
	config := AuthConfig{
		TokenURL:    tokenServer.URL + "/token",
		ClientID:    "test-client-id",
		RedirectURL: "http://127.0.0.1:1/callback",
		CodeURL:     tokenServer.URL + "/authorize",
	}

	// Simulate headless: user pastes the redirect URL with auth code
	fakeRedirectURL := "http://127.0.0.1:1/callback?code=mock-auth-code"
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

func TestAddAccount_TokenExchangeError(t *testing.T) {
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

	fakeRedirectURL := "http://127.0.0.1:1/callback?code=bad-code"
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
