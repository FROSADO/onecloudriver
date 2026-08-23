package auth

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/frosado/onecloudriver/internal/printer"
	"github.com/rs/zerolog/log"
)

// httpClient reusable (ensure this name matches the one in your file)
var httpClient = &http.Client{Timeout: 15 * time.Second}

// authSession holds the per-login secrets that bind an authorization
// response to the request that started it:
//
//   - state: random value echoed back by Microsoft in the redirect. The
//     callback is rejected unless it matches, so an unsolicited request to
//     the loopback server (any local process, or a web page sending the
//     browser to http://localhost:9090/callback?code=...) cannot inject a
//     foreign authorization code and link the attacker's OneDrive account.
//   - verifier/challenge: PKCE (RFC 7636). This is a public client with no
//     client_secret, so an intercepted authorization code is otherwise
//     redeemable by anyone; the token request proves possession of the
//     verifier whose SHA-256 was sent with the authorization request.
type authSession struct {
	state     string
	verifier  string
	challenge string
}

// newAuthSessionFunc is the seam used to obtain the login session; tests
// replace it to work with a deterministic state.
var newAuthSessionFunc = newAuthSession

// newAuthSession generates a fresh state and PKCE verifier/challenge pair.
func newAuthSession() (*authSession, error) {
	state, err := randomURLSafe(32)
	if err != nil {
		return nil, err
	}
	verifier, err := randomURLSafe(64)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(verifier))
	return &authSession{
		state:     state,
		verifier:  verifier,
		challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
	}, nil
}

// randomURLSafe returns n cryptographically random bytes encoded as an
// unpadded base64url string, safe to use as an OAuth query parameter.
func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// matchesState compares the state received in a callback against the one
// generated for this login, in constant time.
func (s *authSession) matchesState(got string) bool {
	return subtle.ConstantTimeCompare([]byte(s.state), []byte(got)) == 1
}

// requireLoopbackRedirect rejects a redirect_uri whose host is not a
// loopback address. The callback server receives authorization codes, so
// binding it to a routable address would expose it to the whole network;
// RFC 8252 section 7.3 restricts native-app redirects to loopback.
func requireLoopbackRedirect(u *url.URL) error {
	host := u.Hostname()
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("redirect_uri host %q is not a loopback address", host)
}

// buildAuthURL constructs the OAuth authorization URL with the required
// parameters (client_id, scope, redirect_uri, response_type) plus the
// session state and PKCE challenge. Used by both the local server flow and
// the copy-paste fallback.
func buildAuthURL(config AuthConfig, session *authSession) string {
	u, err := url.Parse(config.CodeURL)
	if err != nil {
		// CodeURL is an internal constant that should never fail, but
		// if the caller passes a malformed URL, we return a safe value.
		return ""
	}
	q := u.Query()
	q.Add("client_id", config.ClientID)
	q.Add("scope", "user.read files.readwrite.all offline_access")
	q.Add("response_type", "code")
	q.Add("redirect_uri", config.RedirectURL)
	q.Add("state", session.state)
	q.Add("code_challenge", session.challenge)
	q.Add("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String()
}

// getAuthCodeLocalServer starts a temporary HTTP server on the port
// specified by the redirect_uri, opens the browser automatically, and
// captures the authorization code when Microsoft redirects the user.
//
// If the port is already in use or the server cannot start, it returns
// an error so the caller can use the copy-paste fallback.
func getAuthCodeLocalServer(config AuthConfig, session *authSession) (string, error) {
	redirectURL, err := url.Parse(config.RedirectURL)
	if err != nil {
		return "", fmt.Errorf("invalid redirect_uri: %w", err)
	}

	host := redirectURL.Host
	if host == "" {
		host = "localhost:9090"
		redirectURL.Host = host
	}
	if err := requireLoopbackRedirect(redirectURL); err != nil {
		return "", err
	}

	// The callback path (e.g. "/callback") — Microsoft redirects to this
	// exact URL with ?code=XXXX as query parameter.
	callbackPath := redirectURL.Path
	if callbackPath == "" {
		callbackPath = "/"
	}

	// Channel to receive the code or error from the HTTP handler
	resultCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	handler := func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		errorDesc := r.URL.Query().Get("error_description")

		// Check state before anything else: a callback without this
		// login's state is either stray traffic or an injected code, and
		// must never reach the token exchange.
		if !session.matchesState(r.URL.Query().Get("state")) {
			log.Warn().Msg("Discarded OAuth callback with missing or mismatched state")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(errorHTML("Invalid request", "The 'state' parameter does not match this login attempt.")))
			return
		}

		if errorDesc != "" {
			// Write and flush the page before signaling: getAuthCodeLocalServer
			// returns as soon as it reads errCh and closes the server, which
			// would otherwise truncate the response before the browser (or the
			// test client) receives it (issue #126).
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(errorHTML("Authentication error", errorDesc)))
			w.(http.Flusher).Flush()
			errCh <- fmt.Errorf("microsoft returned error: %s", errorDesc)
			return
		}

		if code == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(errorHTML("Error", "Authorization code not received.")))
			w.(http.Flusher).Flush()
			errCh <- fmt.Errorf("authorization code not received in callback")
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(successHTML))
		w.(http.Flusher).Flush()
		resultCh <- code
	}

	// Register the handler on the exact callback path and on "/" as fallback
	mux.HandleFunc(callbackPath, handler)
	if callbackPath != "/" {
		mux.HandleFunc("/", handler)
	}

	listener, err := net.Listen("tcp", host)
	if err != nil {
		return "", fmt.Errorf("could not start local server on %s: %w", host, err)
	}

	// G112: configure ReadHeaderTimeout to prevent Slowloris attacks
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Ensure listener and server are closed on exit
	defer listener.Close()
	defer server.Close()

	// Start the server in a goroutine
	go func() {
		_ = server.Serve(listener)
	}()

	// Open the browser automatically
	authURL := buildAuthURL(config, session)
	fmt.Printf("\n%s Opening browser for authentication...\n", printer.Globe)
	fmt.Printf("   If it doesn't open automatically, visit:\n   %s\n\n", authURL)

	if err := openBrowser(authURL); err != nil {
		log.Warn().Err(err).Msg("Could not open browser automatically")
	}

	fmt.Println(printer.Hourglass, "Waiting for authorization in the browser...")

	// Wait for the code or timeout (2 minutes)
	select {
	case code := <-resultCh:
		fmt.Println(printer.Success, "Authorization received.")
		return code, nil
	case err := <-errCh:
		return "", err
	case <-time.After(2 * time.Minute):
		return "", fmt.Errorf("timeout waiting for authorization (2 minutes)")
	}
}

// openBrowser opens the URL in the system's default browser.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return fmt.Errorf("unsupported operating system for opening browser: %s", runtime.GOOS)
	}
}

// successHTML is the page shown to the user after successful authentication.
const successHTML = `<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>Authentication successful</title>
<style>
  body { font-family: system-ui, sans-serif; display: flex; justify-content: center; align-items: center; min-height: 100vh; margin: 0; background: #f0fdf4; }
  .card { background: white; border-radius: 12px; padding: 2rem; text-align: center; box-shadow: 0 4px 12px rgba(0,0,0,0.1); max-width: 400px; }
  .check { font-size: 3rem; margin-bottom: 0.5rem; }
  h1 { color: #166534; margin: 0 0 0.5rem; }
  p { color: #4b5563; }
</style></head>
<body>
<div class="card">
  <div class="check">✅</div>
  <h1>Authentication successful!</h1>
  <p>You can now close this window and return to the terminal.</p>
</div>
</body>
</html>`

// errorHTML generates an error page to show in the browser.
func errorHTML(title, message string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>%s</title>
<style>
  body { font-family: system-ui, sans-serif; display: flex; justify-content: center; align-items: center; min-height: 100vh; margin: 0; background: #fef2f2; }
  .card { background: white; border-radius: 12px; padding: 2rem; text-align: center; box-shadow: 0 4px 12px rgba(0,0,0,0.1); max-width: 400px; }
  .icon { font-size: 3rem; margin-bottom: 0.5rem; }
  h1 { color: #991b1b; margin: 0 0 0.5rem; }
  p { color: #4b5563; }
</style></head>
<body>
<div class="card">
  <div class="icon">❌</div>
  <h1>%s</h1>
  <p>%s</p>
</div>
</body>
</html>`, html.EscapeString(title), html.EscapeString(title), html.EscapeString(message))
}

// getAuthCodeHeadless starts the device-less OAuth flow: prints a URL for
// the user to authorize in the browser and reads the redirect URL from the
// provided io.Reader (typically os.Stdin). Extracts the authorization code
// from the query parameters of the pasted URL.
//
// Used as fallback when the local server cannot start (e.g. port occupied,
// no graphical browser environment, or SSH tunnel).
func getAuthCodeHeadless(config AuthConfig, session *authSession, input io.Reader) (string, error) {
	authURL := buildAuthURL(config, session)

	fmt.Printf("\n1. Open this URL in your browser:\n%s\n\n", authURL)
	fmt.Print("2. Sign in and authorize the application.\n")
	fmt.Printf("3. You will be redirected to %s. Copy the full URL of that page and paste it here:\n> ", config.RedirectURL)

	// IMPROVEMENT: Use bufio.Scanner over the injected io.Reader
	scanner := bufio.NewScanner(input)
	if !scanner.Scan() {
		return "", fmt.Errorf("error reading input: %w", scanner.Err())
	}
	response := strings.TrimSpace(scanner.Text())

	parsed, err := url.Parse(response)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	if err := validatePastedRedirect(parsed, config.RedirectURL); err != nil {
		return "", err
	}

	if !session.matchesState(parsed.Query().Get("state")) {
		return "", fmt.Errorf("'state' parameter does not match this login attempt; paste the URL from the browser window opened by this command")
	}

	code := parsed.Query().Get("code")
	if code == "" {
		return "", fmt.Errorf("'code' parameter not found in URL. Did you copy the correct URL?")
	}

	return code, nil
}

// validatePastedRedirect ensures the URL pasted by the user is the
// configured redirect target and not an arbitrary link handed to them,
// which could otherwise smuggle a foreign authorization code into the
// token exchange.
func validatePastedRedirect(pasted *url.URL, redirectURL string) error {
	expected, err := url.Parse(redirectURL)
	if err != nil {
		return fmt.Errorf("invalid redirect_uri: %w", err)
	}
	if !strings.EqualFold(pasted.Scheme, expected.Scheme) ||
		!strings.EqualFold(pasted.Host, expected.Host) ||
		strings.TrimSuffix(pasted.Path, "/") != strings.TrimSuffix(expected.Path, "/") {
		return fmt.Errorf("pasted URL does not match the expected redirect %s", redirectURL)
	}
	return nil
}

// exchangeCodeForTokens exchanges an OAuth authorization code for access
// and refresh tokens against Microsoft's /token endpoint.
func exchangeCodeForTokens(ctx context.Context, config AuthConfig, code, codeVerifier string) (accessToken, refreshToken string, expiresIn int64, err error) {
	data := url.Values{
		"client_id":    {config.ClientID},
		"redirect_uri": {config.RedirectURL},
		"code":         {code},
		"grant_type":   {"authorization_code"},
	}
	if codeVerifier != "" {
		data.Set("code_verifier", codeVerifier)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", "", 0, fmt.Errorf("error creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", 0, fmt.Errorf("network error obtaining tokens: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", 0, fmt.Errorf("error reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			ErrorDescription string `json:"error_description"`
		}
		if err := json.Unmarshal(body, &errResp); err != nil {
			// The body is not valid JSON (e.g. an HTML error page from
			// a corporate proxy). We include a fragment of the raw body
			// instead of silencing the error, to avoid losing all
			// diagnostic information at the most sensitive login
			// entry point.
			bodyStr := strings.TrimSpace(string(body))
			if len(bodyStr) > 200 {
				bodyStr = bodyStr[:200] + "..."
			}
			return "", "", 0, fmt.Errorf("microsoft rejected the code (HTTP %d), non-JSON response: %s", resp.StatusCode, bodyStr)
		}
		return "", "", 0, fmt.Errorf("microsoft rejected the code (HTTP %d): %s", resp.StatusCode, errResp.ErrorDescription)
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}

	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", "", 0, fmt.Errorf("error parsing tokens: %w", err)
	}

	if tokenResp.AccessToken == "" || tokenResp.RefreshToken == "" {
		return "", "", 0, fmt.Errorf("response does not contain the expected tokens")
	}

	return tokenResp.AccessToken, tokenResp.RefreshToken, tokenResp.ExpiresIn, nil
}

// AddAccount executes the full OAuth authentication flow to add a new
// Microsoft account to the manager:
//  1. Obtains authorization code (via browser)
//  2. Exchanges code for tokens
//  3. Fetches user profile from Graph
//  4. Saves the account to disk and keyring
func (m *Manager) AddAccount(ctx context.Context, config AuthConfig, _ bool, input io.Reader) (*Account, error) {
	if err := config.ApplyDefaults(); err != nil {
		return nil, fmt.Errorf("Error applying default values: %w", err)
	}

	fmt.Println("\n--- Authentication process started ---")

	session, err := newAuthSessionFunc()
	if err != nil {
		return nil, fmt.Errorf("failed preparing the authentication session: %w", err)
	}

	// 1. Obtain the authorization code
	//    Try the local server first (opens browser automatically).
	//    If it fails (port occupied, no browser, etc.), use copy-paste fallback.
	code, err := getAuthCodeLocalServer(config, session)
	if err != nil {
		fmt.Printf("\n%s Could not use local server: %v\n", printer.Warning, err)
		fmt.Println("   Switching to manual mode (copy and paste the URL)...")
		code, err = getAuthCodeHeadless(config, session, input)
	}
	if err != nil {
		return nil, fmt.Errorf("failed obtaining code: %w", err)
	}

	// 2. Exchange code for tokens
	accessToken, refreshToken, expiresIn, err := exchangeCodeForTokens(ctx, config, code, session.verifier)
	if err != nil {
		return nil, fmt.Errorf("failed exchanging code: %w", err)
	}

	// 3. Get the real account name from Graph
	// Since we don't have an Account yet, we use a temporary TokenProvider
	graphClient := m.graphClientFactory()
	tempTokenProvider := &staticTokenProvider{token: accessToken}
	user, err := graphClient.GetUser(ctx, tempTokenProvider)
	if err != nil {
		return nil, fmt.Errorf("failed obtaining user profile from Graph: %w", err)
	}

	log.Info().Str("user", secureString(user.UserPrincipalName)).Msg("User profile obtained successfully")

	// 4. Create the Account structure
	newAcc := &Account{
		Name:         user.UserPrincipalName,
		Config:       config,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().Unix() + expiresIn,
		keyring:      m.keyring,
	}

	// 5. Set the file path and save
	newAcc.tokenFilePath = m.getAccountFilePath(newAcc.Name)
	if err := newAcc.saveUnsafe(); err != nil {
		return nil, fmt.Errorf("failed saving the new account: %w", err)
	}

	// Visible warning in the interactive flow if the keyring failed: the user
	// should know immediately (not just from the log file), because
	// it means the session will not survive a process restart and
	// they will have to repeat the login next time.
	if newAcc.KeyringSaveFailed() {
		fmt.Println("\n" + printer.Warning + " Warning: could not save the refresh token in the system keyring.")
		fmt.Println("   The session will NOT survive a restart: you will have to re-authenticate")
		fmt.Println("   the next time this account is used. Check that the keyring service")
		fmt.Println("   (gnome-keyring, KWallet, etc.) is available on your system.")
	}

	// 6. Register in the Manager's map
	m.accounts[newAcc.Name] = newAcc

	fmt.Printf("\n%s Account '%s' added and configured successfully!\n", printer.Success, newAcc.Name)
	return newAcc, nil
}

// getAccountFilePath returns the full path to an account's JSON file
// within the configuration directory.
func (m *Manager) getAccountFilePath(accountName string) string {
	safeName := sanitizeFileName(accountName)
	return fmt.Sprintf("%s/%s.json", m.configDir, safeName)
}

// staticTokenProvider is a simple TokenProvider that always returns the same token.
// Used during initial authentication when we don't have an Account created yet.
type staticTokenProvider struct {
	token string
}

func (s *staticTokenProvider) GetAccessToken(_ context.Context) (string, error) {
	return s.token, nil
}
