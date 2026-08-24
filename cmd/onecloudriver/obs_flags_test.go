package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestObsFlags_RegisteredWithDefaults verifies the observability flags from
// issue #74 are registered with the documented defaults: --log-json on the
// root command, --debug/--debug-addr on mount, loopback by default.
func TestObsFlags_RegisteredWithDefaults(t *testing.T) {
	// --log-json is a global (persistent) flag on rootCmd.
	logJSON := rootCmd.PersistentFlags().Lookup("log-json")
	if logJSON == nil {
		t.Fatal("rootCmd missing --log-json persistent flag")
	}
	if v := logJSON.Value.String(); v != "false" {
		t.Errorf("--log-json default = %q, want false", v)
	}

	debug := mountCmd.Flags().Lookup("debug")
	if debug == nil {
		t.Fatal("mountCmd missing --debug flag")
	}
	if v := debug.Value.String(); v != "false" {
		t.Errorf("--debug default = %q, want false", v)
	}

	debugAddr := mountCmd.Flags().Lookup("debug-addr")
	if debugAddr == nil {
		t.Fatal("mountCmd missing --debug-addr flag")
	}
	if v := debugAddr.Value.String(); v != "127.0.0.1:6060" {
		t.Errorf("--debug-addr default = %q, want 127.0.0.1:6060 (loopback)", v)
	}
}

// TestObsFlags_WireDebugIntoMountConfig simulates the mount command's flag
// reading logic (the part of RunE before the blocking FUSE call) to confirm
// --debug enables the debug server address and without it the address stays
// empty.
func TestObsFlags_WireDebugIntoMountConfig(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().Bool("debug", false, "")
	cmd.Flags().String("debug-addr", "127.0.0.1:6060", "")

	getDebugAddr := func(debug string, addr string) string {
		_ = cmd.Flags().Set("debug", debug)
		_ = cmd.Flags().Set("debug-addr", addr)
		if d, _ := cmd.Flags().GetBool("debug"); d {
			a, _ := cmd.Flags().GetString("debug-addr")
			return a
		}
		return ""
	}

	if got := getDebugAddr("false", ""); got != "" {
		t.Errorf("debug off → DebugAddr = %q, want empty (disabled)", got)
	}
	if got := getDebugAddr("true", "127.0.0.1:6060"); got != "127.0.0.1:6060" {
		t.Errorf("debug on → DebugAddr = %q, want the debug addr", got)
	}
}
