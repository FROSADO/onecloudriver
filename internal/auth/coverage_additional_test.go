package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
)

func TestGetAuthCodeLocalServer_CallbackOutcomes(t *testing.T) {
	browserDir := t.TempDir()
	browserPath := filepath.Join(browserDir, "xdg-open")
	if err := os.WriteFile(browserPath, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil { //nolint:gosec // test-only executable browser stub
		t.Fatalf("WriteFile(xdg-open): %v", err)
	}
	t.Setenv("PATH", browserDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	tests := []struct {
		name     string
		query    string
		wantCode string
		wantErr  string
		wantBody string
	}{
		{
			name:     "success",
			query:    "?code=auth-code",
			wantCode: "auth-code",
			wantBody: "Authentication successful",
		},
		{
			name:     "microsoft error",
			query:    "?error_description=access+denied",
			wantErr:  "microsoft returned error: access denied",
			wantBody: "access denied",
		},
		{
			name:     "missing code",
			query:    "?state=present",
			wantErr:  "authorization code not received in callback",
			wantBody: "Authorization code not received",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port := findAvailablePort(t)
			config := AuthConfig{
				CodeURL:     "https://login.example/authorize",
				ClientID:    "client-id",
				RedirectURL: fmt.Sprintf("http://%s/callback", port),
			}
			resultCh := make(chan string, 1)
			errCh := make(chan error, 1)
			go func() {
				code, err := getAuthCodeLocalServer(config)
				if err != nil {
					errCh <- err
					return
				}
				resultCh <- code
			}()

			var response *http.Response
			var err error
			callbackURL := "http://" + port + "/callback" + tt.query
			for range 500 {
				response, err = http.Get(callbackURL) //nolint:gosec // test URL uses a temporary localhost listener
				if err == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err != nil {
				t.Fatalf("GET callback: %v", err)
			}
			body, readErr := readAllAndClose(response)
			if readErr != nil {
				t.Fatalf("read callback response: %v", readErr)
			}
			if response.StatusCode != http.StatusOK {
				t.Fatalf("callback status = %d, want 200", response.StatusCode)
			}
			if !strings.Contains(string(body), tt.wantBody) {
				t.Errorf("callback body does not contain %q:\n%s", tt.wantBody, body)
			}

			select {
			case code := <-resultCh:
				if tt.wantErr != "" {
					t.Fatalf("received code %q, want error", code)
				}
				if code != tt.wantCode {
					t.Errorf("code = %q, want %q", code, tt.wantCode)
				}
			case callbackErr := <-errCh:
				if tt.wantErr == "" {
					t.Fatalf("unexpected callback error: %v", callbackErr)
				}
				if !strings.Contains(callbackErr.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", callbackErr, tt.wantErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for callback result")
			}
		})
	}
}

func TestGetAuthCodeLocalServer_StartFailures(t *testing.T) {
	t.Run("occupied host", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.Listen: %v", err)
		}
		defer listener.Close()

		_, err = getAuthCodeLocalServer(AuthConfig{
			RedirectURL: "http://" + listener.Addr().String() + "/callback",
		})
		if err == nil || !strings.Contains(err.Error(), "could not start local server") {
			t.Fatalf("error = %v, want listen failure", err)
		}
	})

	t.Run("unparseable redirect URL", func(t *testing.T) {
		_, err := getAuthCodeLocalServer(AuthConfig{RedirectURL: "://not-a-url"})
		if err == nil || !strings.Contains(err.Error(), "invalid redirect_uri") {
			t.Fatalf("error = %v, want invalid redirect URI", err)
		}
	})
}

func readAllAndClose(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	return io.ReadAll(response.Body)
}

func TestAddAccount_HeadlessSuccessAndKeyringWarning(t *testing.T) {
	tokenServer, graphServer := addAccountServers(t, http.StatusOK, http.StatusOK)
	defer tokenServer.Close()
	defer graphServer.Close()

	tests := []struct {
		name     string
		failSet  bool
		wantWarn bool
	}{
		{name: "persists account", wantWarn: false},
		{name: "continues when keyring save fails", failSet: true, wantWarn: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			occupied, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("net.Listen: %v", err)
			}
			defer occupied.Close()

			keyring := &mockKeyring{storage: make(map[string]string), failSet: tt.failSet}
			manager, err := NewManagerWithDeps(t.TempDir(), keyring)
			if err != nil {
				t.Fatalf("NewManagerWithDeps: %v", err)
			}
			manager.SetGraphClientFactory(func() *graph.Client {
				return graph.NewClient(
					graph.WithBaseURL(graphServer.URL),
					graph.WithHTTPClient(graphServer.Client()),
					graph.WithRetry(0),
				)
			})
			config := AuthConfig{
				TokenURL:    tokenServer.URL + "/token",
				ClientID:    "client-id",
				RedirectURL: "http://" + occupied.Addr().String() + "/callback",
			}
			input := strings.NewReader(config.RedirectURL + "?code=auth-code\n")

			account, err := manager.AddAccount(context.Background(), config, true, input)
			if err != nil {
				t.Fatalf("AddAccount: %v", err)
			}
			if account.Name != "user@example.com" {
				t.Fatalf("account name = %q, want user@example.com", account.Name)
			}
			if _, err := manager.GetAccount(account.Name); err != nil {
				t.Fatalf("GetAccount: %v", err)
			}
			accountFile := filepath.Join(manager.configDir, "user_example_com.json")
			if _, err := os.Stat(accountFile); err != nil {
				t.Fatalf("account file was not written: %v", err)
			}
			refreshKey, _ := keyringKeys(account.Name)
			storedRefresh := keyring.storage[keyringService+":"+refreshKey]
			if got := account.KeyringSaveFailed(); got != tt.wantWarn {
				t.Errorf("KeyringSaveFailed() = %t, want %t", got, tt.wantWarn)
			}
			if !tt.failSet && storedRefresh != "refresh-token" {
				t.Errorf("refresh token = %q, want refresh-token", storedRefresh)
			}
		})
	}
}

func addAccountServers(t *testing.T, tokenStatus, graphStatus int) (tokenServer, graphServer *httptest.Server) {
	t.Helper()
	tokenServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(tokenStatus)
		if tokenStatus == http.StatusOK {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "access-token",
				"refresh_token": "refresh-token",
				"expires_in":    3600,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"error_description": "token exchange failed"})
	}))
	graphServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(graphStatus)
		if graphStatus == http.StatusOK {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id":                "user-id",
				"userPrincipalName": "user@example.com",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "profile failed"})
	}))
	return tokenServer, graphServer
}

func TestAddAccount_FailureBranches(t *testing.T) {
	t.Run("code acquisition", func(t *testing.T) {
		manager, err := NewManagerWithDeps(t.TempDir(), &mockKeyring{storage: make(map[string]string)})
		if err != nil {
			t.Fatalf("NewManagerWithDeps: %v", err)
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.Listen: %v", err)
		}
		defer listener.Close()
		config := AuthConfig{RedirectURL: "http://" + listener.Addr().String() + "/callback"}
		_, err = manager.AddAccount(context.Background(), config, true, strings.NewReader("not a URL\n"))
		if err == nil || !strings.Contains(err.Error(), "failed obtaining code") {
			t.Fatalf("error = %v, want code acquisition failure", err)
		}
	})

	t.Run("token exchange", func(t *testing.T) {
		tokenServer, graphServer := addAccountServers(t, http.StatusBadRequest, http.StatusOK)
		defer tokenServer.Close()
		defer graphServer.Close()
		manager, err := NewManagerWithDeps(t.TempDir(), &mockKeyring{storage: make(map[string]string)})
		if err != nil {
			t.Fatalf("NewManagerWithDeps: %v", err)
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.Listen: %v", err)
		}
		defer listener.Close()
		config := AuthConfig{
			TokenURL:    tokenServer.URL,
			RedirectURL: "http://" + listener.Addr().String() + "/callback",
		}
		_, err = manager.AddAccount(context.Background(), config, true, strings.NewReader(config.RedirectURL+"?code=code\n"))
		if err == nil || !strings.Contains(err.Error(), "failed exchanging code") {
			t.Fatalf("error = %v, want token exchange failure", err)
		}
	})

	t.Run("Graph profile", func(t *testing.T) {
		tokenServer, graphServer := addAccountServers(t, http.StatusOK, http.StatusInternalServerError)
		defer tokenServer.Close()
		defer graphServer.Close()
		manager, err := NewManagerWithDeps(t.TempDir(), &mockKeyring{storage: make(map[string]string)})
		if err != nil {
			t.Fatalf("NewManagerWithDeps: %v", err)
		}
		manager.SetGraphClientFactory(func() *graph.Client {
			return graph.NewClient(graph.WithBaseURL(graphServer.URL), graph.WithHTTPClient(graphServer.Client()), graph.WithRetry(0))
		})
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.Listen: %v", err)
		}
		defer listener.Close()
		config := AuthConfig{
			TokenURL:    tokenServer.URL,
			RedirectURL: "http://" + listener.Addr().String() + "/callback",
		}
		_, err = manager.AddAccount(context.Background(), config, true, strings.NewReader(config.RedirectURL+"?code=code\n"))
		if err == nil || !strings.Contains(err.Error(), "failed obtaining user profile") {
			t.Fatalf("error = %v, want Graph profile failure", err)
		}
	})
}

func TestExchangeCodeForTokens_AdditionalErrorBodies(t *testing.T) {
	t.Run("truncates long non-JSON body", func(t *testing.T) {
		body := strings.Repeat("x", 250)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(body))
		}))
		defer server.Close()
		_, _, _, err := exchangeCodeForTokens(context.Background(), AuthConfig{TokenURL: server.URL}, "code")
		if err == nil || !strings.Contains(err.Error(), strings.Repeat("x", 200)+"...") {
			t.Fatalf("error = %v, want truncated body", err)
		}
		if strings.Contains(err.Error(), strings.Repeat("x", 201)) {
			t.Fatal("error contains more than 200 body characters")
		}
	})

	t.Run("rejects invalid successful JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{invalid"))
		}))
		defer server.Close()
		_, _, _, err := exchangeCodeForTokens(context.Background(), AuthConfig{TokenURL: server.URL}, "code")
		if err == nil || !strings.Contains(err.Error(), "error parsing tokens") {
			t.Fatalf("error = %v, want parsing failure", err)
		}
	})
}

func TestNewManager_UsesXDGConfigHome(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	manager, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	want := filepath.Join(configHome, "onecloudriver")
	if manager.configDir != want {
		t.Errorf("configDir = %q, want %q", manager.configDir, want)
	}
}

func TestSetGraphClientFactory_OverridesFactory(t *testing.T) {
	manager, err := NewManagerWithDeps(t.TempDir(), &mockKeyring{storage: make(map[string]string)})
	if err != nil {
		t.Fatalf("NewManagerWithDeps: %v", err)
	}
	want := graph.NewClient(graph.WithBaseURL("http://example.test"))
	manager.SetGraphClientFactory(func() *graph.Client { return want })
	if got := manager.graphClientFactory(); got != want {
		t.Fatalf("graph client factory returned %p, want %p", got, want)
	}
}
