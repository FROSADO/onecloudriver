package main

import (
	"path/filepath"
	"testing"

	"github.com/frosado/onecloudriver/internal/auth"
)

// resolveInstallMountpoint is the only service-related helper that remains
// in the CLI package (it depends on auth.Account and flag UX).
// The systemd logic it supports lives in internal/service (see
// internal/service/systemd_test.go).
func TestResolveInstallMountpoint(t *testing.T) {
	t.Run("uses explicit flag when provided", func(t *testing.T) {
		acc := &auth.Account{}
		got := resolveInstallMountpoint("/custom/mount", acc)
		if got != "/custom/mount" {
			t.Errorf("expected /custom/mount, got %q", got)
		}
	})

	t.Run("expands a tilde in the explicit flag to the absolute home path", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		acc := &auth.Account{}
		got := resolveInstallMountpoint("~/OneDrive/%i", acc)
		expected := filepath.Join(homeDir, "OneDrive/%i")
		if got != expected {
			t.Errorf("expected %q, got %q", expected, got)
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

	t.Run("expands a tilde in the saved default to the absolute home path", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		acc := &auth.Account{}
		acc.Mount.DefaultMountpoint = "~/OneDrive"
		got := resolveInstallMountpoint("", acc)
		expected := filepath.Join(homeDir, "OneDrive")
		if got != expected {
			t.Errorf("expected %q, got %q", expected, got)
		}
	})

	t.Run("falls back to the absolute form of ~/OneDrive/%i when nothing is set", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		acc := &auth.Account{}
		got := resolveInstallMountpoint("", acc)
		expected := filepath.Join(homeDir, "OneDrive/%i")
		if got != expected {
			t.Errorf("expected %q, got %q", expected, got)
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
