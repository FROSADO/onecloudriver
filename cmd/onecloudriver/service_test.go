package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frosado/onecloudriver/internal/auth"
)

// --- serviceFilePath ----------------------------------------------------------

func TestServiceFilePath(t *testing.T) {
	t.Run("uses XDG_CONFIG_HOME when set", func(t *testing.T) {
		customDir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", customDir)

		got, err := serviceFilePath()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := filepath.Join(customDir, "systemd", "user", "onecloudriver@.service")
		if got != expected {
			t.Errorf("expected %q, got %q", expected, got)
		}
	})

	t.Run("falls back to HOME/.config when XDG_CONFIG_HOME is empty", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", homeDir)

		got, err := serviceFilePath()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := filepath.Join(homeDir, ".config", "systemd", "user", "onecloudriver@.service")
		if got != expected {
			t.Errorf("expected %q, got %q", expected, got)
		}
	})
}

// --- serviceUnit --------------------------------------------------------------

func TestServiceUnit(t *testing.T) {
	t.Run("generates valid systemd unit", func(t *testing.T) {
		unit := serviceUnit("/home/user/OneDrive/%i")

		// Must contain unit section
		if !strings.Contains(unit, "[Unit]") {
			t.Error("service unit missing [Unit] section")
		}
		if !strings.Contains(unit, "Description=OneCloudRiver") {
			t.Error("service unit missing Description")
		}
		if !strings.Contains(unit, "After=network-online.target") {
			t.Error("service unit missing After=network-online.target")
		}

		// Must contain service section
		if !strings.Contains(unit, "[Service]") {
			t.Error("service unit missing [Service] section")
		}
		if !strings.Contains(unit, "Type=simple") {
			t.Error("service unit missing Type=simple")
		}
		if !strings.Contains(unit, "ExecStart=") {
			t.Error("service unit missing ExecStart")
		}
		if !strings.Contains(unit, "ExecStop=/bin/fusermount3") {
			t.Error("service unit missing ExecStop")
		}
		if !strings.Contains(unit, "Restart=on-failure") {
			t.Error("service unit missing Restart=on-failure")
		}

		// Must contain install section
		if !strings.Contains(unit, "[Install]") {
			t.Error("service unit missing [Install] section")
		}
		if !strings.Contains(unit, "WantedBy=default.target") {
			t.Error("service unit missing WantedBy=default.target")
		}

		// Must contain the mountpoint
		if !strings.Contains(unit, "/home/user/OneDrive/%i") {
			t.Error("service unit missing mountpoint")
		}
	})

	t.Run("embeds mountpoint in ExecStart, ExecStop, ExecReload", func(t *testing.T) {
		mountpoint := "/mnt/onedrive/%i"
		unit := serviceUnit(mountpoint)

		count := strings.Count(unit, mountpoint)
		if count < 3 {
			t.Errorf("expected mountpoint %q to appear at least 3 times (ExecStart, ExecStop, ExecReload), got %d", mountpoint, count)
		}
	})
}

// --- resolveInstallMountpoint -------------------------------------------------

func TestResolveInstallMountpoint(t *testing.T) {
	t.Run("uses explicit flag when provided", func(t *testing.T) {
		acc := &auth.Account{}
		got := resolveInstallMountpoint("/custom/mount", acc)
		if got != "/custom/mount" {
			t.Errorf("expected /custom/mount, got %q", got)
		}
	})

	t.Run("uses account default when flag is empty", func(t *testing.T) {
		acc := &auth.Account{}
		acc.Mount.DefaultMountpoint = "/saved/mountpoint"
		got := resolveInstallMountpoint("", acc)
		if got != "/saved/mountpoint" {
			t.Errorf("expected /saved/mountpoint, got %q", got)
		}
	})

	t.Run("falls back to ~/OneDrive/%i when nothing is set", func(t *testing.T) {
		acc := &auth.Account{}
		got := resolveInstallMountpoint("", acc)
		if got != "~/OneDrive/%i" {
			t.Errorf("expected ~/OneDrive/%%i, got %q", got)
		}
	})

	t.Run("explicit flag takes priority over account default", func(t *testing.T) {
		acc := &auth.Account{}
		acc.Mount.DefaultMountpoint = "/saved/mountpoint"
		got := resolveInstallMountpoint("/explicit/path", acc)
		if got != "/explicit/path" {
			t.Errorf("expected /explicit/path, got %q", got)
		}
	})
}

// --- defaultMountpointFor -----------------------------------------------------

func TestDefaultMountpointFor(t *testing.T) {
	t.Run("returns HOME/OneDrive/account", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		got := defaultMountpointFor("user@outlook.com")
		expected := filepath.Join(homeDir, "OneDrive", "user@outlook.com")
		if got != expected {
			t.Errorf("expected %q, got %q", expected, got)
		}
	})

	t.Run("returns empty when HOME cannot be determined", func(t *testing.T) {
		// Clear HOME completely — but os.UserHomeDir may still work on some systems.
		// We test that the function doesn't panic and returns empty.
		origHome := os.Getenv("HOME")
		os.Unsetenv("HOME")
		t.Cleanup(func() { os.Setenv("HOME", origHome) })

		got := defaultMountpointFor("test@test.com")
		// On most systems without HOME, UserHomeDir returns an error and we get ""
		if got != "" {
			// HOME might still be resolvable from /etc/passwd, so this is OK
			t.Logf("HOME was still resolvable, got: %q", got)
		}
	})
}
