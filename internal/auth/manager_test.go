package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mockKeyring is a fake implementation of Keyring to simulate errors
type mockKeyring struct {
	failSet bool
	failGet bool
	storage map[string]string
}

func (m *mockKeyring) Get(service, user string) (string, error) {
	if m.failGet {
		return "", errors.New("simulated keyring get error")
	}
	return m.storage[service+":"+user], nil
}

func (m *mockKeyring) Set(service, user, password string) error {
	if m.failSet {
		return errors.New("simulated keyring set error")
	}
	if m.storage == nil {
		m.storage = make(map[string]string)
	}
	m.storage[service+":"+user] = password
	return nil
}

func (m *mockKeyring) Delete(service, user string) error {
	delete(m.storage, service+":"+user)
	return nil
}

// TestManager_AddAccount_Success tests the happy path
func TestManager_AddAccount_Success(t *testing.T) {
	// 1. Prepare isolated environment
	tempDir := t.TempDir() // Go creates and cleans up this directory automatically!
	mockKR := &mockKeyring{}

	manager, err := NewManagerWithDeps(tempDir, mockKR)
	if err != nil {
		t.Fatalf("Error creating manager: %v", err)
	}

	// 2. Create an account manually (simulating what AddAccount would do)
	acc := &Account{
		Name:          "test@outlook.com",
		AccessToken:   "access_123",
		RefreshToken:  "refresh_456",
		ExpiresAt:     9999999999,
		tokenFilePath: filepath.Join(tempDir, "test_outlook_com.json"),
		keyring:       mockKR,
	}
	acc.Config.ApplyDefaults()

	// 3. Save the account
	err = acc.saveUnsafe() // Note: in a real test, we would make a public Save() method
	if err != nil {
		t.Fatalf("Error saving account: %v", err)
	}

	// 4. Verify the manager loaded it correctly
	manager.loadAccounts() // Reload to simulate restart

	retrievedAcc, err := manager.GetAccount("test@outlook.com")
	if err != nil {
		t.Fatalf("Account not found: %v", err)
	}

	if retrievedAcc.RefreshToken != "refresh_456" {
		t.Errorf("Expected refresh token 'refresh_456', got '%s'", retrievedAcc.RefreshToken)
	}
}

// TestManager_KeyringFailure tests what happens if the operating system fails to save the secret
// TestManager_LoadAccounts_RecoversAccessTokenFromKeyring verifies that when
// loading an account from disk, the AccessToken is recovered from the keyring
// (separate key onecloudriver:access:<name>) to allow offline mode startup
// without network.
func TestManager_LoadAccounts_RecoversAccessTokenFromKeyring(t *testing.T) {
	tempDir := t.TempDir()
	mockKR := &mockKeyring{}

	// 1. Save an account with access token and refresh token in the keyring
	acc := &Account{
		Name:          "offline@outlook.com",
		AccessToken:   "access-from-keyring",
		RefreshToken:  "refresh-from-keyring",
		ExpiresAt:     time.Now().Unix() + 3600,
		tokenFilePath: filepath.Join(tempDir, "offline_outlook_com.json"),
		keyring:       mockKR,
	}
	acc.Config.ApplyDefaults()
	if err := acc.saveUnsafe(); err != nil {
		t.Fatalf("saveUnsafe error: %v", err)
	}

	// 2. New manager (simulates process restart) on the same dir+keyring
	manager, err := NewManagerWithDeps(tempDir, mockKR)
	if err != nil {
		t.Fatalf("NewManagerWithDeps error: %v", err)
	}

	retrieved, err := manager.GetAccount("offline@outlook.com")
	if err != nil {
		t.Fatalf("GetAccount error: %v", err)
	}

	// Both tokens should be recovered from the keyring
	if retrieved.AccessToken != "access-from-keyring" {
		t.Errorf("AccessToken expected %q, got %q", "access-from-keyring", retrieved.AccessToken)
	}
	if retrieved.RefreshToken != "refresh-from-keyring" {
		t.Errorf("RefreshToken expected %q, got %q", "refresh-from-keyring", retrieved.RefreshToken)
	}
}

// TestManager_RemoveAccount_PurgesAccessToken from keyring: when removing the
// account, the access token (separate key) should also be deleted from the keyring,
// not just the refresh token.
func TestManager_RemoveAccount_PurgesAccessToken(t *testing.T) {
	tempDir := t.TempDir()
	mockKR := &mockKeyring{}

	acc := &Account{
		Name:          "delete2@outlook.com",
		AccessToken:   "access-to-purge",
		RefreshToken:  "refresh-to-purge",
		ExpiresAt:     9999999999,
		tokenFilePath: filepath.Join(tempDir, "delete2_outlook_com.json"),
		keyring:       mockKR,
	}
	acc.Config.ApplyDefaults()
	if err := acc.saveUnsafe(); err != nil {
		t.Fatalf("saveUnsafe error: %v", err)
	}

	manager, _ := NewManagerWithDeps(tempDir, mockKR)
	if err := manager.RemoveAccount("delete2@outlook.com"); err != nil {
		t.Fatalf("RemoveAccount error: %v", err)
	}

	// Both tokens should have been purged from the keyring
	if tok, _ := mockKR.Get("onecloudriver", "onecloudriver:delete2@outlook.com"); tok != "" {
		t.Errorf("refresh token should be purged, got: %q", tok)
	}
	if tok, _ := mockKR.Get("onecloudriver", "onecloudriver:access:delete2@outlook.com"); tok != "" {
		t.Errorf("access token should be purged, got: %q", tok)
	}
}

func TestManager_KeyringFailure(t *testing.T) {
	tempDir := t.TempDir()
	mockKR := &mockKeyring{failSet: true} // Force the error!

	NewManagerWithDeps(tempDir, mockKR)

	acc := &Account{
		Name:          "fail@outlook.com",
		AccessToken:   "access_123",
		RefreshToken:  "refresh_456",
		ExpiresAt:     9999999999,
		tokenFilePath: filepath.Join(tempDir, "fail_outlook_com.json"),
	}
	acc.Config.ApplyDefaults()

	acc.saveUnsafe()

	// In this case, we expect the function to handle the keyring error
	// (depending on your design, it may return an error or just log a warning).
	// If you decided it's a warning, the JSON file should still be created:
	if _, err := os.Stat(acc.tokenFilePath); os.IsNotExist(err) {

		t.Error("Expected the JSON file to be created even if keyring failed")
	}
}

// TestManager_CorruptJSON tests resilience against damaged files
func TestManager_CorruptJSON(t *testing.T) {
	tempDir := t.TempDir()
	mockKR := &mockKeyring{}

	// Write intentionally invalid JSON
	badFilePath := filepath.Join(tempDir, "bad_outlook_com.json")
	os.WriteFile(badFilePath, []byte("{ this is not valid json }"), 0600)

	manager, err := NewManagerWithDeps(tempDir, mockKR)
	if err != nil {
		t.Fatalf("NewManager should not fail for a corrupt file, it should ignore it: %v", err)
	}

	// The corrupt account should not be in the map
	if _, exists := manager.accounts["bad@outlook.com"]; exists {
		t.Error("Manager should not have loaded an account with corrupt JSON")
	}
}

func TestManager_RemoveAccount(t *testing.T) {
	tempDir := t.TempDir()
	mockKR := &mockKeyring{}

	manager, _ := NewManagerWithDeps(tempDir, mockKR)

	// 1. Create and save an account manually
	acc := &Account{
		Name:          "delete@outlook.com",
		AccessToken:   "access_123",
		RefreshToken:  "refresh_456",
		ExpiresAt:     9999999999,
		tokenFilePath: filepath.Join(tempDir, "delete_outlook_com.json"),
	}
	acc.Config.ApplyDefaults()
	acc.saveUnsafe()
	manager.loadAccounts()

	// 2. Verify it exists
	if _, err := manager.GetAccount("delete@outlook.com"); err != nil {
		t.Fatalf("Account should exist before deleting it")
	}

	// 3. Delete it
	err := manager.RemoveAccount("delete@outlook.com")
	if err != nil {
		t.Fatalf("RemoveAccount failed: %v", err)
	}

	// 4. Verify it no longer exists and the file was deleted
	if _, err := manager.GetAccount("delete@outlook.com"); err == nil {
		t.Error("Expected an error when searching for the deleted account")
	}
	if _, err := os.Stat(acc.tokenFilePath); !os.IsNotExist(err) {
		t.Error("Expected the JSON file to have been deleted")
	}
}

func Test_sanitizeFileName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "simple case", input: "test@outlook.com", want: "test_outlook_com"},
		{name: "name with dot", input: "user.name@domain.com", want: "user_name_domain_com"},
		{name: "no mail", input: "simple", want: "simple"},
		{name: "empty", input: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeFileName(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeFileName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
