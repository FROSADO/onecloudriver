package service

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceFilePath(t *testing.T) {
	t.Run("uses XDG_CONFIG_HOME when set", func(t *testing.T) {
		customDir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", customDir)

		got, err := ServiceFilePath()
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

		got, err := ServiceFilePath()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := filepath.Join(homeDir, ".config", "systemd", "user", "onecloudriver@.service")
		if got != expected {
			t.Errorf("expected %q, got %q", expected, got)
		}
	})
}

func TestServiceUnit(t *testing.T) {
	t.Run("generates valid systemd unit", func(t *testing.T) {
		unit := ServiceUnit("/home/user/OneDrive/%i")

		for _, section := range []string{"[Unit]", "[Service]", "[Install]"} {
			if !strings.Contains(unit, section) {
				t.Errorf("service unit missing %s section", section)
			}
		}
		for _, line := range []string{
			"Description=OneCloudRiver",
			"After=network-online.target",
			"Type=simple",
			"ExecStart=",
			"ExecStop=/bin/fusermount3",
			"Restart=on-failure",
			"WantedBy=default.target",
			"/home/user/OneDrive/%i",
		} {
			if !strings.Contains(unit, line) {
				t.Errorf("service unit missing %q", line)
			}
		}
	})

	t.Run("embeds mountpoint in ExecStart, ExecStop, ExecReload", func(t *testing.T) {
		mountpoint := "/mnt/onedrive/%i"
		unit := ServiceUnit(mountpoint)

		count := strings.Count(unit, mountpoint)
		if count < 3 {
			t.Errorf("expected mountpoint %q to appear at least 3 times (ExecStart, ExecStop, ExecReload), got %d", mountpoint, count)
		}
	})
}

func TestDefaultMountpointFor(t *testing.T) {
	t.Run("returns HOME/OneDrive/account", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		got := DefaultMountpointFor("user@outlook.com")
		expected := filepath.Join(homeDir, "OneDrive", "user@outlook.com")
		if got != expected {
			t.Errorf("expected %q, got %q", expected, got)
		}
	})
}
