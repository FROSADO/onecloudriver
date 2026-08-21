package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTokenServer returns a fake /token endpoint that reports the
// code_verifier it received to the given callback.
func newTokenServer(t *testing.T, onVerifier func(string)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		onVerifier(r.PostFormValue("code_verifier"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"a","refresh_token":"r","expires_in":3600}`))
	}))
}

// TestGetAuthCodeLocalServer_RejectsForeignState verifies that a callback
// which does not carry this login's state is discarded: the code must never
// reach the token exchange, so the flow keeps waiting.
func TestGetAuthCodeLocalServer_RejectsForeignState(t *testing.T) {
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
	resultCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		code, err := getAuthCodeLocalServer(config, session)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- code
	}()

	injectedURL := fmt.Sprintf("http://%s/callback?code=ATTACKER_CODE&state=not-my-state", port)
	var resp *http.Response
	var dialErr error
	for i := 0; i < 60; i++ {
		resp, dialErr = http.Get(injectedURL) //nolint:gosec // test-only, URL from verified port
		if dialErr == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if dialErr != nil {
		t.Fatalf("callback request failed: %v", dialErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected HTTP 400 for mismatched state, got %d", resp.StatusCode)
	}

	select {
	case code := <-resultCh:
		t.Fatalf("injected code was accepted: %q", code)
	case err := <-errCh:
		t.Fatalf("flow aborted instead of ignoring the callback: %v", err)
	case <-time.After(300 * time.Millisecond):
		// Expected: the flow keeps waiting for the legitimate callback.
	}
}

func TestGetAuthCodeLocalServer_RejectsNonLoopbackRedirect(t *testing.T) {
	config := AuthConfig{
		ClientID:    "test-client-id",
		RedirectURL: "http://192.168.1.10:9090/callback",
		CodeURL:     "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
	}

	_, err := getAuthCodeLocalServer(config, mustAuthSession(t))
	if err == nil {
		t.Fatal("expected error for non-loopback redirect_uri")
	}
	if !strings.Contains(err.Error(), "not a loopback address") {
		t.Errorf("expected loopback error, got: %v", err)
	}
}

func TestGetAuthCodeHeadless_RejectsForeignState(t *testing.T) {
	config := AuthConfig{
		ClientID:    "test-client-id",
		RedirectURL: "http://localhost:9090/callback",
		CodeURL:     "https://example.com/authorize",
	}

	input := strings.NewReader("http://localhost:9090/callback?code=ATTACKER_CODE&state=not-my-state\n")
	_, err := getAuthCodeHeadless(config, mustAuthSession(t), input)
	if err == nil {
		t.Fatal("expected error for mismatched state")
	}
	if !strings.Contains(err.Error(), "'state' parameter does not match") {
		t.Errorf("expected state mismatch error, got: %v", err)
	}
}

func TestGetAuthCodeHeadless_RejectsUnexpectedRedirect(t *testing.T) {
	config := AuthConfig{
		ClientID:    "test-client-id",
		RedirectURL: "http://localhost:9090/callback",
		CodeURL:     "https://example.com/authorize",
	}

	session := mustAuthSession(t)
	input := strings.NewReader("https://evil.example.com/callback?code=ATTACKER_CODE&state=" + session.state + "\n")
	_, err := getAuthCodeHeadless(config, session, input)
	if err == nil {
		t.Fatal("expected error for URL that is not the configured redirect")
	}
	if !strings.Contains(err.Error(), "does not match the expected redirect") {
		t.Errorf("expected redirect mismatch error, got: %v", err)
	}
}

func TestNewAuthSession_UniqueAndPKCE(t *testing.T) {
	first := mustAuthSession(t)
	second := mustAuthSession(t)

	if first.state == second.state || first.verifier == second.verifier {
		t.Error("auth sessions must not repeat state or verifier")
	}
	if len(first.state) < 32 || len(first.verifier) < 43 {
		t.Errorf("state/verifier too short: %d/%d", len(first.state), len(first.verifier))
	}
	if first.challenge == first.verifier {
		t.Error("code_challenge must be the hash of the verifier, not the verifier itself")
	}
	if !first.matchesState(first.state) || first.matchesState(second.state) {
		t.Error("matchesState must accept only its own state")
	}
}

func TestExchangeCodeForTokens_SendsCodeVerifier(t *testing.T) {
	var gotVerifier string
	server := newTokenServer(t, func(verifier string) {
		gotVerifier = verifier
	})
	defer server.Close()

	config := AuthConfig{
		TokenURL:    server.URL,
		ClientID:    "test-id",
		RedirectURL: "http://localhost:9090/callback",
	}

	if _, _, _, err := exchangeCodeForTokens(t.Context(), config, "code", "my-verifier"); err != nil {
		t.Fatalf("exchangeCodeForTokens failed: %v", err)
	}
	if gotVerifier != "my-verifier" {
		t.Errorf("expected code_verifier to be sent, got %q", gotVerifier)
	}
}

func TestErrorHTML_EscapesUntrustedText(t *testing.T) {
	page := errorHTML("Authentication error", `<script>alert("x")</script>`)

	if strings.Contains(page, "<script>") {
		t.Errorf("errorHTML must escape untrusted markup: %s", page)
	}
	if !strings.Contains(page, "&lt;script&gt;") {
		t.Errorf("expected escaped markup in page: %s", page)
	}
}
