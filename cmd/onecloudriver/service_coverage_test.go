package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frosado/onecloudriver/internal/auth"
	"github.com/frosado/onecloudriver/internal/service"
	"github.com/spf13/cobra"
)

func newServiceCoverageManager(t *testing.T, accounts ...string) *auth.Manager {
	t.Helper()
	configDir := t.TempDir()
	for _, account := range accounts {
		writeAccountJSON(t, configDir, account)
	}
	keyring := &mockKeyringForCLI{storage: make(map[string]string)}
	manager, err := auth.NewManagerWithDeps(configDir, keyring)
	if err != nil {
		t.Fatalf("NewManagerWithDeps: %v", err)
	}
	return manager
}

func setupServiceCommandFakes(t *testing.T, systemctlScript string) {
	t.Helper()
	binDir := t.TempDir()
	writeExecutable := func(name, content string) {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte(content), 0700); err != nil { //nolint:gosec // test-only fake executables must be executable
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}
	writeExecutable("onecloudriver", "#!/bin/sh\nexit 0\n")
	writeExecutable("systemctl", systemctlScript)
	writeExecutable("mount", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestResolveServiceAccount_AutoDetectsAndValidates(t *testing.T) {
	manager := newServiceCoverageManager(t, "user@example.com")
	cmd := &cobra.Command{}
	cmd.Flags().String("account", "", "")

	account, err := resolveServiceAccount(cmd, manager, true)
	if err != nil {
		t.Fatalf("resolveServiceAccount auto-detect: %v", err)
	}
	if account.Name != "user@example.com" {
		t.Errorf("account = %q, want user@example.com", account.Name)
	}
	cmd.Flags().Set("account", "")
	output := captureStdoutForTest(t, func() error {
		_, err := resolveServiceAccount(cmd, manager, false)
		return err
	})
	if !strings.Contains(output, "Using the only default account") {
		t.Errorf("auto-detect output = %q, want default-account message", output)
	}

	cmd.Flags().Set("account", "missing@example.com")
	if _, err := resolveServiceAccount(cmd, manager, true); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing account error = %v, want account-not-found context", err)
	}

	emptyManager := newServiceCoverageManager(t)
	cmd.Flags().Set("account", "")
	if _, err := resolveServiceAccount(cmd, emptyManager, true); err == nil || !strings.Contains(err.Error(), "specify an account") {
		t.Fatalf("no-account error = %v, want account-selection context", err)
	}
}

func TestInstallAllStructured(t *testing.T) {
	setupServiceCommandFakes(t, "#!/bin/sh\nexit 0\n")
	manager := newServiceCoverageManager(t, "z@example.com", "a@example.com")
	cmd := &cobra.Command{}
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	err := installAllStructured(cmd, manager, "~/OneDrive/%i", false, "json")
	if err != nil {
		t.Fatalf("installAllStructured: %v", err)
	}
	var result service.ActionResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("structured output is not valid JSON: %v\n%s", err, out.String())
	}
	if !result.OK || len(result.AffectedAccounts) != 2 ||
		result.AffectedAccounts[0] != "a@example.com" || result.AffectedAccounts[1] != "z@example.com" {
		t.Errorf("result = %#v, want successful aggregate result", result)
	}
}

func TestInstallAllStructured_WithEnable(t *testing.T) {
	setupServiceCommandFakes(t, "#!/bin/sh\nexit 0\n")
	manager := newServiceCoverageManager(t, "user@example.com")
	cmd := &cobra.Command{}
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	mountpoint := filepath.Join(os.Getenv("HOME"), "OneDrive", "%i")
	if err := installAllStructured(cmd, manager, mountpoint, true, "yaml"); err != nil {
		t.Fatalf("installAllStructured with enable: %v", err)
	}
	if !strings.Contains(out.String(), "affected_accounts") {
		t.Errorf("YAML output = %q, want affected_accounts", out.String())
	}
}

func TestInstallAllText(t *testing.T) {
	setupServiceCommandFakes(t, "#!/bin/sh\nexit 0\n")
	manager := newServiceCoverageManager(t, "z@example.com", "a@example.com")
	cmd := &cobra.Command{}

	mountpoint := filepath.Join(os.Getenv("HOME"), "OneDrive", "%i")
	if err := installAllText(cmd, manager, mountpoint, true); err != nil {
		t.Fatalf("installAllText: %v", err)
	}
	for _, account := range []string{"a@example.com", "z@example.com"} {
		if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), "OneDrive", account)); err != nil {
			t.Errorf("mountpoint for %s was not created: %v", account, err)
		}
	}
}

func TestInstallAllText_ReturnsInstallFailure(t *testing.T) {
	setupServiceCommandFakes(t, "#!/bin/sh\nexit 1\n")
	manager := newServiceCoverageManager(t, "user@example.com")
	if err := installAllText(&cobra.Command{}, manager, filepath.Join(os.Getenv("HOME"), "OneDrive", "%i"), false); err == nil {
		t.Fatal("installAllText should return an installation error")
	}
}

func TestInstallAllText_NoAccounts(t *testing.T) {
	manager := newServiceCoverageManager(t)
	if err := installAllText(&cobra.Command{}, manager, "", false); err == nil {
		t.Fatal("installAllText should reject an empty account list")
	}
}

func TestInstallAllStructured_ErrorBranches(t *testing.T) {
	t.Run("no accounts", func(t *testing.T) {
		manager := newServiceCoverageManager(t)
		cmd := &cobra.Command{}
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := installAllStructured(cmd, manager, "", false, "json"); err == nil {
			t.Fatal("installAllStructured should reject an empty account list")
		}
	})

	t.Run("install failure", func(t *testing.T) {
		setupServiceCommandFakes(t, "#!/bin/sh\nexit 1\n")
		manager := newServiceCoverageManager(t, "user@example.com")
		cmd := &cobra.Command{}
		var out bytes.Buffer
		cmd.SetOut(&out)
		err := installAllStructured(cmd, manager, filepath.Join(os.Getenv("HOME"), "OneDrive", "%i"), false, "json")
		if err == nil {
			t.Fatal("installAllStructured should return an installation error")
		}
		var result service.ActionResult
		if decodeErr := json.Unmarshal(out.Bytes(), &result); decodeErr != nil {
			t.Fatalf("failure document is not valid JSON: %v", decodeErr)
		}
		if result.OK || result.Error == "" {
			t.Errorf("failure result = %#v, want an error result", result)
		}
	})

	t.Run("enable failure", func(t *testing.T) {
		setupServiceCommandFakes(t, "#!/bin/sh\nif [ \"$2\" = \"enable\" ]; then exit 1; fi\nexit 0\n")
		manager := newServiceCoverageManager(t, "user@example.com")
		cmd := &cobra.Command{}
		var out bytes.Buffer
		cmd.SetOut(&out)
		err := installAllStructured(cmd, manager, filepath.Join(os.Getenv("HOME"), "OneDrive", "%i"), true, "json")
		if err == nil {
			t.Fatal("installAllStructured should return an enable error")
		}
		var result service.ActionResult
		if decodeErr := json.Unmarshal(out.Bytes(), &result); decodeErr != nil {
			t.Fatalf("enable failure document is not valid JSON: %v", decodeErr)
		}
		if result.OK || !strings.Contains(result.Error, "error enabling systemd unit") {
			t.Errorf("enable failure result = %#v", result)
		}
	})
}

func TestStopAllStructured(t *testing.T) {
	setupServiceCommandFakes(t, "#!/bin/sh\nexit 0\n")
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := stopAllStructured(cmd, []string{"z@example.com", "a@example.com"}, "json"); err != nil {
		t.Fatalf("stopAllStructured: %v", err)
	}
	var result service.ActionResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("structured output is not valid JSON: %v\n%s", err, out.String())
	}
	if !result.OK || result.Message != "All accounts stopped." {
		t.Errorf("result = %#v, want successful all-stopped result", result)
	}
	if got := strings.Join(result.AffectedAccounts, ","); got != "a@example.com,z@example.com" {
		t.Errorf("affected accounts = %q, want sorted accounts", got)
	}
}

func TestStopAllStructured_ReturnsFailureDocument(t *testing.T) {
	setupServiceCommandFakes(t, "#!/bin/sh\nexit 1\n")
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	err := stopAllStructured(cmd, []string{"user@example.com"}, "json")
	if err == nil {
		t.Fatal("stopAllStructured should return the systemctl error")
	}
	var result service.ActionResult
	if decodeErr := json.Unmarshal(out.Bytes(), &result); decodeErr != nil {
		t.Fatalf("failure document is not valid JSON: %v\n%s", decodeErr, out.String())
	}
	if result.OK || result.Error == "" {
		t.Errorf("failure result = %#v, want ok=false and error", result)
	}
}

func TestWriteServiceStructured_ReturnsSerializationError(t *testing.T) {
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := writeServiceStructured(cmd, "json", func() {}); err == nil {
		t.Fatal("writeServiceStructured should return JSON serialization errors")
	}
}
