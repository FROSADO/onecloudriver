// Package auth manages OAuth2 authentication with Microsoft Identity Platform,
// secure token storage (keyring + disk), and automatic refresh of expired tokens.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"dario.cat/mergo"
	"github.com/rs/zerolog/log"
)

const (
	authClientID    = "a074b377-e82d-41a0-b7b3-88c57c011510"                           //#nosec G101 -- public Microsoft client_id, not a private credential
	authCodeURL     = "https://login.microsoftonline.com/common/oauth2/v2.0/authorize" //#nosec G101 -- public OAuth authorization endpoint
	authTokenURL    = "https://login.microsoftonline.com/common/oauth2/v2.0/token"     //#nosec G101 -- public OAuth token endpoint
	authRedirectURL = "http://localhost:9090/callback"                                 //#nosec G101 -- redirect_uri for desktop app with local server
)

// AuthConfig contains the OAuth2 configuration parameters for authenticating
// against Microsoft Identity Platform. If any field is empty, ApplyDefaults
// fills it with the application's default values.
type AuthConfig struct {
	ClientID    string `json:"clientID"`
	CodeURL     string `json:"codeURL"`
	TokenURL    string `json:"tokenURL"`
	RedirectURL string `json:"redirectURL"`
}

// ApplyDefaults fills empty AuthConfig fields with the application's default
// values (official client_id, Microsoft endpoints, etc.).
func (a *AuthConfig) ApplyDefaults() error {
	return mergo.Merge(a, AuthConfig{
		ClientID:    authClientID,
		CodeURL:     authCodeURL,
		TokenURL:    authTokenURL,
		RedirectURL: authRedirectURL,
	})
}

// # --------------------------------------------------
//
// Account represents an authenticated Microsoft account.
// Includes an RWMutex to be safe in concurrent environments (like FUSE).
type Account struct {
	sync.RWMutex `json:"-"`             // Not serialized in JSON
	keyring      Keyring                `json:"-"`    // Never saved
	Name         string                 `json:"name"` // e.g.: "user@outlook.com" (Primary key)
	Config       AuthConfig             `json:"config"`
	Mount        AccountPersistedConfig `json:"mount,omitempty"`
	ExpiresAt    int64                  `json:"expires_at"`
	AccessToken  string                 `json:"-"` // NEVER saved to disk JSON (see accountJSON)
	RefreshToken string                 `json:"-"` // NEVER saved to disk JSON

	tokenFilePath  string // Internal path where the JSON is saved
	lastKeyringErr error  // Last error when saving to keyring (or "no keyring"), nil if last saveUnsafe succeeded
}

// AccountPersistedConfig groups mount and cache parameters that are persisted
// in the account JSON to survive between sessions. All fields are optional
// (omitempty): if absent from JSON, defaults are used.
type AccountPersistedConfig struct {
	// DefaultMountpoint is the last mountpoint used successfully.
	// If present, mount without positional arguments reuses it.
	DefaultMountpoint string `json:"defaultMountpoint,omitempty"`

	// ──── Cache ────
	CacheDir        string        `json:"cacheDir,omitempty"`
	CacheTTL        time.Duration `json:"cacheTTL,omitempty"`
	CacheMaxEntries int           `json:"cacheMaxEntries,omitempty"`
	CacheMaxSize    int64         `json:"cacheMaxSize,omitempty"`

	// ──── Delta Sync ────
	DeltaInterval time.Duration `json:"deltaInterval,omitempty"`

	// ──── Upload Manager ────
	MaxUploadsInFlight int `json:"maxUploadsInFlight,omitempty"`
	MaxUploadRetries   int `json:"maxUploadRetries,omitempty"`

	// ──── Network / HTTP ────
	HTTPTimeout  time.Duration `json:"httpTimeout,omitempty"`
	GraphRetries int           `json:"graphRetries,omitempty"`

	// ──── Pre-warm ────
	// PreWarmDepth is the number of metadata listing levels to fetch eagerly
	// after mount using a BFS traversal from root (1=root, 2=root+immediate
	// children, ...). 0 disables pre-warming. Valid range: [0, 10].
	// A pointer distinguishes "not set" (nil → use default 2) from an
	// explicit 0 (disable), since 0 is a meaningful value here.
	PreWarmDepth *int `json:"preWarmDepth,omitempty"`
}

// KeyringSaveFailed returns true if the last save of RefreshToken to the
// OS keyring failed (or no keyring was available).
// The caller (e.g. the interactive "account add" flow) should use this
// to visibly warn the user: without the keyring, the session does not
// survive a process restart and re-authentication will be required.
func (a *Account) KeyringSaveFailed() bool {
	a.RLock()
	defer a.RUnlock()
	return a.lastKeyringErr != nil
}

// accountJSON is Account without sync.RWMutex or internal fields, for disk serialization.
//
// Security note: AccessToken is deliberately NOT included here. Like
// RefreshToken, there is no reason to persist it in plain text on disk:
// it grants immediate functional access to the Graph API (read/write/delete
// OneDrive files) during its lifetime. If ExpiresAt has already passed when
// loading the account, GetAccessToken/Refresh renew it automatically from
// the RefreshToken (stored in the OS keyring, not on disk). There is no
// functional need to save it, only risk ($HOME backups, cloud sync,
// reading by other processes with access to the user's home).
type accountJSON struct {
	Name      string                 `json:"name"`
	Config    AuthConfig             `json:"config"`
	Mount     AccountPersistedConfig `json:"mount,omitempty"`
	ExpiresAt int64                  `json:"expires_at"`
}

// AuthError is an authentication error from the Microsoft API. Generally we don't see
// these unless something goes catastrophically wrong with Microsoft's authentication
// services.
type AuthError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	ErrorCodes       []int  `json:"error_codes"`
	ErrorURI         string `json:"error_uri"`
	Timestamp        string `json:"timestamp"` // json.Unmarshal doesn't like this timestamp format
	TraceID          string `json:"trace_id"`
	CorrelationID    string `json:"correlation_id"`
}

// toJSON returns a flat copy of the account ready for JSON serialization,
// excluding the mutex, internal fields (keyring, tokenFilePath), and
// AccessToken (see accountJSON comment on why it's not persisted).
func (a *Account) toJSON() accountJSON {
	return accountJSON{
		Name:      a.Name,
		Config:    a.Config,
		Mount:     a.Mount,
		ExpiresAt: a.ExpiresAt,
	}
}

// Refresh renews tokens if they are expired or if the AccessToken is not
// available in memory. It is concurrency-safe.
//
// The second case (AccessToken == "") covers loading from disk: the
// AccessToken is deliberately not persisted (see accountJSON), so after
// a process restart, ExpiresAt may still point to a future time (it was
// valid when saved) but AccessToken is empty in memory. Without this
// check, Refresh would short-circuit believing the token is still valid
// and GetAccessToken would return an empty string.
func (a *Account) Refresh(ctx context.Context) error {
	a.Lock()
	defer a.Unlock()

	if a.ExpiresAt > time.Now().Unix() && a.AccessToken != "" {
		return nil // Tokens still valid and available in memory, no need to renew
	}

	log.Info().Str("account", secureString(a.Name)).Msg("Tokens expired or not available in memory, renewing...")

	data := url.Values{
		"client_id":     {a.Config.ClientID},
		"redirect_uri":  {a.Config.RedirectURL},
		"refresh_token": {a.RefreshToken},
		"grant_type":    {"refresh_token"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Config.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("creating refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		if IsOffline(err) {
			if a.AccessToken != "" {
				// We already have an AccessToken in memory (even if its ExpiresAt
				// has passed); we stay offline temporarily and will
				// retry on the next use, without blocking the caller.
				log.Trace().Err(err).Msg("Network unreachable during renewal, will be ignored until next use.")
				return nil
			}
			// No AccessToken available (e.g. freshly loaded
			// from disk after a restart) and no network to get one:
			// there is nothing valid to return, so we must propagate the
			// error instead of returning success with an empty token.
			return fmt.Errorf("no connection and no access token in memory: %w", err)
		}
		return fmt.Errorf("network failure during renewal: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading refresh response: %w", err)
	}

	// Microsoft responds 4xx with a specific error body when the
	// refresh token is no longer valid (revoked by user, password
	// changed, organization policy, MFA re-required, etc.). Typical
	// codes are "invalid_grant" and "interaction_required": in
	// both cases, retrying with the same RefreshToken will never
	// succeed, so we purge it from the keyring to force explicit
	// re-authentication instead of indefinite silent failures.
	if resp.StatusCode >= 400 {
		var authErr AuthError
		_ = json.Unmarshal(body, &authErr) // best-effort; if it doesn't parse, proceed with generic error

		if authErr.Error == "invalid_grant" || authErr.Error == "interaction_required" {
			a.purgeInvalidRefreshTokenLocked()
			return fmt.Errorf(
				"the refresh token is no longer valid (%s): %s — re-authentication required with 'account add'",
				authErr.Error, authErr.ErrorDescription,
			)
		}

		return fmt.Errorf("microsoft rejected the renewal (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var newTokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}

	if err := json.Unmarshal(body, &newTokens); err != nil {
		return fmt.Errorf("error parsing renewal response: %w", err)

	}

	if newTokens.AccessToken == "" {
		return fmt.Errorf("no access_token received in renewal")
	}

	// Save the previous state so we can roll back in memory if the
	// save to disk/keyring fails. Without this rollback, a
	// saveUnsafe() failure (full disk, keyring unavailable, etc.)
	// would leave the new RefreshToken only in memory: if the process
	// terminates before a subsequent successful save, the token is
	// lost, and on the next start the old RefreshToken is loaded from
	// the keyring, which Microsoft may have already invalidated when
	// issuing the new one (rotation).
	prevAccessToken := a.AccessToken
	prevRefreshToken := a.RefreshToken
	prevExpiresAt := a.ExpiresAt

	a.AccessToken = newTokens.AccessToken
	if newTokens.RefreshToken != "" {
		a.RefreshToken = newTokens.RefreshToken // Microsoft sometimes sends a new one
	}
	a.ExpiresAt = time.Now().Unix() + newTokens.ExpiresIn

	if err := a.saveUnsafe(); err != nil {
		// Roll back in-memory state so it remains consistent with what
		// actually persists on disk/keyring, and so the next
		// GetAccessToken will retry the refresh instead of assuming
		// the new (unpersisted) token is still valid.
		a.AccessToken = prevAccessToken
		a.RefreshToken = prevRefreshToken
		a.ExpiresAt = prevExpiresAt
		return fmt.Errorf("tokens renewed but could not be saved: %w", err)
	}

	return nil
}

// secureString obfuscates a string (email or token) for secure logging,
// revealing only the first character before '@'.
func secureString(s string) string {
	var leng int = len(s)
	switch leng {
	case 0:
		return "***"
	case 1:
		return s[:1] + "***"
	default:
		split := strings.Split(s, "@")
		if len(split) == 2 && split[0] != "" {
			return split[0][:1] + "****@" + split[1]
		}
		return s[:1] + "****"
	}

}

// purgeInvalidRefreshTokenLocked deletes the RefreshToken from memory and
// from the system keyring after Microsoft confirms it is no longer valid
// (invalid_grant / interaction_required). Requires the caller to already
// hold the Account's write Lock (it is used from Refresh).
//
// Without this, a revoked RefreshToken would stay in the keyring forever:
// every refresh attempt would fail in the same way indefinitely, until
// the user manually ran 'account remove'. Purging it forces explicit
// re-authentication on the next use, which is the only action that can
// truly resolve the situation.
func (a *Account) purgeInvalidRefreshTokenLocked() {
	a.RefreshToken = ""
	a.AccessToken = ""

	if a.keyring == nil {
		return
	}
	refreshKey, accessKey := keyringKeys(a.Name)
	if err := a.keyring.Delete(keyringService, refreshKey); err != nil {
		log.Warn().Err(err).Str("account", secureString(a.Name)).Msg(
			"Could not purge the invalid refresh token from the keyring")
		return
	}
	// Also purge the access token cached in the keyring: both belong
	// to the same revoked session.
	if err := a.keyring.Delete(keyringService, accessKey); err != nil {
		log.Warn().Err(err).Str("account", secureString(a.Name)).Msg(
			"Could not purge the access token of the revoked session from the keyring")
	}
	log.Info().Str("account", secureString(a.Name)).Msg(
		"Invalid refresh token purged from keyring; re-authentication required")
}

// Save persists the account to disk (JSON) and the refresh token to the keyring.
// It is a thread-safe wrapper around saveUnsafe that acquires the Lock.
func (a *Account) Save() error {
	a.Lock()
	defer a.Unlock()
	return a.saveUnsafe()
}

// GetAccessToken returns the current token, refreshing it if necessary.
func (a *Account) GetAccessToken(ctx context.Context) (string, error) {
	if err := a.Refresh(ctx); err != nil {
		return "", err
	}

	a.RLock()
	defer a.RUnlock()
	return a.AccessToken, nil
}

// saveUnsafe saves the account to disk (JSON) and the refresh token to the
// OS keyring. Requires the caller to hold the Account's write Lock.
//
// A failure to save to the keyring does NOT fail this function (the disk
// JSON is still useful on its own, and by design the RefreshToken is never
// persisted there as a fallback). Instead, the failure is recorded in
// a.lastKeyringErr so the caller (e.g. the AddAccount interactive flow)
// can decide to show a visible warning to the user: without the
// RefreshToken in the keyring, the session does not survive a process
// restart, and the user should know immediately, not just find it in a
// log file.
func (a *Account) saveUnsafe() error {
	a.lastKeyringErr = nil

	// 1. Save Refresh Token to the OS Keyring (secure)
	refreshKey, accessKey := keyringKeys(a.Name)
	if a.RefreshToken != "" {
		if a.keyring == nil {
			a.lastKeyringErr = fmt.Errorf("no keyring configured")
			log.Error().Str("account", secureString(a.Name)).Msg(
				"No keyring available: the refresh token will not be persisted and the session will not survive a restart")
		} else if err := a.keyring.Set(keyringService, refreshKey, a.RefreshToken); err != nil {
			a.lastKeyringErr = err
			log.Error().Err(err).Str("account", secureString(a.Name)).Msg(
				"Failed to save refresh token to keyring: the session will not survive a restart")
		}
	}

	// 1b. Also save the Access Token to the keyring, under a separate
	// key (onecloudriver:access:<name>). The AccessToken is NOT persisted
	// in the disk JSON (security S1: plain text accessible to any
	// process with access to $HOME). However, without persisting it
	// anywhere, a process restart without network would leave the
	// account without a token: offline mode could not start. The OS
	// keyring is the appropriate secure place: encrypted, access
	// restricted to the user, and it's where the RefreshToken already
	// lives. A failure here does NOT set lastKeyringErr: the access
	// token can be re-renewed with network, so it does not compromise
	// the session.
	if a.AccessToken != "" && a.keyring != nil {
		if err := a.keyring.Set(keyringService, accessKey, a.AccessToken); err != nil {
			log.Error().Err(err).Str("account", secureString(a.Name)).Msg(
				"Failed to save access token to keyring: offline mode may not work after a restart")
		}
	}

	// 2. Save the rest to the JSON file (without the refresh token or access token)
	byteData, err := json.MarshalIndent(a.toJSON(), "", "  ")
	if err != nil {
		return fmt.Errorf("error serializing account to JSON: %w", err)
	}
	// IMPROVEMENT: 0600 permissions (only read/write for the current user)
	if err := os.WriteFile(a.tokenFilePath, byteData, 0600); err != nil {
		return fmt.Errorf("error saving account file: %w", err)
	}

	return nil
}
