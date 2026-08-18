package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/rs/zerolog/log"
)

// Manager manages multiple OneDrive accounts.
type Manager struct {
	configDir string
	keyring   Keyring
	accounts  map[string]*Account // Map: "user@domain.com" -> *Account

	// graphClientFactory creates the Graph client used by the interactive OAuth
	// flow (AddAccount) to fetch the user profile. Defaults to graph.NewClient;
	// tests override it to intercept Graph API calls.
	graphClientFactory func() *graph.Client
}

// NewManager creates a new manager. Uses the OS's standard configuration directory.
func NewManager() (*Manager, error) {
	// os.UserConfigDir() returns ~/.config on Linux, ~/Library/Application Support on Mac, etc.
	baseDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("could not determine configuration directory: %w", err)
	}
	configDir := filepath.Join(baseDir, "onecloudriver")
	return NewManagerWithDeps(configDir, realKeyring{})
}

// NewManagerWithDeps allows injecting dependencies (useful for tests).
func NewManagerWithDeps(baseDir string, kr Keyring) (*Manager, error) {
	if err := os.MkdirAll(baseDir, 0700); err != nil {
		return nil, fmt.Errorf("could not create config directory: %w", err)
	}

	m := &Manager{
		configDir:          baseDir,
		keyring:            kr,
		accounts:           make(map[string]*Account),
		graphClientFactory: func() *graph.Client { return graph.NewClient() },
	}

	return m, m.loadAccounts()
}

// SetGraphClientFactory overrides how the Manager creates Graph clients during
// the OAuth flow (AddAccount). Tests use it to intercept Graph API calls.
func (m *Manager) SetGraphClientFactory(f func() *graph.Client) {
	m.graphClientFactory = f
}

// loadAccounts scans the configuration directory and loads all existing
// accounts from JSON files. For each account, it retrieves the refresh token
// from the OS keyring.
func (m *Manager) loadAccounts() error {
	files, err := os.ReadDir(m.configDir)
	if err != nil {
		return err
	}

	for _, file := range files {
		// Ignore directories and files that don't end in .json
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}

		// security: gosec (G304) flags this read as "file inclusion via
		// variable", but file.Name() comes from os.ReadDir(m.configDir) —
		// the filesystem itself guarantees that a directory entry name
		// never contains "/", so it cannot escape m.configDir via
		// filepath.Join. It does not cross an external trust boundary.
		filePath := filepath.Join(m.configDir, file.Name()) //nolint:gosec // G304: file.Name() comes from ReadDir, cannot contain path separators
		contents, err := os.ReadFile(filePath)
		if err != nil {
			log.Warn().Err(err).Str("file", filePath).Msg("Could not read account file. Ignoring.")
			continue
		}

		var acc Account
		if err := json.Unmarshal(contents, &acc); err != nil {
			log.Warn().Err(err).Str("file", filePath).Msg("Corrupt account JSON. Ignoring.")
			continue
		}
		acc.keyring = m.keyring
		if err := acc.Config.ApplyDefaults(); err != nil {
			log.Warn().Err(err).Str("account", secureString(acc.Name)).Msg("Error applying defaults to authentication configuration")
		}
		acc.tokenFilePath = filePath

		// Retrieve the refresh token from the keyring
		refreshKey, accessKey := keyringKeys(acc.Name)
		if token, err := m.keyring.Get(keyringService, refreshKey); err == nil {
			acc.RefreshToken = token
		}

		// Also retrieve the access token from the keyring (separate key).
		// The access token is NOT persisted on disk (security S1), but is
		// stored in the keyring so that a process restart WITHOUT network
		// can start in offline mode while the token is still valid. If
		// absent (or expired), Refresh() will renew it via network with
		// the refresh token.
		if token, err := m.keyring.Get(keyringService, accessKey); err == nil {
			acc.AccessToken = token
		}

		m.accounts[acc.Name] = &acc
		log.Info().Str("account", secureString(acc.Name)).Msg("Account loaded")
	}

	return nil
}

// GetAccount returns an account by name. Returns an error if it doesn't exist.
func (m *Manager) GetAccount(name string) (*Account, error) {
	acc, exists := m.accounts[name]
	if !exists {
		return nil, fmt.Errorf("account '%s' is not configured. Use 'onecloudriver account add'", name)
	}
	return acc, nil
}

// ListAccounts returns the names of all configured accounts.
func (m *Manager) ListAccounts() []string {
	names := make([]string, 0, len(m.accounts))
	for name := range m.accounts {
		names = append(names, name)
	}
	return names
}

// RemoveAccount removes an account from the manager, from disk, and from the keyring.
func (m *Manager) RemoveAccount(name string) error {
	acc, exists := m.accounts[name]
	if !exists {
		return fmt.Errorf("account not found")
	}

	// Delete from keyring (uses the injected interface, not the global package,
	// to respect dependency injection and allow mocks in tests)
	refreshKey, accessKey := keyringKeys(name)
	if err := m.keyring.Delete(keyringService, refreshKey); err != nil {
		log.Warn().Err(err).Str("account", secureString(name)).Msg("Could not delete refresh token from keyring")
	}
	if err := m.keyring.Delete(keyringService, accessKey); err != nil {
		log.Warn().Err(err).Str("account", secureString(name)).Msg("Could not delete access token from keyring")
	}

	// Delete file
	if err := os.Remove(acc.tokenFilePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("error deleting file: %w", err)
	}

	delete(m.accounts, name)
	log.Info().Str("account", secureString(name)).Msg("Account removed")
	return nil
}

// ResolveMainAccountName auto-detects the configured account:
//   - 1 account → uses it automatically
//   - 0 accounts → error: "no accounts configured"
//   - 2+ accounts → error: "multiple accounts, use --account"
func (m *Manager) ResolveMainAccountName() (string, error) {
	accounts := m.ListAccounts()
	switch len(accounts) {
	case 0:
		return "", fmt.Errorf("no accounts configured. Use 'onecloudriver account add' to add one")
	case 1:
		return accounts[0], nil
	default:
		return "", fmt.Errorf(
			"there are %d configured accounts. Specify which one to use with --account:\n%s",
			len(accounts),
			strings.Join(accounts, "\n"),
		)
	}
}

// sanitizeFileName converts an account name (e.g.: "user@outlook.com") into a
// safe filename by replacing special characters like '.' and '@' with '_'.
func sanitizeFileName(name string) string {
	// 🔧 Fixed: the strings.Map was iterating over `name` while the '.', '@'
	// replacements were stored in `s`, so the replacements were silently lost
	// (ineffassign). Now the Map iterates over the sanitized value.
	s := strings.ReplaceAll(name, ".", "_")
	s = strings.ReplaceAll(s, "@", "_")

	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			return r
		}
		return '_' // Replace strange characters (e.g.: emojis, accents) with underscore
	}, s)
}
