package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// binaryPath holds the path to the compiled onecloudriver binary.
// Set by TestMain before any tests run.
var binaryPath string

func TestMain(m *testing.M) {
	// Skip the full binary build in CI (no keyring available, longer build times).
	// Smoke tests will be skipped via binaryAvailable.
	if os.Getenv("GITHUB_ACTIONS") == "true" || os.Getenv("CI") == "true" {
		os.Exit(m.Run())
	}

	// Build the binary in a temporary directory.
	tmpDir, err := os.MkdirTemp("", "onecloudriver-smoke-*")
	if err != nil {
		panic("failed to create temp dir: " + err.Error())
	}
	defer os.RemoveAll(tmpDir)

	binaryPath = filepath.Join(tmpDir, "onecloudriver")
	build := exec.Command("go", "build", "-o", binaryPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		panic("failed to build binary: " + err.Error() + "\n" + string(out))
	}

	code := m.Run()
	os.Exit(code)
}

// exitCode extracts the exit code from an exec error, returning -1 for
// non-exit errors and 0 for nil.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// runBinary executes the compiled binary with the given arguments.
// It returns combined stdout+stderr and the exit code.
func runBinary(args ...string) (string, int) {
	cmd := exec.Command(binaryPath, args...)
	out, err := cmd.CombinedOutput()
	return string(out), exitCode(err)
}

// binaryAvailable is a helper that skips the test if the binary wasn't built.
func binaryAvailable(t *testing.T) {
	t.Helper()
	if binaryPath == "" {
		t.Skip("binary not built (CI environment or -short flag)")
	}
}

// runBinaryTimeout runs the binary with a timeout (for commands that invoke
// PersistentPreRun and may hang on keyring access).
func runBinaryTimeout(timeout time.Duration, args ...string) (string, int, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, args...)
	out, err := cmd.CombinedOutput()
	timedOut := false
	code := 0
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			timedOut = true
		} else {
			code = exitCode(err)
		}
	}
	return string(out), code, timedOut
}

// ─── Smoke: --help ───────────────────────────────────────────────────────────

func TestSmoke_Help(t *testing.T) {
	binaryAvailable(t)

	out, code := runBinary("--help")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d\nOutput:\n%s", code, out)
	}

	// The help output should mention the main subcommands.
	required := []string{
		"onecloudriver",
		"account",
		"mount",
		"list",
		"info",
		"download",
		"upload",
		"mkdir",
		"rm",
		"rename",
		"mv",
		"copy",
		"service",
	}
	for _, sub := range required {
		if !strings.Contains(out, sub) {
			t.Errorf("help output missing expected subcommand %q", sub)
		}
	}
}

// ─── Smoke: --version ────────────────────────────────────────────────────────

func TestSmoke_Version(t *testing.T) {
	binaryAvailable(t)

	out, code := runBinary("--version")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d\nOutput:\n%s", code, out)
	}

	// Default build (no ldflags) shows "onecloudriver version dev"
	if !strings.Contains(out, "onecloudriver version") {
		t.Errorf("version output missing expected prefix: %q", out)
	}
}

// ─── Smoke: subcommand help (bypass PersistentPreRun) ────────────────────────

func TestSmoke_AccountHelp(t *testing.T) {
	binaryAvailable(t)

	out, code := runBinary("account", "--help")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d\nOutput:\n%s", code, out)
	}
	if !strings.Contains(out, "add") || !strings.Contains(out, "list") || !strings.Contains(out, "remove") {
		t.Errorf("account --help missing expected subcommands: %s", out)
	}
}

func TestSmoke_MountHelp(t *testing.T) {
	binaryAvailable(t)

	out, code := runBinary("mount", "--help")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d\nOutput:\n%s", code, out)
	}
	if !strings.Contains(out, "--account") {
		t.Errorf("mount --help missing --account flag: %s", out)
	}
}

func TestSmoke_ServiceHelp(t *testing.T) {
	binaryAvailable(t)

	out, code := runBinary("service", "--help")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d\nOutput:\n%s", code, out)
	}
	for _, sub := range []string{"install", "uninstall", "status", "start", "stop"} {
		if !strings.Contains(out, sub) {
			t.Errorf("service --help missing %q subcommand", sub)
		}
	}
}

func TestSmoke_DownloadHelp(t *testing.T) {
	binaryAvailable(t)

	out, code := runBinary("download", "--help")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d\nOutput:\n%s", code, out)
	}
	if !strings.Contains(out, "--output") {
		t.Errorf("download --help missing --output flag: %s", out)
	}
}

func TestSmoke_UploadHelp(t *testing.T) {
	binaryAvailable(t)

	out, code := runBinary("upload", "--help")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d\nOutput:\n%s", code, out)
	}
	if !strings.Contains(out, "--file") {
		t.Errorf("upload --help missing --file flag: %s", out)
	}
}

func TestSmoke_ListHelp(t *testing.T) {
	binaryAvailable(t)

	out, code := runBinary("list", "--help")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d\nOutput:\n%s", code, out)
	}
	if !strings.Contains(out, "--output") {
		t.Errorf("list --help missing --output flag: %s", out)
	}
}

// ─── Smoke: account list (uses PersistentPreRun → NewManager) ────────────────

func TestSmoke_AccountList(t *testing.T) {
	binaryAvailable(t)

	// Use a temporary XDG_CONFIG_HOME so the binary sees zero configured accounts.
	tmpDir := t.TempDir()

	// Use a timeout: PersistentPreRun may try to access the keyring.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, "account", "list")
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+tmpDir)

	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Skip("account list timed out (keyring unavailable)")
	}
	if err != nil {
		t.Fatalf("account list failed: %v\nOutput:\n%s", err, out)
	}

	outs := string(out)
	if !strings.Contains(outs, "No accounts configured") {
		t.Errorf("expected 'No accounts configured', got: %s", outs)
	}
}

// ─── Smoke: invalid command ──────────────────────────────────────────────────

func TestSmoke_InvalidCommand(t *testing.T) {
	binaryAvailable(t)

	out, code := runBinary("nonexistent")
	if code == 0 {
		t.Errorf("expected non-zero exit for invalid command\nOutput:\n%s", out)
	}
}

// ─── Smoke: account remove with no args ──────────────────────────────────────

func TestSmoke_AccountRemove_MissingArg(t *testing.T) {
	binaryAvailable(t)

	out, code, timedOut := runBinaryTimeout(10*time.Second, "account", "remove")
	if timedOut {
		t.Skip("account remove timed out (keyring unavailable)")
	}
	if code == 0 {
		t.Errorf("account remove with no args should return non-zero exit code\nOutput:\n%s", out)
	}
}
