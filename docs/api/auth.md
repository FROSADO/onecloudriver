# API: internal/auth

> Auto-generated with `go doc -all`. Date: 2026-08-30 13:04:27

```
package auth // import "github.com/frosado/onecloudriver/internal/auth"

Package auth manages OAuth2 authentication with Microsoft Identity Platform,
secure token storage (keyring + disk), and automatic refresh of expired tokens.

CONSTANTS

const DefaultLogLevel = "info"

FUNCTIONS

func DiscardLogs()
    DiscardLogs completely silences zerolog logs (useful for tests)

func InitLogging() error
    InitLogging configures the full logging system with the production default
    of Info and above: - Logs go to a file in JSON format (for debugging) -
    Trace and Debug records are discarded by default - The console stays silent
    (only explicit CLI Printf calls are shown)

func InitLoggingWithLevel(level string) error
    InitLoggingWithLevel configures the logging system using a caller-selected
    minimum level. The level is applied before opening the log file so invalid
    configuration never leaves the process at zerolog's more verbose default.

func IsOffline(err error) bool
    IsOffline determines whether an error is caused by lack of network
    connectivity (DNS, connection timeout, etc.). Returns true if the error is
    of type *url.Error or does not contain "HTTP " in its message.

    Used during token refresh to avoid failures when the machine is disconnected
    from the internet.

func SetLogLevel(level string) error
    SetLogLevel configures zerolog's global minimum level. Levels below the
    configured threshold are discarded before they are serialized or written.

func SetLogOutput(w io.Writer)
    SetLogOutput redirects zerolog's global logs to the provided writer.
    Use it to send logs to a file instead of the console.


TYPES

type Account struct {
	sync.RWMutex `json:"-"` // Not serialized in JSON

	Name         string                 `json:"name"` // e.g.: "user@outlook.com" (Primary key)
	Config       AuthConfig             `json:"config"`
	Mount        AccountPersistedConfig `json:"mount,omitempty"`
	ExpiresAt    int64                  `json:"expires_at"`
	AccessToken  string                 `json:"-"` // NEVER saved to disk JSON (see accountJSON)
	RefreshToken string                 `json:"-"` // NEVER saved to disk JSON

	// Has unexported fields.
}
    # --------------------------------------------------

    Account represents an authenticated Microsoft account. Includes an RWMutex
    to be safe in concurrent environments (like FUSE).

func (a *Account) GetAccessToken(ctx context.Context) (string, error)
    GetAccessToken returns the current token, refreshing it if necessary.

func (a *Account) KeyringSaveFailed() bool
    KeyringSaveFailed returns true if the last save of RefreshToken to the
    OS keyring failed (or no keyring was available). The caller (e.g. the
    interactive "account add" flow) should use this to visibly warn the user:
    without the keyring, the session does not survive a process restart and
    re-authentication will be required.

func (a *Account) Refresh(ctx context.Context) error
    Refresh renews tokens if they are expired or if the AccessToken is not
    available in memory. It is concurrency-safe.

    The second case (AccessToken == "") covers loading from disk: the
    AccessToken is deliberately not persisted (see accountJSON), so after a
    process restart, ExpiresAt may still point to a future time (it was valid
    when saved) but AccessToken is empty in memory. Without this check, Refresh
    would short-circuit believing the token is still valid and GetAccessToken
    would return an empty string.

func (a *Account) Save() error
    Save persists the account to disk (JSON) and the refresh token to the
    keyring. It is a thread-safe wrapper around saveUnsafe that acquires the
    Lock.

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
    AccountPersistedConfig groups mount and cache parameters that are persisted
    in the account JSON to survive between sessions. All fields are optional
    (omitempty): if absent from JSON, defaults are used.

type AuthConfig struct {
	ClientID    string `json:"clientID"`
	CodeURL     string `json:"codeURL"`
	TokenURL    string `json:"tokenURL"`
	RedirectURL string `json:"redirectURL"`
}
    AuthConfig contains the OAuth2 configuration parameters for authenticating
    against Microsoft Identity Platform. If any field is empty, ApplyDefaults
    fills it with the application's default values.

func (a *AuthConfig) ApplyDefaults() error
    ApplyDefaults fills empty AuthConfig fields with the application's default
    values (official client_id, Microsoft endpoints, etc.).

type AuthError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	ErrorCodes       []int  `json:"error_codes"`
	ErrorURI         string `json:"error_uri"`
	Timestamp        string `json:"timestamp"` // json.Unmarshal doesn't like this timestamp format
	TraceID          string `json:"trace_id"`
	CorrelationID    string `json:"correlation_id"`
}
    AuthError is an authentication error from the Microsoft API. Generally
    we don't see these unless something goes catastrophically wrong with
    Microsoft's authentication services.

type Keyring interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
	Delete(service, user string) error
}
    Keyring defines the interface for secure storage of secrets.

type Manager struct {
	// Has unexported fields.
}
    Manager manages multiple OneDrive accounts.

func NewManager() (*Manager, error)
    NewManager creates a new manager. Uses the OS's standard configuration
    directory.

func NewManagerWithDeps(baseDir string, kr Keyring) (*Manager, error)
    NewManagerWithDeps allows injecting dependencies (useful for tests).

func (m *Manager) AddAccount(ctx context.Context, config AuthConfig, _ bool, input io.Reader) (*Account, error)
    AddAccount executes the full OAuth authentication flow to add a new
    Microsoft account to the manager:
     1. Obtains authorization code (via browser)
     2. Exchanges code for tokens
     3. Fetches user profile from Graph
     4. Saves the account to disk and keyring

func (m *Manager) GetAccount(name string) (*Account, error)
    GetAccount returns an account by name. Returns an error if it doesn't exist.

func (m *Manager) ListAccounts() []string
    ListAccounts returns the names of all configured accounts.

func (m *Manager) RemoveAccount(name string) error
    RemoveAccount removes an account from the manager, from disk, and from the
    keyring.

func (m *Manager) ResolveMainAccountName() (string, error)
    ResolveMainAccountName auto-detects the configured account:
      - 1 account → uses it automatically
      - 0 accounts → error: "no accounts configured"
      - 2+ accounts → error: "multiple accounts, use --account"

func (m *Manager) SetGraphClientFactory(f func() *graph.Client)
    SetGraphClientFactory overrides how the Manager creates Graph clients during
    the OAuth flow (AddAccount). Tests use it to intercept Graph API calls.

```
