package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frosado/onecloudriver/internal/auth"
)

func TestResolveInstallMountpoint(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	fallback := filepath.Join(home, "OneDrive", "%i")

	newAcc := func(defaultMountpoint string) *auth.Account {
		return &auth.Account{
			Name:  "user@example.com",
			Mount: auth.AccountPersistedConfig{DefaultMountpoint: defaultMountpoint},
		}
	}

	t.Run("explicit flag wins over saved mountpoint", func(t *testing.T) {
		mp, warn := resolveInstallMountpoint("/mnt/onedrive/%i", newAcc("/saved/path"), nil)
		if mp != "/mnt/onedrive/%i" {
			t.Errorf("resolveInstallMountpoint = %q, want explicit flag", mp)
		}
		if warn != "" {
			t.Errorf("warning = %q, want empty for an explicit flag", warn)
		}
	})

	t.Run("reuses saved mountpoint when it contains %i", func(t *testing.T) {
		mp, warn := resolveInstallMountpoint("", newAcc("/home/user/OneDrive/%i"), nil)
		if mp != "/home/user/OneDrive/%i" {
			t.Errorf("resolveInstallMountpoint = %q, want saved template", mp)
		}
		if warn != "" {
			t.Errorf("warning = %q, want empty when the saved value is a template", warn)
		}
	})

	t.Run("expands tilde in a saved %i template", func(t *testing.T) {
		mp, warn := resolveInstallMountpoint("", newAcc("~/OneDrive/%i"), nil)
		if mp != fallback {
			t.Errorf("resolveInstallMountpoint = %q, want %q", mp, fallback)
		}
		if warn != "" {
			t.Errorf("warning = %q, want empty when the saved value is a template", warn)
		}
	})

	t.Run("ignores concrete saved mountpoint and uses fallback", func(t *testing.T) {
		mp, warn := resolveInstallMountpoint("", newAcc("/home/user/OneDrive/paveryutu72@hotmail.com"), nil)
		if mp != fallback {
			t.Errorf("resolveInstallMountpoint = %q, want fallback %q", mp, fallback)
		}
		if warn == "" {
			t.Error("expected a non-empty warning when a concrete mountpoint is ignored")
		}
		if !strings.Contains(warn, "no %i placeholder") {
			t.Errorf("warning = %q, want it to mention the missing %%i placeholder", warn)
		}
	})

	t.Run("uses fallback when no saved mountpoint", func(t *testing.T) {
		mp, warn := resolveInstallMountpoint("", newAcc(""), nil)
		if mp != fallback {
			t.Errorf("resolveInstallMountpoint = %q, want fallback %q", mp, fallback)
		}
		if warn != "" {
			t.Errorf("warning = %q, want empty when there is nothing to ignore", warn)
		}
	})

	t.Run("prints the warning to the diagnostics writer", func(t *testing.T) {
		var buf bytes.Buffer
		mp, warn := resolveInstallMountpoint("", newAcc("/home/user/OneDrive/paveryutu72@hotmail.com"), &buf)
		if mp != fallback {
			t.Errorf("resolveInstallMountpoint = %q, want fallback %q", mp, fallback)
		}
		if warn == "" {
			t.Fatal("expected a non-empty warning")
		}
		if msg := buf.String(); !strings.Contains(msg, warn) {
			t.Errorf("diagnostics %q do not include the warning %q", msg, warn)
		}
	})
}
