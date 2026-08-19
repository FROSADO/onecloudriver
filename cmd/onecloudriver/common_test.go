package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/spf13/cobra"
)

func TestBuildResource(t *testing.T) {
	tests := []struct {
		name      string
		itemID    string
		itemPath  string
		label     string
		wantErr   string // expected substring of the error, empty if no error expected
		wantIsID  bool
		wantIsPth bool
		wantVal   string
	}{
		{
			name:     "only id",
			itemID:   "01BYE5RZ",
			wantIsID: true,
			wantVal:  "01BYE5RZ",
		},
		{
			name:      "only path",
			itemPath:  "/Documents/photo.jpg",
			wantIsPth: true,
			wantVal:   "/Documents/photo.jpg",
		},
		{
			name:    "neither with label",
			label:   " for the source",
			wantErr: "you must specify exactly one of --id or --path for the source",
		},
		{
			name:     "both",
			itemID:   "01BYE5RZ",
			itemPath: "/Documents/photo.jpg",
			label:    " for the destination folder",
			wantErr:  "you must specify exactly one of --id or --path for the destination folder",
		},
		{
			name:    "neither without label",
			wantErr: "you must specify exactly one of --id or --path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := buildResource(tt.itemID, tt.itemPath, tt.label)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if r == nil {
				t.Fatal("expected a non-nil Resource")
			}

			switch v := r.(type) {
			case graph.ItemID:
				if !tt.wantIsID {
					t.Errorf("expected ItemPath resource, got ItemID(%q)", string(v))
				}
				if string(v) != tt.wantVal {
					t.Errorf("expected ItemID value %q, got %q", tt.wantVal, string(v))
				}
			case graph.ItemPath:
				if !tt.wantIsPth {
					t.Errorf("expected ItemID resource, got ItemPath(%q)", string(v))
				}
				if string(v) != tt.wantVal {
					t.Errorf("expected ItemPath value %q, got %q", tt.wantVal, string(v))
				}
			default:
				t.Fatalf("unexpected resource type %T", r)
			}
		})
	}
}

func TestValidateOutputFlags(t *testing.T) {
	tests := []struct {
		name       string
		outputPath string
		outputDir  string
		wantErr    string // expected error substring, empty if no error
	}{
		{name: "only output", outputPath: "./file.pdf"},
		{name: "only output-dir", outputDir: "./downloads"},
		{name: "neither", wantErr: "you must specify exactly one of --output or --output-dir"},
		{name: "both", outputPath: "./file.pdf", outputDir: "./downloads", wantErr: "you must specify exactly one of --output or --output-dir"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOutputFlags(tt.outputPath, tt.outputDir)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateDestFlags(t *testing.T) {
	tests := []struct {
		name     string
		destID   string
		destPath string
		wantErr  string
	}{
		{name: "only dest-id", destID: "01FOLDER"},
		{name: "only dest-path", destPath: "/Archive"},
		{name: "neither", wantErr: "you must specify exactly one of --dest-id or --dest-path for the destination"},
		{name: "both", destID: "01FOLDER", destPath: "/Archive", wantErr: "you must specify exactly one of --dest-id or --dest-path for the destination"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDestFlags(tt.destID, tt.destPath)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateOptionalDestFlags(t *testing.T) {
	tests := []struct {
		name     string
		destID   string
		destPath string
		wantErr  string
	}{
		{name: "neither (allowed)"},
		{name: "only dest-id", destID: "01FOLDER"},
		{name: "only dest-path", destPath: "/Archive"},
		{name: "both", destID: "01FOLDER", destPath: "/Archive", wantErr: "you must specify exactly one of --dest-id or --dest-path for the destination"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOptionalDestFlags(tt.destID, tt.destPath)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestResolveAccountNameDefault(t *testing.T) {

	setupManager(t, "test@outlook.com")

	// The setupManager() call above has already set up the rootCmd.PersistentPreRun
	// which injects the manager into the context. We need to manually call it to
	// populate the context for our test command. Create the command with a background context.
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	rootCmd.PersistentPreRun(cmd, nil)

	manager, err := getManager(cmd)
	if err != nil {
		t.Fatalf("Failed to get manager from context: %v", err)
	}

	resolvedName, err := resolveAccountName(cmd, manager)
	if err != nil {
		t.Fatal("Unexpected error resolving default account")
	}
	if resolvedName != "test@outlook.com" {
		t.Fatalf("Expected resolved account name to be 'test@outlook.com', got '%s'", resolvedName)
	}
	acc, err := resolveAccount(cmd, manager)
	if err != nil {
		t.Fatal("Unexpected error resolving default account")
	}
	if acc.Name != "test@outlook.com" {
		t.Fatalf("Expected resolved account name to be 'test@outlook.com', got '%s'", acc.Name)
	}

}

// TestGetManagerEmptyContext tests that getManager returns an error when
// the manager is not present in the context (error path coverage).
func TestGetManagerEmptyContext(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	manager, err := getManager(cmd)
	if err == nil {
		t.Fatal("expected error when manager not in context, got nil")
	}
	if manager != nil {
		t.Fatalf("expected nil manager, got %v", manager)
	}
	if !strings.Contains(err.Error(), "auth manager not initialized in context") {
		t.Errorf("expected error message containing 'auth manager not initialized in context', got: %v", err)
	}
}

// TestResolveAccountNonexistent tests resolveAccount with an account that
// does not exist (error path coverage).
func TestResolveAccountNonexistent(t *testing.T) {
	setupManager(t, "test@outlook.com")

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	rootCmd.PersistentPreRun(cmd, nil)

	manager, err := getManager(cmd)
	if err != nil {
		t.Fatalf("Failed to get manager from context: %v", err)
	}

	// Explicitly set a non-existent account name via flag
	cmd.Flags().String("account", "nonexistent@outlook.com", "Account name")
	cmd.Flags().Parse([]string{"--account", "nonexistent@outlook.com"})

	// Try to resolve the non-existent account
	acc, err := resolveAccount(cmd, manager)
	if err == nil {
		t.Fatal("expected error resolving non-existent account, got nil")
	}
	if acc != nil {
		t.Fatalf("expected nil account, got %v", acc)
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected error message containing 'does not exist', got: %v", err)
	}
}

// TestExpandHomePrefixNoTilde tests expandHomePrefix with a path that
// does not start with ~ (should return unchanged).
func TestExpandHomePrefixNoTilde(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "absolute path",
			path: "/absolute/path",
			want: "/absolute/path",
		},
		{
			name: "relative path",
			path: "relative/path",
			want: "relative/path",
		},
		{
			name: "current dir",
			path: ".",
			want: ".",
		},
		{
			name: "path with tilde not at start",
			path: "/path/with/~/tilde",
			want: "/path/with/~/tilde",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandHomePrefix(tt.path)
			if got != tt.want {
				t.Errorf("expandHomePrefix(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// TestExpandHomePrefixWithTilde tests expandHomePrefix with paths that
// contain ~ and need expansion.
func TestExpandHomePrefixWithTilde(t *testing.T) {
	// Get the actual user's home directory for comparison
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("Failed to get home directory: %v", err)
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "tilde only",
			path: "~",
			want: realHome,
		},
		{
			name: "tilde with trailing slash",
			path: "~/",
			want: realHome,
		},
		{
			name: "tilde with path",
			path: "~/Documents",
			want: filepath.Join(realHome, "Documents"),
		},
		{
			name: "tilde with nested path",
			path: "~/a/b/c",
			want: filepath.Join(realHome, "a/b/c"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandHomePrefix(tt.path)
			if got != tt.want {
				t.Errorf("expandHomePrefix(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
