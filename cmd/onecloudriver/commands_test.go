package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/auth"
	"github.com/spf13/cobra"
	flag "github.com/spf13/pflag"
)

// =============================================================================
// CLI RunE early-validation tests
// =============================================================================

// originalPersistentPreRun is the original rootCmd.PersistentPreRun saved
// before any test modifies it. All tests restore to this value.
var originalPersistentPreRun = rootCmd.PersistentPreRun

// mockKeyringForCLI is a minimal in-memory keyring for CLI tests.
type mockKeyringForCLI struct {
	storage map[string]string
}

func (m *mockKeyringForCLI) Get(service, user string) (string, error) {
	if v, ok := m.storage[service+":"+user]; ok {
		return v, nil
	}
	return "", fmt.Errorf("not found")
}

func (m *mockKeyringForCLI) Set(service, user, password string) error {
	m.storage[service+":"+user] = password
	return nil
}

func (m *mockKeyringForCLI) Delete(service, user string) error {
	delete(m.storage, service+":"+user)
	return nil
}

// accountJSONTemplate is the structure that gets serialized to disk.
// Mirrors auth.accountJSON but is defined here for test setup.
type accountJSONTemplate struct {
	Name      string                      `json:"name"`
	Config    auth.AuthConfig             `json:"config"`
	Mount     auth.AccountPersistedConfig `json:"mount,omitempty"`
	ExpiresAt int64                       `json:"expires_at"`
}

// writeAccountJSON creates an account JSON file in the given directory.
func writeAccountJSON(t *testing.T, dir, accountName string) {
	t.Helper()

	cfg := auth.AuthConfig{}
	cfg.ApplyDefaults()

	tmpl := accountJSONTemplate{
		Name:      accountName,
		Config:    cfg,
		ExpiresAt: time.Now().Unix() + 3600,
	}

	data, err := json.MarshalIndent(tmpl, "", "  ")
	if err != nil {
		t.Fatalf("marshal account JSON: %v", err)
	}

	safeName := strings.ReplaceAll(strings.ReplaceAll(accountName, ".", "_"), "@", "_")
	jsonPath := filepath.Join(dir, safeName+".json")
	if err := os.WriteFile(jsonPath, data, 0600); err != nil {
		t.Fatalf("write account JSON: %v", err)
	}
}

// setupManager creates a Manager in a temp directory with a pre-created
// account JSON file. Overrides rootCmd.PersistentPreRun to inject the manager
// into the context without using a global variable (issue #5).
func setupManager(t *testing.T, accountName string) { //nolint:unparam // accountName is a parameter for future tests with different account names
	t.Helper()

	tempDir := t.TempDir()

	// Pre-create account JSON so NewManagerWithDeps loads it via loadAccounts()
	writeAccountJSON(t, tempDir, accountName)

	mockKR := &mockKeyringForCLI{storage: make(map[string]string)}
	// Pre-populate keyring with tokens
	mockKR.Set("onecloudriver", "onecloudriver:"+accountName, "test-refresh-token")
	mockKR.Set("onecloudriver", "onecloudriver:access:"+accountName, "test-access-token")

	m, err := auth.NewManagerWithDeps(tempDir, mockKR)
	if err != nil {
		t.Fatalf("NewManagerWithDeps: %v", err)
	}

	// Override PersistentPreRun to inject manager into context instead of using global
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		_ = auth.InitLogging()
		// Inject manager and graph.Client into context (issue #5, issue #10)
		ctx := contextWithManager(cmd.Context(), m)
		ctx = contextWithClient(ctx, getClient(cmd)) // fallback client
		cmd.SetContext(ctx)
	}
	t.Cleanup(func() {
		rootCmd.PersistentPreRun = originalPersistentPreRun
		rootCmd.SetArgs(nil)
	})
}

// resetFlagValues clears all flag values on a cobra.Command back to their
// defaults. This prevents state leakage when Execute() is called multiple
// times on the same command in tests.
func resetFlagValues(cmd *cobra.Command) {
	cmd.Flags().VisitAll(func(f *flag.Flag) {
		f.Value.Set(f.DefValue)
		f.Changed = false
	})
	for _, sub := range cmd.Commands() {
		resetFlagValues(sub)
	}
}

// execCmd executes a cobra subcommand via rootCmd with the given args.
// The first arg must be the subcommand name (e.g., "download").
func execCmd(subcommand string, args ...string) error {
	resetFlagValues(rootCmd)
	allArgs := append([]string{subcommand}, args...)
	rootCmd.SetArgs(allArgs)
	return rootCmd.Execute()
}

// =============================================================================
// mount — missing account when no accounts configured
// =============================================================================

func TestMountCmd_NoAccountFlag(t *testing.T) {
	tempDir := t.TempDir()
	mockKR := &mockKeyringForCLI{storage: make(map[string]string)}
	m, err := auth.NewManagerWithDeps(tempDir, mockKR)
	if err != nil {
		t.Fatalf("NewManagerWithDeps: %v", err)
	}

	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		ctx := contextWithManager(cmd.Context(), m)
		ctx = contextWithClient(ctx, getClient(cmd))
		cmd.SetContext(ctx)
	}
	t.Cleanup(func() {
		rootCmd.PersistentPreRun = originalPersistentPreRun
		rootCmd.SetArgs(nil)
	})

	err = execCmd("mount", "/tmp/mountpoint")
	if err == nil {
		t.Fatal("expected error when no accounts are configured")
	}
	if !strings.Contains(err.Error(), "specify an account") {
		t.Errorf("expected 'specify an account' error, got: %v", err)
	}
}

// =============================================================================
// account remove — purge/keep mutual exclusivity
// =============================================================================

func TestAccountRemoveCmd_PurgeAndKeepMutuallyExclusive(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("account", "remove", "test@outlook.com", "--purge", "--keep")
	if err == nil {
		t.Fatal("expected error when --purge and --keep are both set")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually exclusive error, got: %v", err)
	}
}

// =============================================================================
// download — mutually exclusive flags
// =============================================================================

func TestDownloadCmd_MissingIDAndPath(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("download",
		"--account", "test@outlook.com",
		"--output", "/tmp/out.pdf",
	)
	if err == nil {
		t.Fatal("expected error when neither --id nor --path is specified")
	}
	if !strings.Contains(err.Error(), "exactly one of --id or --path") {
		t.Errorf("expected 'exactly one of --id or --path', got: %v", err)
	}
}

func TestDownloadCmd_BothIDAndPath(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("download",
		"--account", "test@outlook.com",
		"--id", "ABC123",
		"--path", "/Docs/file.pdf",
		"--output", "/tmp/out.pdf",
	)
	if err == nil {
		t.Fatal("expected error when both --id and --path are specified")
	}
	if !strings.Contains(err.Error(), "exactly one of --id or --path") {
		t.Errorf("expected 'exactly one of --id or --path', got: %v", err)
	}
}

func TestDownloadCmd_MissingOutput(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("download",
		"--account", "test@outlook.com",
		"--id", "ABC123",
	)
	if err == nil {
		t.Fatal("expected error when neither --output nor --output-dir is specified")
	}
	if !strings.Contains(err.Error(), "exactly one of --output or --output-dir") {
		t.Errorf("expected 'exactly one of --output or --output-dir', got: %v", err)
	}
}

func TestDownloadCmd_BothOutputAndOutputDir(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("download",
		"--account", "test@outlook.com",
		"--id", "ABC123",
		"--output", "/tmp/out.pdf",
		"--output-dir", "/tmp/downloads",
	)
	if err == nil {
		t.Fatal("expected error when both --output and --output-dir are specified")
	}
	if !strings.Contains(err.Error(), "exactly one of --output or --output-dir") {
		t.Errorf("expected 'exactly one of --output or --output-dir', got: %v", err)
	}
}

// =============================================================================
// upload — mutually exclusive flags
// =============================================================================

func TestUploadCmd_MissingIDAndPath(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("upload",
		"--account", "test@outlook.com",
		"--file", "/tmp/local.pdf",
	)
	if err == nil {
		t.Fatal("expected error when neither --id nor --path is specified")
	}
	if !strings.Contains(err.Error(), "exactly one of --id or --path") {
		t.Errorf("expected 'exactly one of --id or --path', got: %v", err)
	}
}

func TestUploadCmd_BothIDAndPath(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("upload",
		"--account", "test@outlook.com",
		"--id", "FOLDER123",
		"--path", "/Documents",
		"--file", "/tmp/local.pdf",
	)
	if err == nil {
		t.Fatal("expected error when both --id and --path are specified")
	}
	if !strings.Contains(err.Error(), "exactly one of --id or --path") {
		t.Errorf("expected 'exactly one of --id or --path', got: %v", err)
	}
}

func TestUploadCmd_MissingFile(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("upload",
		"--account", "test@outlook.com",
		"--id", "FOLDER123",
	)
	if err == nil {
		t.Fatal("expected error when --file is missing")
	}
}

// =============================================================================
// rm — missing force
// =============================================================================

func TestRmCmd_MissingForce(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("rm",
		"--account", "test@outlook.com",
		"--id", "ABC123",
	)
	if err == nil {
		t.Fatal("expected error when --force is missing")
	}
	if !strings.Contains(err.Error(), "force") {
		t.Errorf("expected 'force' in error, got: %v", err)
	}
}

func TestRmCmd_MissingIDAndPath(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("rm",
		"--account", "test@outlook.com",
		"--force",
	)
	if err == nil {
		t.Fatal("expected error when neither --id nor --path is specified")
	}
	if !strings.Contains(err.Error(), "exactly one of --id or --path") {
		t.Errorf("expected 'exactly one of --id or --path', got: %v", err)
	}
}

// =============================================================================
// rename — missing name
// =============================================================================

func TestRenameCmd_MissingName(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("rename",
		"--account", "test@outlook.com",
		"--id", "ABC123",
	)
	if err == nil {
		t.Fatal("expected error when --name is missing")
	}
}

// =============================================================================
// mkdir — missing name
// =============================================================================

func TestMkdirCmd_MissingName(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("mkdir",
		"--account", "test@outlook.com",
		"--id", "FOLDER123",
	)
	if err == nil {
		t.Fatal("expected error when --name is missing")
	}
}

// =============================================================================
// mv — mutually exclusive source and dest flags
// =============================================================================

func TestMvCmd_MissingSourceFlags(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("mv",
		"--account", "test@outlook.com",
		"--dest-id", "FOLDER456",
	)
	if err == nil {
		t.Fatal("expected error when neither --id nor --path for source is specified")
	}
	if !strings.Contains(err.Error(), "exactly one of --id or --path for the source") {
		t.Errorf("expected source flag error, got: %v", err)
	}
}

func TestMvCmd_MissingDestFlags(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("mv",
		"--account", "test@outlook.com",
		"--id", "ABC123",
	)
	if err == nil {
		t.Fatal("expected error when neither --dest-id nor --dest-path is specified")
	}
	if !strings.Contains(err.Error(), "exactly one of --dest-id or --dest-path") {
		t.Errorf("expected dest flag error, got: %v", err)
	}
}

func TestMvCmd_BothDestFlags(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("mv",
		"--account", "test@outlook.com",
		"--id", "ABC123",
		"--dest-id", "FOLDER456",
		"--dest-path", "/Archive",
	)
	if err == nil {
		t.Fatal("expected error when both --dest-id and --dest-path are specified")
	}
	if !strings.Contains(err.Error(), "exactly one of --dest-id or --dest-path") {
		t.Errorf("expected dest flag error, got: %v", err)
	}
}

// =============================================================================
// copy — missing name + dest
// =============================================================================

func TestCopyCmd_MissingNameAndDest(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("copy",
		"--account", "test@outlook.com",
		"--id", "ABC123",
	)
	if err == nil {
		t.Fatal("expected error when neither --name nor --dest-* is specified")
	}
	if !strings.Contains(err.Error(), "at least --name or --dest-id/--dest-path") {
		t.Errorf("expected 'at least --name or --dest-id', got: %v", err)
	}
}

func TestCopyCmd_BothDestFlags(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("copy",
		"--account", "test@outlook.com",
		"--id", "ABC123",
		"--dest-id", "FOLDER456",
		"--dest-path", "/Backup",
	)
	if err == nil {
		t.Fatal("expected error when both --dest-id and --dest-path are specified")
	}
	if !strings.Contains(err.Error(), "exactly one of --dest-id or --dest-path") {
		t.Errorf("expected 'exactly one of --dest-id or --dest-path', got: %v", err)
	}
}

// =============================================================================
// list — invalid format
// =============================================================================

func TestListCmd_InvalidFormat(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("list",
		"--account", "test@outlook.com",
		"--output", "xml",
	)
	if err == nil {
		t.Fatal("expected error for invalid output format")
	}
	if !strings.Contains(err.Error(), "unsupported format") {
		t.Errorf("expected 'unsupported format' in error, got: %v", err)
	}
}

// =============================================================================
// info — mutually exclusive id/path
// =============================================================================

func TestInfoCmd_MissingIDAndPath(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("info",
		"--account", "test@outlook.com",
	)
	if err == nil {
		t.Fatal("expected error when neither --id nor --path is specified")
	}
	if !strings.Contains(err.Error(), "exactly one of --id or --path") {
		t.Errorf("expected 'exactly one of --id or --path', got: %v", err)
	}
}

func TestInfoCmd_BothIDAndPath(t *testing.T) {
	setupManager(t, "test@outlook.com")

	err := execCmd("info",
		"--account", "test@outlook.com",
		"--id", "ABC123",
		"--path", "/Docs/photo.jpg",
	)
	if err == nil {
		t.Fatal("expected error when both --id and --path are specified")
	}
	if !strings.Contains(err.Error(), "exactly one of --id or --path") {
		t.Errorf("expected 'exactly one of --id or --path', got: %v", err)
	}
}

// =============================================================================
// account list — smoke test
// =============================================================================

func TestAccountListCmd_NoAccounts(t *testing.T) {
	tempDir := t.TempDir()
	mockKR := &mockKeyringForCLI{storage: make(map[string]string)}
	m, err := auth.NewManagerWithDeps(tempDir, mockKR)
	if err != nil {
		t.Fatalf("NewManagerWithDeps: %v", err)
	}

	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		ctx := contextWithManager(cmd.Context(), m)
		ctx = contextWithClient(ctx, getClient(cmd))
		cmd.SetContext(ctx)
	}
	t.Cleanup(func() {
		rootCmd.PersistentPreRun = originalPersistentPreRun
		rootCmd.SetArgs(nil)
	})

	// Should not panic even with zero accounts
	rootCmd.SetArgs([]string{"account", "list"})
	rootCmd.Execute()
}

func TestAccountListCmd_WithAccounts(t *testing.T) {
	setupManager(t, "test@outlook.com")

	// Should list the account without panicking
	rootCmd.SetArgs([]string{"account", "list"})
	rootCmd.Execute()
}
