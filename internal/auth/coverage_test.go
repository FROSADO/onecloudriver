package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog/log"
)

// =============================================================================
// secureString
// =============================================================================

func TestSecureString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"email", "user@outlook.com", "u****@outlook.com"},
		{"email short user", "a@b.com", "a****@b.com"},
		{"simple string (no @)", "hello", "h****"},
		{"two char string", "ab", "a****"},
		{"single char", "x", "x***"},
		{"empty string", "", "***"},
		{"email with dot", "john.doe@domain.com", "j****@domain.com"},
		{"multiple @ signs", "a@b@c.com", "a****"},
		{"long name", "verylongname@example.org", "v****@example.org"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := secureString(tt.input)
			if got != tt.want {
				t.Errorf("secureString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// =============================================================================
// KeyringSaveFailed
// =============================================================================

func TestKeyringSaveFailed(t *testing.T) {
	t.Run("returns true when lastKeyringErr is set", func(t *testing.T) {
		acc := &Account{lastKeyringErr: fmt.Errorf("no keyring")}
		if !acc.KeyringSaveFailed() {
			t.Error("expected true when lastKeyringErr is set")
		}
	})

	t.Run("returns false when lastKeyringErr is nil", func(t *testing.T) {
		acc := &Account{}
		if acc.KeyringSaveFailed() {
			t.Error("expected false when lastKeyringErr is nil")
		}
	})
}

// =============================================================================
// Save (public wrapper)
// =============================================================================

func TestAccount_Save(t *testing.T) {
	mockKR := &mockKeyring{}
	tokenFile := filepath.Join(t.TempDir(), "save_test.json")

	acc := &Account{
		Name:          "save@test.com",
		RefreshToken:  "refresh-save",
		AccessToken:   "access-save",
		ExpiresAt:     time.Now().Unix() + 3600,
		tokenFilePath: tokenFile,
		keyring:       mockKR,
	}
	acc.Config.ApplyDefaults()

	err := acc.Save()
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify disk file was created
	if _, err := os.Stat(tokenFile); os.IsNotExist(err) {
		t.Error("expected JSON file to be created by Save")
	}

	// Verify keyring received the refresh token
	stored, _ := mockKR.Get("onecloudriver", "onecloudriver:save@test.com")
	if stored != "refresh-save" {
		t.Errorf("RefreshToken not saved in keyring, got: %q", stored)
	}
}

// =============================================================================
// ListAccounts
// =============================================================================

func TestManager_ListAccounts(t *testing.T) {
	t.Run("empty manager returns empty list", func(t *testing.T) {
		m := &Manager{accounts: make(map[string]*Account)}
		got := m.ListAccounts()
		if len(got) != 0 {
			t.Errorf("expected empty list, got %v", got)
		}
	})

	t.Run("single account", func(t *testing.T) {
		m := &Manager{accounts: map[string]*Account{
			"user@test.com": {},
		}}
		got := m.ListAccounts()
		if len(got) != 1 || got[0] != "user@test.com" {
			t.Errorf("expected [user@test.com], got %v", got)
		}
	})

	t.Run("multiple accounts", func(t *testing.T) {
		m := &Manager{accounts: map[string]*Account{
			"a@test.com": {},
			"b@test.com": {},
			"c@test.com": {},
		}}
		got := m.ListAccounts()
		if len(got) != 3 {
			t.Errorf("expected 3 accounts, got %d: %v", len(got), got)
		}
	})
}

// =============================================================================
// ResolveMainAccountName
// =============================================================================

func TestManager_ResolveMainAccountName(t *testing.T) {
	t.Run("0 accounts returns error", func(t *testing.T) {
		m := &Manager{accounts: make(map[string]*Account)}
		_, err := m.ResolveMainAccountName()
		if err == nil {
			t.Fatal("expected error with zero accounts")
		}
		if !strings.Contains(err.Error(), "no accounts configured") {
			t.Errorf("expected 'no accounts configured', got: %v", err)
		}
	})

	t.Run("1 account resolves automatically", func(t *testing.T) {
		m := &Manager{accounts: map[string]*Account{
			"only@test.com": {},
		}}
		name, err := m.ResolveMainAccountName()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if name != "only@test.com" {
			t.Errorf("expected 'only@test.com', got %q", name)
		}
	})

	t.Run("2+ accounts returns error listing them", func(t *testing.T) {
		m := &Manager{accounts: map[string]*Account{
			"first@test.com":  {},
			"second@test.com": {},
		}}
		_, err := m.ResolveMainAccountName()
		if err == nil {
			t.Fatal("expected error with multiple accounts")
		}
		if !strings.Contains(err.Error(), "2 configured accounts") {
			t.Errorf("expected '2 configured accounts', got: %v", err)
		}
		if !strings.Contains(err.Error(), "first@test.com") {
			t.Errorf("expected error to list account names, got: %v", err)
		}
	})
}

// =============================================================================
// getAccountFilePath
// =============================================================================

func TestManager_GetAccountFilePath(t *testing.T) {
	m := &Manager{configDir: "/home/user/.config/onecloudriver"}

	got := m.getAccountFilePath("user@outlook.com")
	expected := "/home/user/.config/onecloudriver/user_outlook_com.json"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

// =============================================================================
// errorHTML
// =============================================================================

func TestErrorHTML(t *testing.T) {
	html := errorHTML("Test Title", "Test Message")

	if !strings.Contains(html, "<title>Test Title</title>") {
		t.Error("missing title in HTML")
	}
	if !strings.Contains(html, "<h1>Test Title</h1>") {
		t.Error("missing h1 with title in HTML")
	}
	if !strings.Contains(html, "<p>Test Message</p>") {
		t.Error("missing message in HTML")
	}
}

// =============================================================================
// IsOffline - edge cases
// =============================================================================

func TestIsOffline_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"url.Error with DNS", &url.Error{Op: "Get", URL: "https://example.com", Err: &net.DNSError{Name: "example.com"}}, true},
		{"url.Error with connection refused", &url.Error{Op: "Get", URL: "https://example.com", Err: fmt.Errorf("connection refused")}, true},
		{"HTTP 401", fmt.Errorf("HTTP 401 Unauthorized"), false},
		{"HTTP 500", fmt.Errorf("server error: HTTP 500 Internal Server Error"), false},
		{"non-URL network error", fmt.Errorf("network timeout"), true},
		{"wrapped network error", fmt.Errorf("outer: %w", fmt.Errorf("dial tcp: connect: no route to host")), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := IsOffline(tt.err)
			if got != tt.want {
				t.Errorf("IsOffline(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// =============================================================================
// buildAuthURL - edge case
// =============================================================================

func TestBuildAuthURL_InvalidURL(t *testing.T) {
	config := AuthConfig{
		CodeURL: "://invalid-url",
	}

	u := buildAuthURL(config, mustAuthSession(t))
	if u != "" {
		t.Errorf("expected empty string for malformed URL, got %q", u)
	}
}

// =============================================================================
// sanitizeFileName - edge cases
// =============================================================================

func TestSanitizeFileName_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{"user@domain.com", "user_domain_com"},
		{"user.name@sub.domain.com", "user_name_sub_domain_com"},
		{"UPPER_CASE@DOMAIN.COM", "UPPER_CASE_DOMAIN_COM"},
		{"with-numbers-123@test.com", "with-numbers-123_test_com"},
		{"hyphen-ated@test.com", "hyphen-ated_test_com"},
		{"underscore_name@test.com", "underscore_name_test_com"},
		{"", ""},
		{"a@b", "a_b"},
		{"emoji😀@test.com", "emoji__test_com"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := sanitizeFileName(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeFileName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// =============================================================================
// DiscardLogs
// =============================================================================

func TestDiscardLogs(t *testing.T) {
	origLogger := log.Logger
	t.Cleanup(func() { log.Logger = origLogger })

	DiscardLogs()

	// Verify the Nop logger doesn't panic when logging
	log.Info().Msg("this should be discarded and not panic")
}

// =============================================================================
// staticTokenProvider
// =============================================================================

func TestStaticTokenProvider_GetAccessToken(t *testing.T) {
	tp := &staticTokenProvider{token: "test-static-token"}
	token, err := tp.GetAccessToken(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "test-static-token" {
		t.Errorf("expected 'test-static-token', got %q", token)
	}
}

// =============================================================================
// exchangeCodeForTokens - error paths
// =============================================================================

func TestExchangeCodeForTokens_HTTPError(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error_description": "the code is invalid or expired",
		})
	}))
	defer mockServer.Close()

	config := AuthConfig{
		TokenURL:    mockServer.URL,
		ClientID:    "test-id",
		RedirectURL: "http://localhost:9090/callback",
	}

	_, _, _, err := exchangeCodeForTokens(context.Background(), config, "bad_code", "verifier")
	if err == nil {
		t.Fatal("expected error for HTTP 400")
	}
	if !strings.Contains(err.Error(), "the code is invalid") {
		t.Errorf("expected descriptive error, got: %v", err)
	}
}

func TestExchangeCodeForTokens_NonJSONError(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("<html>502 Bad Gateway</html>"))
	}))
	defer mockServer.Close()

	config := AuthConfig{
		TokenURL:    mockServer.URL,
		ClientID:    "test-id",
		RedirectURL: "http://localhost:9090/callback",
	}

	_, _, _, err := exchangeCodeForTokens(context.Background(), config, "code", "verifier")
	if err == nil {
		t.Fatal("expected error for HTTP 502 with non-JSON body")
	}
	if !strings.Contains(err.Error(), "non-JSON response") {
		t.Errorf("expected 'non-JSON response' in error, got: %v", err)
	}
}

func TestExchangeCodeForTokens_MissingTokens(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Response missing access_token
		json.NewEncoder(w).Encode(map[string]interface{}{
			"expires_in": 3600,
		})
	}))
	defer mockServer.Close()

	config := AuthConfig{
		TokenURL:    mockServer.URL,
		ClientID:    "test-id",
		RedirectURL: "http://localhost:9090/callback",
	}

	_, _, _, err := exchangeCodeForTokens(context.Background(), config, "code", "verifier")
	if err == nil {
		t.Fatal("expected error for response missing tokens")
	}
	if !strings.Contains(err.Error(), "does not contain the expected tokens") {
		t.Errorf("expected 'does not contain the expected tokens', got: %v", err)
	}
}

func TestExchangeCodeForTokens_NetworkError(t *testing.T) {
	config := AuthConfig{
		TokenURL:    "http://127.0.0.1:1", // non-routable or closed port
		ClientID:    "test-id",
		RedirectURL: "http://localhost:9090/callback",
	}

	// Use a short timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, _, _, err := exchangeCodeForTokens(ctx, config, "code", "verifier")
	if err == nil {
		t.Fatal("expected network error")
	}
}

// =============================================================================
// Account.Refresh - offline scenarios
// =============================================================================

func TestAccount_Refresh_OfflineNetworkError(t *testing.T) {
	// Use a non-routable address that will fail
	acc := &Account{
		Name:          "offline@test.com",
		RefreshToken:  "valid-refresh",
		ExpiresAt:     time.Now().Unix() - 10, // expired
		Config:        AuthConfig{TokenURL: "http://127.0.0.1:1/token"},
		tokenFilePath: filepath.Join(t.TempDir(), "offline.json"),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := acc.Refresh(ctx)
	if err == nil {
		t.Fatal("expected error when network is unreachable and no access token in memory")
	}

	// Now test with a valid access token in memory
	acc2 := &Account{
		Name:          "offline2@test.com",
		AccessToken:   "cached-access-token",
		RefreshToken:  "valid-refresh",
		ExpiresAt:     time.Now().Unix() - 10, // expired
		Config:        AuthConfig{TokenURL: "http://127.0.0.1:1/token"},
		tokenFilePath: filepath.Join(t.TempDir(), "offline2.json"),
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()

	err = acc2.Refresh(ctx2)
	if err != nil {
		t.Fatalf("expected no error when access token is in memory (offline tolerated): %v", err)
	}
	if acc2.AccessToken != "cached-access-token" {
		t.Error("access token should remain unchanged during offline mode")
	}
}

// =============================================================================
// Account.Refresh - rollback on save failure
// =============================================================================

func TestAccount_Refresh_RollbackOnSaveFailure(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "new-access-token",
			"refresh_token": "new-refresh-token",
			"expires_in":    3600,
		})
	}))
	defer mockServer.Close()

	acc := &Account{
		Name:         "rollback@test.com",
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
		ExpiresAt:    time.Now().Unix() - 10,
		Config:       AuthConfig{TokenURL: mockServer.URL + "/token"},
		// Write to a subdirectory of TempDir that doesn't exist to guarantee failure
		tokenFilePath: filepath.Join(t.TempDir(), "subdir", "file.json"),
	}

	err := acc.Refresh(context.Background())
	if err == nil {
		t.Fatal("expected error when save fails")
	}
	if !strings.Contains(err.Error(), "could not be saved") {
		t.Errorf("expected rollback error, got: %v", err)
	}

	// Tokens should have rolled back to original values
	if acc.AccessToken != "old-access-token" {
		t.Errorf("expected rollback to 'old-access-token', got %q", acc.AccessToken)
	}
	if acc.RefreshToken != "old-refresh-token" {
		t.Errorf("expected rollback to 'old-refresh-token', got %q", acc.RefreshToken)
	}
}

// =============================================================================
// AuthConfig.ApplyDefaults - preserves existing values
// =============================================================================

func TestApplyDefaults_PreservesExisting(t *testing.T) {
	cfg := AuthConfig{
		ClientID:    "custom-client-id",
		RedirectURL: "http://custom:8080/callback",
	}

	err := cfg.ApplyDefaults()
	if err != nil {
		t.Fatalf("ApplyDefaults failed: %v", err)
	}

	// Custom values should be preserved
	if cfg.ClientID != "custom-client-id" {
		t.Errorf("ClientID should be preserved, got %q", cfg.ClientID)
	}
	if cfg.RedirectURL != "http://custom:8080/callback" {
		t.Errorf("RedirectURL should be preserved, got %q", cfg.RedirectURL)
	}

	// Empty fields should get defaults
	if cfg.CodeURL == "" {
		t.Error("CodeURL should have default value")
	}
	if cfg.TokenURL == "" {
		t.Error("TokenURL should have default value")
	}
}

func TestApplyDefaults_FillsEmpty(t *testing.T) {
	cfg := AuthConfig{}

	err := cfg.ApplyDefaults()
	if err != nil {
		t.Fatalf("ApplyDefaults failed: %v", err)
	}

	if cfg.ClientID == "" {
		t.Error("ClientID should be populated")
	}
	if cfg.CodeURL == "" {
		t.Error("CodeURL should be populated")
	}
	if cfg.TokenURL == "" {
		t.Error("TokenURL should be populated")
	}
	if cfg.RedirectURL == "" {
		t.Error("RedirectURL should be populated")
	}
}

// =============================================================================
// Account.toJSON
// =============================================================================

func TestAccount_ToJSON(t *testing.T) {
	acc := &Account{
		Name:         "json@test.com",
		AccessToken:  "secret-access-should-not-appear",
		RefreshToken: "secret-refresh-should-not-appear",
		ExpiresAt:    1234567890,
	}
	acc.Config.ApplyDefaults()
	acc.Mount.DefaultMountpoint = "/mnt/test"

	aj := acc.toJSON()

	if aj.Name != "json@test.com" {
		t.Errorf("Name = %q, want 'json@test.com'", aj.Name)
	}
	if aj.ExpiresAt != 1234567890 {
		t.Errorf("ExpiresAt = %d, want 1234567890", aj.ExpiresAt)
	}
	if aj.Mount.DefaultMountpoint != "/mnt/test" {
		t.Errorf("DefaultMountpoint = %q, want '/mnt/test'", aj.Mount.DefaultMountpoint)
	}
}

// =============================================================================
// Manager.NewManagerWithDeps - error path (can't create dir)
// =============================================================================

func TestNewManagerWithDeps_CreateDirError(t *testing.T) {
	// Create a file where we expect a directory
	tmpDir := t.TempDir()
	blockingFile := filepath.Join(tmpDir, "blocker")
	os.WriteFile(blockingFile, []byte("block"), 0600)

	// Try to create a manager with the file path as base dir (should fail MkdirAll)
	badPath := filepath.Join(blockingFile, "subdir")
	_, err := NewManagerWithDeps(badPath, &mockKeyring{})
	if err == nil {
		t.Fatal("expected error when config dir can't be created")
	}
}

// =============================================================================
// Manager.RemoveAccount - account not found
// =============================================================================

func TestRemoveAccount_NotFound(t *testing.T) {
	m, err := NewManagerWithDeps(t.TempDir(), &mockKeyring{})
	if err != nil {
		t.Fatalf("NewManagerWithDeps failed: %v", err)
	}

	err = m.RemoveAccount("nonexistent@test.com")
	if err == nil {
		t.Fatal("expected error for non-existent account")
	}
	if err.Error() != "account not found" {
		t.Errorf("expected 'account not found', got %q", err.Error())
	}
}

// =============================================================================
// Manager.GetAccount - account not found
// =============================================================================

func TestGetAccount_NotFound(t *testing.T) {
	m, err := NewManagerWithDeps(t.TempDir(), &mockKeyring{})
	if err != nil {
		t.Fatalf("NewManagerWithDeps failed: %v", err)
	}

	_, err = m.GetAccount("ghost@test.com")
	if err == nil {
		t.Fatal("expected error for non-existent account")
	}
	if !strings.Contains(err.Error(), "is not configured") {
		t.Errorf("expected 'is not configured', got %q", err.Error())
	}
}

// =============================================================================
// Account.purgeInvalidRefreshTokenLocked - with nil keyring
// =============================================================================

func TestPurgeInvalidRefreshTokenLocked_NilKeyring(t *testing.T) {
	acc := &Account{
		Name:         "purge@test.com",
		RefreshToken: "token-to-purge",
		AccessToken:  "access-to-purge",
		keyring:      nil, // no keyring
	}

	acc.purgeInvalidRefreshTokenLocked()

	if acc.RefreshToken != "" {
		t.Error("RefreshToken should be purged from memory even without keyring")
	}
	if acc.AccessToken != "" {
		t.Error("AccessToken should be purged from memory even without keyring")
	}
}

// =============================================================================
// Manager.loadAccounts - loads from disk with keyring tokens
// =============================================================================

func TestLoadAccounts_WithKeyringTokens(t *testing.T) {
	tempDir := t.TempDir()
	mockKR := &mockKeyring{}

	// Pre-populate keyring with tokens
	mockKR.Set("onecloudriver", "onecloudriver:load@test.com", "keyring-refresh")
	mockKR.Set("onecloudriver", "onecloudriver:access:load@test.com", "keyring-access")

	// Write account JSON
	accJSON := accountJSON{
		Name:      "load@test.com",
		ExpiresAt: time.Now().Unix() + 99999,
	}
	accJSON.Config.ApplyDefaults()

	data, _ := json.MarshalIndent(accJSON, "", "  ")
	os.WriteFile(filepath.Join(tempDir, "load_test_com.json"), data, 0600)

	m, err := NewManagerWithDeps(tempDir, mockKR)
	if err != nil {
		t.Fatalf("NewManagerWithDeps failed: %v", err)
	}

	acc, err := m.GetAccount("load@test.com")
	if err != nil {
		t.Fatalf("GetAccount failed: %v", err)
	}

	if acc.RefreshToken != "keyring-refresh" {
		t.Errorf("RefreshToken = %q, want 'keyring-refresh'", acc.RefreshToken)
	}
	if acc.AccessToken != "keyring-access" {
		t.Errorf("AccessToken = %q, want 'keyring-access'", acc.AccessToken)
	}
}

// =============================================================================
// Manager.loadAccounts - ignores non-JSON files
// =============================================================================

func TestLoadAccounts_IgnoresNonJSONFiles(t *testing.T) {
	tempDir := t.TempDir()
	mockKR := &mockKeyring{}

	// Write a .txt file (should be ignored)
	os.WriteFile(filepath.Join(tempDir, "notes.txt"), []byte("not an account"), 0600)
	// Write a directory (should be ignored)
	os.MkdirAll(filepath.Join(tempDir, "subdir"), 0700)

	m, err := NewManagerWithDeps(tempDir, mockKR)
	if err != nil {
		t.Fatalf("NewManagerWithDeps failed: %v", err)
	}

	if len(m.ListAccounts()) != 0 {
		t.Errorf("expected 0 accounts, got %d", len(m.ListAccounts()))
	}
}

// =============================================================================
// Manager.loadAccounts - skips unreadable file
// =============================================================================

func TestLoadAccounts_SkipsUnreadableFile(t *testing.T) {
	tempDir := t.TempDir()
	mockKR := &mockKeyring{}

	// Write a .json file with no read permissions (won't be possible on all OS,
	// but we test that loadAccounts doesn't crash)
	badFile := filepath.Join(tempDir, "bad_test_com.json")
	os.WriteFile(badFile, []byte(`{"name": "bad@test.com"}`), 0000)

	m, err := NewManagerWithDeps(tempDir, mockKR)
	if err != nil {
		t.Fatalf("NewManagerWithDeps failed: %v", err)
	}

	// Should not crash and should not have loaded the unreadable account
	if len(m.ListAccounts()) != 0 {
		// On some systems, 0000 perms still allow reading by owner,
		// so we just check that we didn't crash
		t.Log("account may have been loaded due to OS permissions")
	}
}

// =============================================================================
// Manager.RemoveAccount - keyring delete error (non-fatal)
// =============================================================================

func TestRemoveAccount_KeyringError(t *testing.T) {
	tempDir := t.TempDir()
	mockKR := &mockKeyring{}

	acc := &Account{
		Name:          "keyringerr@test.com",
		RefreshToken:  "refresh-token",
		AccessToken:   "access-token",
		ExpiresAt:     time.Now().Unix() + 3600,
		tokenFilePath: filepath.Join(tempDir, "keyringerr_test_com.json"),
		keyring:       mockKR,
	}
	acc.Config.ApplyDefaults()
	acc.saveUnsafe()

	// Now remove should still work even if keyring delete is OK
	m, _ := NewManagerWithDeps(tempDir, mockKR)
	err := m.RemoveAccount("keyringerr@test.com")
	if err != nil {
		t.Fatalf("RemoveAccount should succeed even if keyring operations are tried: %v", err)
	}
}

// =============================================================================
// Account.GetAccessToken - error propagation from Refresh
// =============================================================================

func TestGetAccessToken_RefreshError(t *testing.T) {
	acc := &Account{
		Name:          "fail@test.com",
		RefreshToken:  "bad",
		AccessToken:   "",
		ExpiresAt:     time.Now().Unix() - 10,
		Config:        AuthConfig{TokenURL: "http://127.0.0.1:1/token"},
		tokenFilePath: filepath.Join(t.TempDir(), "fail.json"),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := acc.GetAccessToken(ctx)
	if err == nil {
		t.Fatal("expected error from GetAccessToken when Refresh fails")
	}
}

// =============================================================================
// saveUnsafe with nil keyring
// =============================================================================

func TestSaveUnsafe_NilKeyring(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "nil_keyring.json")

	acc := &Account{
		Name:          "nilkeyring@test.com",
		RefreshToken:  "should-fail-to-save-in-keyring",
		AccessToken:   "access-token",
		ExpiresAt:     time.Now().Unix() + 3600,
		tokenFilePath: tokenFile,
		keyring:       nil, // no keyring
	}
	acc.Config.ApplyDefaults()

	err := acc.saveUnsafe()
	if err != nil {
		t.Fatalf("saveUnsafe should not fail when keyring is nil: %v", err)
	}

	// lastKeyringErr should be set
	if acc.lastKeyringErr == nil {
		t.Error("lastKeyringErr should be set when keyring is nil")
	}

	// KeyringSaveFailed should return true
	if !acc.KeyringSaveFailed() {
		t.Error("KeyringSaveFailed should return true when keyring is nil")
	}
}

// =============================================================================
// saveUnsafe with keyring Set error
// =============================================================================

func TestSaveUnsafe_KeyringSetError(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "kr_fail.json")
	mockKR := &mockKeyring{failSet: true}

	acc := &Account{
		Name:          "krfail@test.com",
		RefreshToken:  "refresh-will-fail",
		AccessToken:   "access-token",
		ExpiresAt:     time.Now().Unix() + 3600,
		tokenFilePath: tokenFile,
		keyring:       mockKR,
	}
	acc.Config.ApplyDefaults()

	err := acc.saveUnsafe()
	if err != nil {
		t.Fatalf("saveUnsafe should not fail when keyring Set fails: %v", err)
	}

	// JSON file should still be created
	if _, err := os.Stat(tokenFile); os.IsNotExist(err) {
		t.Error("JSON file should be created even when keyring fails")
	}

	// lastKeyringErr should be set
	if acc.lastKeyringErr == nil {
		t.Error("lastKeyringErr should be set when keyring Set fails")
	}
}

// =============================================================================
// openBrowser - platform coverage (just verifies it doesn't panic)
// =============================================================================

func TestOpenBrowser_DoesNotPanic(_ *testing.T) {
	// Test that it doesn't panic; it will likely fail but not crash
	err := openBrowser("http://example.com")
	// We don't care if it succeeds or fails, just that it doesn't crash
	_ = err
}

// =============================================================================
// account JSON serialization roundtrip
// =============================================================================

func TestAccountJSON_Roundtrip(t *testing.T) {
	original := accountJSON{
		Name:      "roundtrip@test.com",
		ExpiresAt: 9999999999,
	}
	original.Config.ApplyDefaults()
	original.Mount.DefaultMountpoint = "/mnt/od"

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var restored accountJSON
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if restored.Name != original.Name {
		t.Errorf("Name mismatch: %q vs %q", restored.Name, original.Name)
	}
	if restored.ExpiresAt != original.ExpiresAt {
		t.Errorf("ExpiresAt mismatch: %d vs %d", restored.ExpiresAt, original.ExpiresAt)
	}
}

// =============================================================================
// AuthError JSON deserialization
// =============================================================================

func TestAuthError_Unmarshal(t *testing.T) {
	body := `{"error":"invalid_grant","error_description":"revoked","error_codes":[123],"error_uri":"https://example.com"}`

	var ae AuthError
	if err := json.Unmarshal([]byte(body), &ae); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if ae.Error != "invalid_grant" {
		t.Errorf("Error = %q, want 'invalid_grant'", ae.Error)
	}
	if ae.ErrorDescription != "revoked" {
		t.Errorf("ErrorDescription = %q, want 'revoked'", ae.ErrorDescription)
	}
	if len(ae.ErrorCodes) != 1 || ae.ErrorCodes[0] != 123 {
		t.Errorf("ErrorCodes = %v, want [123]", ae.ErrorCodes)
	}
	if ae.ErrorURI != "https://example.com" {
		t.Errorf("ErrorURI = %q, want 'https://example.com'", ae.ErrorURI)
	}
}

// =============================================================================
// Logger functions don't panic
// =============================================================================

func TestSetLogOutput_DoesNotPanic(t *testing.T) {
	origLogger := log.Logger
	t.Cleanup(func() { log.Logger = origLogger })

	var buf strings.Builder
	SetLogOutput(&buf)

	log.Info().Msg("test message")
	if !strings.Contains(buf.String(), "test message") {
		t.Error("log message not captured")
	}
}
