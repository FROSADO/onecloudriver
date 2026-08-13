package service

import (
	"os"
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
		unit := ServiceUnit("/home/user/OneDrive/%i", "/usr/local/bin/onecloudriver")

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
			"RestartSec=10",
			"StandardOutput=journal",
			"StandardError=journal",
			"WantedBy=default.target",
			"/home/user/OneDrive/%i",
		} {
			if !strings.Contains(unit, line) {
				t.Errorf("service unit missing %q", line)
			}
		}
		if strings.Contains(unit, "~") {
			t.Errorf("service unit must not contain a literal '~' (systemd does not expand it in ExecStart):\n%s", unit)
		}
	})

	t.Run("normalizes a tilde mountpoint to the %h specifier", func(t *testing.T) {
		unit := ServiceUnit("~/OneDrive/%i", "/usr/local/bin/onecloudriver")

		if !strings.Contains(unit, "mount %h/OneDrive/%i -a %i") {
			t.Errorf("expected ExecStart to use %%h/OneDrive/%%i, got:\n%s", unit)
		}
		if strings.Contains(unit, "~") {
			t.Errorf("service unit must not contain a literal '~', got:\n%s", unit)
		}
	})

	t.Run("leaves bare %h home specifier untouched", func(t *testing.T) {
		unit := ServiceUnit("%h/OneDrive/%i", "/usr/local/bin/onecloudriver")
		if !strings.Contains(unit, "mount %h/OneDrive/%i -a %i") {
			t.Errorf("expected ExecStart to keep %%h/OneDrive/%%i, got:\n%s", unit)
		}
	})

	t.Run("embeds mountpoint in ExecStart, ExecStop, ExecReload", func(t *testing.T) {
		mountpoint := "/mnt/onedrive/%i"
		unit := ServiceUnit(mountpoint, "/usr/local/bin/onecloudriver")

		count := strings.Count(unit, mountpoint)
		if count < 3 {
			t.Errorf("expected mountpoint %q to appear at least 3 times (ExecStart, ExecStop, ExecReload), got %d", mountpoint, count)
		}
	})

	t.Run("embeds the resolved binary in ExecStart", func(t *testing.T) {
		binary := "/usr/local/bin/onecloudriver"
		unit := ServiceUnit("/home/user/OneDrive/%i", binary)
		if !strings.Contains(unit, "ExecStart="+binary+" mount") {
			t.Errorf("expected ExecStart to reference %q, got:\n%s", binary, unit)
		}
	})
}

func TestResolveBinary(t *testing.T) {
	t.Run("resolves a bare name from PATH", func(t *testing.T) {
		binDir := t.TempDir()
		bin := filepath.Join(binDir, "onecloudriver")
		writeExecutable(t, bin)
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		got, err := resolveBinary("onecloudriver")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != bin {
			t.Errorf("expected %q, got %q", bin, got)
		}
	})

	t.Run("resolves an explicit absolute path", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "onecloudriver")
		writeExecutable(t, bin)

		got, err := resolveBinary(bin)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != bin {
			t.Errorf("expected %q, got %q", bin, got)
		}
	})

	t.Run("rejects a test-binary argv0 and falls back to PATH lookup", func(t *testing.T) {
		// os.Args[0] pointing to a .test binary must never be written into the
		// unit; resolveBinary must reject it and fall back to the canonical name.
		binDir := t.TempDir()
		bin := filepath.Join(binDir, "onecloudriver")
		writeExecutable(t, bin)
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		got, err := resolveBinary("/tmp/go-build123/b001/service.test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != bin {
			t.Errorf("expected fallback to %q, got %q", bin, got)
		}
	})

	t.Run("rejects a nonexistent binary path", func(t *testing.T) {
		if _, err := resolveBinary(filepath.Join(t.TempDir(), "missing")); err == nil {
			t.Error("expected error for a nonexistent binary path, got nil")
		}
	})

	t.Run("rejects a bare name not on PATH", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if _, err := resolveBinary("onecloudriver"); err == nil {
			t.Error("expected error for a bare name not on PATH, got nil")
		}
	})

	t.Run("rejects a directory passed as the binary", func(t *testing.T) {
		if _, err := resolveBinary(t.TempDir()); err == nil {
			t.Error("expected error for a directory, got nil")
		}
	})
}

// writeExecutable creates an executable regular file at path for use as a
// fake binary in resolveBinary tests.
func writeExecutable(t *testing.T, path string) {
	t.Helper()
	//#nosec G306 -- test helper: creates an executable fake binary on purpose
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("failed to write fake binary: %v", err)
	}
}

func TestConcreteMountpoint(t *testing.T) {
	t.Run("expands %i to the account", func(t *testing.T) {
		got := concreteMountpoint("/home/user/OneDrive/%i", "user@outlook.com")
		expected := "/home/user/OneDrive/user@outlook.com"
		if got != expected {
			t.Errorf("expected %q, got %q", expected, got)
		}
	})

	t.Run("expands %h to the home directory", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		got := concreteMountpoint("%h/OneDrive/%i", "user@outlook.com")
		expected := filepath.Join(homeDir, "OneDrive", "user@outlook.com")
		if got != expected {
			t.Errorf("expected %q, got %q", expected, got)
		}
	})

	t.Run("leaves a concrete path unchanged", func(t *testing.T) {
		got := concreteMountpoint("/mnt/onedrive/account", "user@outlook.com")
		expected := "/mnt/onedrive/account"
		if got != expected {
			t.Errorf("expected %q, got %q", expected, got)
		}
	})
}

func TestEnsureMountpointDir(t *testing.T) {
	t.Run("creates the directory when missing", func(t *testing.T) {
		base := t.TempDir()
		dir := filepath.Join(base, "OneDrive", "user@outlook.com")

		if err := ensureMountpointDir(filepath.Join(base, "OneDrive/%i"), "user@outlook.com"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("expected directory %q to exist: %v", dir, err)
		}
		if !info.IsDir() {
			t.Errorf("expected %q to be a directory", dir)
		}
	})

	t.Run("is a no-op when the directory already exists", func(t *testing.T) {
		base := t.TempDir()
		dir := filepath.Join(base, "OneDrive")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if err := ensureMountpointDir(dir, "user@outlook.com"); err != nil {
			t.Fatalf("unexpected error: %v", err)
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
