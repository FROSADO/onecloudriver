package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runConfirm calls confirmCacheDeletion with the given inputs while capturing
// os.Stdout, and returns both the captured output and the returned error.
func runConfirm(t *testing.T, cacheDir string, purge, keep bool, stdin string) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	callErr := confirmCacheDeletion(cacheDir, purge, keep, strings.NewReader(stdin))

	if err := w.Close(); err != nil {
		t.Fatalf("w.Close: %v", err)
	}
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("io.ReadAll: %v", err)
	}
	return string(out), callErr
}

func TestConfirmCacheDeletion(t *testing.T) {
	tests := []struct {
		name       string
		purge      bool
		keep       bool
		stdin      string
		prep       func(t *testing.T) string // optional custom cacheDir; default is a real empty dir
		wantExists bool                      // whether cacheDir should still exist afterwards
		wantErr    string                    // error substring, empty if none expected
		wantStdout string                    // stdout substring, empty if don't care
	}{
		{
			name:       "keep preserves cache",
			keep:       true,
			wantExists: true,
			wantStdout: "Cache preserved at:",
		},
		{
			name:       "purge deletes cache",
			purge:      true,
			wantExists: false,
			wantStdout: "Cache deleted:",
		},
		{
			name:       "prompt y deletes",
			stdin:      "y\n",
			wantExists: false,
			wantStdout: "Cache deleted:",
		},
		{
			name:       "prompt yes deletes",
			stdin:      "yes\n",
			wantExists: false,
			wantStdout: "Cache deleted:",
		},
		{
			name:       "prompt n preserves",
			stdin:      "n\n",
			wantExists: true,
			wantStdout: "Cache preserved.",
		},
		{
			name:       "prompt invalid preserves",
			stdin:      "maybe\n",
			wantExists: true,
			wantStdout: "Cache preserved.",
		},
		{
			name:       "non-existent dir tolerated",
			purge:      true,
			wantExists: false,
			wantStdout: "Cache deleted:",
			prep: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "does-not-exist")
			},
		},
		{
			name:    "remove error surfaces",
			purge:   true,
			wantErr: "error deleting cache",
			prep: func(t *testing.T) string {
				dir := t.TempDir()
				file := filepath.Join(dir, "file")
				if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
				// RemoveAll on a path under a regular file returns ENOTDIR,
				// a non-IsNotExist error that must be wrapped and returned.
				return filepath.Join(file, "sub")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cacheDir string
			if tt.prep != nil {
				cacheDir = tt.prep(t)
			} else {
				cacheDir = filepath.Join(t.TempDir(), "cache")
				if err := os.MkdirAll(cacheDir, 0700); err != nil {
					t.Fatalf("MkdirAll: %v", err)
				}
			}

			out, err := runConfirm(t, cacheDir, tt.purge, tt.keep, tt.stdin)

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantStdout != "" && !strings.Contains(out, tt.wantStdout) {
				t.Errorf("expected stdout containing %q, got %q", tt.wantStdout, out)
			}

			_, statErr := os.Stat(cacheDir)
			exists := statErr == nil
			if exists != tt.wantExists {
				t.Errorf("cacheDir exists=%v, want %v (statErr=%v)", exists, tt.wantExists, statErr)
			}
		})
	}
}
