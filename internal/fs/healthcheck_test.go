package fs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/auth"
	"github.com/frosado/onecloudriver/internal/graph"
)

// =============================================================================
// healthCheck — Integration tests with httptest mock Graph API
// =============================================================================

// Note: Most tests use auth.Account directly as a TokenProvider (it implements
// types.TokenProvider via GetAccessToken). The Account struct is set up with
// pre-configured tokens and expiry times to control the refresh flow.

// =============================================================================

func TestHealthCheck_Success(t *testing.T) {
	// Mock Graph API returning a valid DriveItem (200 OK)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/me/drive/root") {
			t.Errorf("expected /me/drive/root, got %s", r.URL.Path)
		}
		// Verify that the Authorization header is present
		if auth := r.Header.Get("Authorization"); auth != "Bearer valid-token" {
			t.Errorf("expected Authorization header, got: %s", auth)
		}

		resp := graph.DriveItem{
			ID:   "root",
			Name: "root",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Account with a valid token that won't expire soon
	acc := &auth.Account{
		Name:        "test@outlook.com",
		AccessToken: "valid-token",
		ExpiresAt:   time.Now().Unix() + 3600,
	}

	graphClient := graph.NewClient(
		graph.WithBaseURL(server.URL),
		graph.WithHTTPClient(server.Client()),
	)

	err := healthCheck(context.Background(), acc, graphClient)
	if err != nil {
		t.Fatalf("healthCheck should succeed, got: %v", err)
	}
}

func TestHealthCheck_NetworkError_OfflineMode(t *testing.T) {
	// Account with expired token, will trigger GetAccessToken → Refresh
	// Refresh will hit a non-routable address → network error → offline tolerated
	acc := &auth.Account{
		Name:         "offline@test.com",
		AccessToken:  "cached-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().Unix() - 10, // expired: triggers Refresh
		Config:       auth.AuthConfig{TokenURL: "http://127.0.0.1:1/token"},
	}

	// No graph server needed — the network error in GetAccessToken should
	// short-circuit before we even reach Graph.
	graphClient := graph.NewClient()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := healthCheck(ctx, acc, graphClient)
	if err != nil {
		t.Fatalf("healthCheck should tolerate network errors (offline mode), got: %v", err)
	}
}

func TestHealthCheck_AuthError(t *testing.T) {
	// Mock Graph API returning 401 Unauthorized
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{
				"code":    "InvalidAuthenticationToken",
				"message": "Access token has expired.",
			},
		})
	}))
	defer server.Close()

	acc := &auth.Account{
		Name:        "bad@test.com",
		AccessToken: "expired-token",
		ExpiresAt:   time.Now().Unix() + 3600,
	}

	graphClient := graph.NewClient(
		graph.WithBaseURL(server.URL),
		graph.WithHTTPClient(server.Client()),
	)

	err := healthCheck(context.Background(), acc, graphClient)
	if err == nil {
		t.Fatal("healthCheck should fail with auth error")
	}
	if !strings.Contains(err.Error(), "token verification against Microsoft Graph failed") {
		t.Errorf("expected token verification error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "re-authenticate with") {
		t.Errorf("expected re-authentication instructions in error: %v", err)
	}
}

func TestHealthCheck_GraphNetworkError_OfflineMode(t *testing.T) {
	// Mock Graph API that returns a network-like error (502 + connection info)
	// isNetworkError checks for certain patterns, but a 5xx won't trigger it.
	// Instead, we use a non-routable address for the Graph server.
	acc := &auth.Account{
		Name:        "graph-offline@test.com",
		AccessToken: "valid-token",
		ExpiresAt:   time.Now().Unix() + 3600,
	}

	graphClient := graph.NewClient(
		graph.WithBaseURL("http://127.0.0.1:1"), // non-routable
	)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := healthCheck(ctx, acc, graphClient)
	if err != nil {
		t.Fatalf("healthCheck should tolerate Graph network errors (offline mode), got: %v", err)
	}
}

func TestHealthCheck_TokenNetworkError_OfflineTolerated(t *testing.T) {
	// Account with NO access token in memory + expired → Refresh fails
	// with network error → isNetworkError true → offline mode tolerated
	// (healthCheck returns nil even without cached token — the offline
	// fallback relies on cache to serve previously-downloaded content)
	acc := &auth.Account{
		Name:         "no-token@test.com",
		AccessToken:  "", // no cached token
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().Unix() - 10, // expired
		Config:       auth.AuthConfig{TokenURL: "http://127.0.0.1:1/token"},
	}

	graphClient := graph.NewClient()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := healthCheck(ctx, acc, graphClient)
	if err != nil {
		t.Fatalf("healthCheck should tolerate offline mode even without cached token, got: %v", err)
	}
}
