package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// ExampleAuthConfig_ApplyDefaults demonstrates how to fill in the default
// values in an OAuth configuration.
func ExampleAuthConfig_ApplyDefaults() {
	cfg := AuthConfig{}
	cfg.ApplyDefaults()

	fmt.Println(cfg.ClientID != "")
	fmt.Println(cfg.CodeURL != "")
	fmt.Println(cfg.TokenURL != "")

	// Output:
	// true
	// true
	// true
}

// TestAccount_Refresh_Success tests successful token renewal
func TestAccount_Refresh_Success(t *testing.T) {
	// 1. Create a fake HTTP server that simulates Microsoft
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request is correct
		if r.URL.Path != "/token" {
			t.Errorf("Expected path /token, got %s", r.URL.Path)
		}

		// Respond with a valid JSON of new tokens
		response := map[string]interface{}{
			"access_token":  "new_access_token_xyz",
			"refresh_token": "new_refresh_token_abc",
			"expires_in":    3600,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close() // Ensure the server shuts down at test end

	// 2. Configure the account to point to our fake server
	acc := &Account{
		Name:          "test@outlook.com",
		AccessToken:   "old_access_token",
		RefreshToken:  "valid_refresh_token",
		ExpiresAt:     time.Now().Unix() - 10,     // Force it to be expired!
		tokenFilePath: t.TempDir() + "/test.json", // Temporary file
	}
	acc.Config.TokenURL = mockServer.URL + "/token" // Here's the magic!

	// 3. Execute the refresh
	err := acc.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh failed unexpectedly: %v", err)
	}

	// 4. Verify that tokens were updated
	if acc.AccessToken != "new_access_token_xyz" {
		t.Errorf("Token not updated. Expected: new_access_token_xyz, Got: %s", acc.AccessToken)
	}
}

// TestAccount_Refresh_MicrosoftError tests how it handles a Microsoft error (e.g.: revoked token)
func TestAccount_Refresh_MicrosoftError(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate Microsoft error response (HTTP 400)
		w.WriteHeader(http.StatusBadRequest)
		errorResp := map[string]string{
			"error":             "invalid_grant",
			"error_description": "The refresh token has expired or been revoked.",
		}
		json.NewEncoder(w).Encode(errorResp)
	}))
	defer mockServer.Close()

	acc := &Account{
		Name:          "test@outlook.com",
		RefreshToken:  "expired_refresh_token",
		ExpiresAt:     time.Now().Unix() - 10,
		Config:        AuthConfig{TokenURL: mockServer.URL + "/token"},
		tokenFilePath: t.TempDir() + "/test.json",
	}

	err := acc.Refresh(context.Background())

	// We expect the function to return an error, not panic or ignore it
	if err == nil {
		t.Fatal("Expected an error when renewing with invalid token, but got nil")
	}

	// Verify the error contains useful information
	if err.Error() == "" {
		t.Error("The returned error is empty")
	}
}

func TestAccount_GetAccessToken(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "fresh_token_from_getaccess",
			"refresh_token": "new_refresh",
			"expires_in":    3600,
		})
	}))
	defer mockServer.Close()

	acc := &Account{
		Name:          "test@outlook.com",
		AccessToken:   "old_token",
		RefreshToken:  "valid_refresh",
		ExpiresAt:     time.Now().Unix() - 10, // Expired
		Config:        AuthConfig{TokenURL: mockServer.URL + "/token"},
		tokenFilePath: t.TempDir() + "/test.json",
	}

	// Call the high-level method
	token, err := acc.GetAccessToken(context.Background())

	if err != nil {
		t.Fatalf("GetAccessToken failed: %v", err)
	}
	if token != "fresh_token_from_getaccess" {
		t.Errorf("Incorrect token. Expected: fresh_token_from_getaccess, Got: %s", token)
	}
}

// TestAccount_SaveUnsafe_DoesNotPersistAccessToken (S1) verifies that the
// AccessToken is never written to the disk JSON file, even when the account
// has a valid one in memory. Only Name, Config and ExpiresAt should persist;
// ExpiresAt alone is not sensitive without the token that accompanies it.
//
// The AccessToken IS saved to the OS keyring (separate key
// onecloudriver:access:<name>), so that a restart without network can start
// in offline mode while the token is still valid — see
// TestAccount_SaveUnsafe_PersistsAccessTokenInKeyring.
func TestAccount_SaveUnsafe_DoesNotPersistAccessToken(t *testing.T) {
	mockKR := &mockKeyring{}
	tokenFile := t.TempDir() + "/test.json"

	acc := &Account{
		Name:          "secret@outlook.com",
		AccessToken:   "super-secret-access-token-must-not-leak",
		RefreshToken:  "super-secret-refresh-token-must-not-leak",
		ExpiresAt:     time.Now().Unix() + 3600,
		tokenFilePath: tokenFile,
		keyring:       mockKR,
	}
	acc.Config.ApplyDefaults()

	if err := acc.saveUnsafe(); err != nil {
		t.Fatalf("saveUnsafe failed: %v", err)
	}

	raw, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatalf("no se pudo leer el archivo de cuenta: %v", err)
	}

	if strings.Contains(string(raw), "super-secret-access-token-must-not-leak") {
		t.Error("the AccessToken leaked to the disk JSON file")
	}
	if strings.Contains(string(raw), "super-secret-refresh-token-must-not-leak") {
		t.Error("the RefreshToken leaked to the disk JSON file")
	}
	if strings.Contains(string(raw), "access_token") {
		t.Error("the JSON file contains the access_token key; it should not be serialized at all")
	}

	// The RefreshToken did reach the keyring (where it belongs).
	stored, _ := mockKR.Get("onecloudriver", "onecloudriver:secret@outlook.com")
	if stored != "super-secret-refresh-token-must-not-leak" {
		t.Errorf("the RefreshToken was not saved correctly in the keyring, got: %q", stored)
	}
}

// TestAccount_SaveUnsafe_PersistsAccessTokenInKeyring (S1 + modo offline)
// verifica que el AccessToken se guarda en el keyring bajo la clave separada
// onecloudriver:access:<name> (allowing offline startup with a still-valid token
// and that it NEVER ends up in the on-disk JSON.
func TestAccount_SaveUnsafe_PersistsAccessTokenInKeyring(t *testing.T) {
	mockKR := &mockKeyring{}
	tokenFile := t.TempDir() + "/test.json"

	acc := &Account{
		Name:          "offline@outlook.com",
		AccessToken:   "access-token-for-offline",
		RefreshToken:  "refresh-token-for-offline",
		ExpiresAt:     time.Now().Unix() + 3600,
		tokenFilePath: tokenFile,
		keyring:       mockKR,
	}
	acc.Config.ApplyDefaults()

	if err := acc.saveUnsafe(); err != nil {
		t.Fatalf("saveUnsafe failed: %v", err)
	}

	// AccessToken in the keyring with a separate key
	stored, _ := mockKR.Get("onecloudriver", "onecloudriver:access:offline@outlook.com")
	if stored != "access-token-for-offline" {
		t.Errorf("the AccessToken should persist in the keyring, got: %q", stored)
	}

	// ...but never in the disk JSON
	raw, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatalf("could not read the account file: %v", err)
	}
	if strings.Contains(string(raw), "access-token-for-offline") {
		t.Error("the AccessToken leaked to the disk JSON file")
	}
}

// TestAccount_GetAccessToken_OfflineWithValidKeyringToken simula el arranque
// del modo offline: la cuenta se carga desde disco con el AccessToken
// recovered from the keyring (still valid) and without network available. GetAccessToken
// debe devolver el token sin intentar renovar (0 llamadas HTTP).
func TestAccount_GetAccessToken_OfflineWithValidKeyringToken(t *testing.T) {
	var called bool
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "should-not-need-refresh",
			"refresh_token": "new_refresh",
			"expires_in":    3600,
		})
	}))
	defer mockServer.Close()

	acc := &Account{
		Name:          "offline-start@outlook.com",
		AccessToken:   "valid-access-token-from-keyring", // recuperado del keyring al cargar
		RefreshToken:  "valid-refresh-from-keyring",
		ExpiresAt:     time.Now().Unix() + 1800, // sigue vigente 30 min
		Config:        AuthConfig{TokenURL: mockServer.URL + "/token"},
		tokenFilePath: t.TempDir() + "/test.json",
	}

	token, err := acc.GetAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAccessToken without network and with valid keyring token should not fail: %v", err)
	}
	if called {
		t.Error("the token server should not be called: the keyring's access token is still valid")
	}
	if token != "valid-access-token-from-keyring" {
		t.Errorf("incorrect token, got: %q", token)
	}
}

// TestAccount_Refresh_LoadedFromDisk_ForcesRefresh (S1) simula el escenario
// of cold startup: an account loaded from disk has ExpiresAt in the
// future (it was saved while the AccessToken was still valid) but AccessToken
// empty in memory, because it is no longer persisted. Refresh must detect this and
// renew instead of short-circuiting by returning an empty token.
func TestAccount_Refresh_LoadedFromDisk_ForcesRefresh(t *testing.T) {
	var called bool
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "rehydrated_access_token",
			"refresh_token": "rehydrated_refresh_token",
			"expires_in":    3600,
		})
	}))
	defer mockServer.Close()

	acc := &Account{
		Name:          "coldstart@outlook.com",
		AccessToken:   "", // as if loaded from disk after a restart
		RefreshToken:  "valid_refresh_from_keyring",
		ExpiresAt:     time.Now().Unix() + 3600, // "valid" according to what was saved, but no token in memory
		Config:        AuthConfig{TokenURL: mockServer.URL + "/token"},
		tokenFilePath: t.TempDir() + "/test.json",
	}

	token, err := acc.GetAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAccessToken failed: %v", err)
	}
	if !called {
		t.Error("expected Refresh to call the token server because AccessToken was empty in memory")
	}
	if token != "rehydrated_access_token" {
		t.Errorf("incorrect token after rehydration, got: %q", token)
	}
}

// TestAccount_Refresh_InvalidGrant_PurgesKeyring (S6) verifica que cuando
// Microsoft rechaza el refresh con invalid_grant, el RefreshToken se purga
// from both memory and the keyring, instead of staying there for infinite
// retries that will never succeed.
func TestAccount_Refresh_InvalidGrant_PurgesKeyring(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error":             "invalid_grant",
			"error_description": "The refresh token has expired or been revoked.",
		})
	}))
	defer mockServer.Close()

	mockKR := &mockKeyring{}
	keyringID := "onecloudriver:revoked@outlook.com"
	mockKR.Set("onecloudriver", keyringID, "revoked_refresh_token")

	acc := &Account{
		Name:          "revoked@outlook.com",
		RefreshToken:  "revoked_refresh_token",
		ExpiresAt:     time.Now().Unix() - 10,
		Config:        AuthConfig{TokenURL: mockServer.URL + "/token"},
		tokenFilePath: t.TempDir() + "/test.json",
		keyring:       mockKR,
	}

	err := acc.Refresh(context.Background())
	if err == nil {
		t.Fatal("expected an invalid_grant error")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("the error does not mention invalid_grant: %v", err)
	}

	if acc.RefreshToken != "" {
		t.Errorf("the RefreshToken should have been purged from memory, got: %q", acc.RefreshToken)
	}

	remaining, _ := mockKR.Get("onecloudriver", keyringID)
	if remaining != "" {
		t.Errorf("the RefreshToken should have been purged from keyring, got: %q", remaining)
	}
}

// TestAccount_Refresh_InteractionRequired_PurgesKeyring (S6) es el mismo
// case as above but with the interaction_required code (MFA/re-required consent)
// which Microsoft also uses to invalidate the refresh token.
func TestAccount_Refresh_InteractionRequired_PurgesKeyring(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error":             "interaction_required",
			"error_description": "MFA re-authentication required.",
		})
	}))
	defer mockServer.Close()

	mockKR := &mockKeyring{}
	keyringID := "onecloudriver:mfa@outlook.com"
	mockKR.Set("onecloudriver", keyringID, "mfa_refresh_token")

	acc := &Account{
		Name:          "mfa@outlook.com",
		RefreshToken:  "mfa_refresh_token",
		ExpiresAt:     time.Now().Unix() - 10,
		Config:        AuthConfig{TokenURL: mockServer.URL + "/token"},
		tokenFilePath: t.TempDir() + "/test.json",
		keyring:       mockKR,
	}

	err := acc.Refresh(context.Background())
	if err == nil {
		t.Fatal("expected an interaction_required error")
	}

	remaining, _ := mockKR.Get("onecloudriver", keyringID)
	if remaining != "" {
		t.Errorf("the RefreshToken should have been purged from keyring, got: %q", remaining)
	}
}
