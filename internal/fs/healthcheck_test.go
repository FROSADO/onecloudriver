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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/me/drive/root") {
			t.Errorf("expected /me/drive/root, got %s", r.URL.Path)
		}
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

// cancelCtx returns a context that is already cancelled (via DeadlineExceeded).
// This is used to simulate network errors reliably in CI because
// context.DeadlineExceeded implements net.Error with Timeout()==true,
// which isNetworkError detects.
func cancelCtx() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	// Give the deadline a moment to expire
	time.Sleep(5 * time.Millisecond)
	return ctx, cancel
}

func TestHealthCheck_NetworkError_OfflineMode(t *testing.T) {
	// Account with expired token + cancelled context forces Refresh to fail
	// with DeadlineExceeded → isNetworkError → offline tolerated
	acc := &auth.Account{
		Name:         "offline@test.com",
		AccessToken:  "cached-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().Unix() - 10, // expired
	}

	graphClient := graph.NewClient(graph.WithRetry(0))

	ctx, cancel := cancelCtx()
	defer cancel()

	err := healthCheck(ctx, acc, graphClient)
	if err != nil {
		t.Fatalf("healthCheck should tolerate network errors (offline mode), got: %v", err)
	}
}

func TestHealthCheck_GraphNetworkError_OfflineMode(t *testing.T) {
	// Account with valid token (no Refresh needed).
	// Cancelled context makes GetItem fail → offline tolerated.
	acc := &auth.Account{
		Name:        "graph-offline@test.com",
		AccessToken: "valid-token",
		ExpiresAt:   time.Now().Unix() + 3600,
	}

	graphClient := graph.NewClient(graph.WithRetry(0))

	ctx, cancel := cancelCtx()
	defer cancel()

	err := healthCheck(ctx, acc, graphClient)
	if err != nil {
		t.Fatalf("healthCheck should tolerate Graph network errors (offline mode), got: %v", err)
	}
}

func TestHealthCheck_TokenNetworkError_NoCachedToken(t *testing.T) {
	// Account with NO access token + expired + cancelled context.
	// Refresh fails with "no connection and no access token in memory"
	// which isNetworkError does not detect → healthCheck fails.
	// This is expected: without a cached token, offline mode cannot work.
	acc := &auth.Account{
		Name:         "no-token@test.com",
		AccessToken:  "",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().Unix() - 10,
	}

	graphClient := graph.NewClient(graph.WithRetry(0))

	ctx, cancel := cancelCtx()
	defer cancel()

	err := healthCheck(ctx, acc, graphClient)
	if err == nil {
		t.Fatal("healthCheck should fail when no cached token and refresh fails")
	}
	if !strings.Contains(err.Error(), "could not obtain access token") {
		t.Errorf("expected 'could not obtain access token', got: %v", err)
	}
}

func TestHealthCheck_AuthError(t *testing.T) {
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
