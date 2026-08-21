// Package service contains the systemd integration logic of the onecloudriver
// CLI: service file generation, systemctl calls and FUSE unmount helpers.
//
// Extracted from cmd/onecloudriver/service.go (issue #6) so the logic is
// testable and independent of the cobra layer.
package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/frosado/onecloudriver/internal/printer"
)

// ServiceFilePath returns the path to the user's systemd service file.
func ServiceFilePath() (string, error) {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("could not determine HOME: %w", err)
		}
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "systemd", "user", "onecloudriver@.service"), nil
}

// normalizeMountpointForUnit replaces a leading '~' with the systemd home
// specifier %h. systemd does not expand '~' in ExecStart (only specifiers
// such as %h, %i, ...), so a literal ~ would be passed verbatim to mount and
// fail with "mount point '~/...' does not exist". %h is safe here: this is a
// user unit by design (installed under ~/.config/systemd/user/), where %h
// expands to the user's home directory.
//
// Only the forms '~' and '~/...' are expanded; other tilde forms (e.g.
// '~otheruser/...') are intentionally left unchanged. The CLI layer expands
// '~' to the absolute home path (cmd/onecloudriver.expandHomePrefix) before
// calling ServiceUnit; this function is the defensive fallback for any
// '~'-containing mountpoint that reaches the unit directly.
func normalizeMountpointForUnit(mountpoint string) string {
	if mountpoint == "~" {
		return "%h"
	}
	if strings.HasPrefix(mountpoint, "~/") {
		return "%h/" + strings.TrimPrefix(mountpoint, "~/")
	}
	return mountpoint
}

// resolveBinary returns an absolute path to an existing, executable
// onecloudriver binary, or an error. It is the fail-fast counterpart of the
// ExecStart path written into the generated unit: a broken path must be
// reported before anything is written to disk, not at service runtime.
//
// argv0 is os.Args[0]. A Go test binary (go test runs <pkg>.test from a
// temporary go-build directory that is removed afterwards) is never a valid
// runtime binary, so it is rejected and the canonical name is used instead.
func resolveBinary(argv0 string) (string, error) {
	// Reject Go test binaries: they live in a temporary go-build directory
	// that disappears after the test run (and on reboot), so writing their
	// path into ExecStart would make systemd fail with 203/EXEC.
	if strings.HasSuffix(filepath.Base(argv0), ".test") {
		argv0 = "onecloudriver"
	}

	var candidate string
	if strings.ContainsRune(argv0, os.PathSeparator) {
		// Explicit path invocation (absolute or relative): resolve to an
		// absolute path and validate below.
		abs, err := filepath.Abs(argv0)
		if err != nil {
			return "", fmt.Errorf("could not resolve binary path %q: %w", argv0, err)
		}
		candidate = abs
	} else {
		// Bare name: look it up on PATH.
		resolved, err := exec.LookPath(argv0)
		if err != nil {
			return "", fmt.Errorf("onecloudriver binary not found in PATH (tried %q): %w", argv0, err)
		}
		candidate = resolved
	}

	// Defensive final validation: the resolved path must be a regular,
	// executable file (LookPath already checks the exec bit, but a directory
	// or a non-regular file must not be accepted).
	if !isExecutableFile(candidate) {
		return "", fmt.Errorf("binary %q is not an executable file", candidate)
	}
	return candidate, nil
}

// isExecutableFile reports whether path is a regular file with at least one
// executable bit set.
func isExecutableFile(path string) bool {
	//#nosec G703 -- path is resolved/validated by resolveBinary (filepath.Abs or exec.LookPath), not raw user input
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
}

// ServiceUnit generates the content of the systemd service file for the given
// mountpoint and the already-resolved binary path.
func ServiceUnit(mountpoint, binary string) string {
	// systemd does not expand '~' in ExecStart: normalize it to the %h
	// specifier so the generated unit always resolves to an absolute path.
	mountpoint = normalizeMountpointForUnit(mountpoint)

	return fmt.Sprintf(`[Unit]
Description=OneCloudRiver - OneDrive filesystem for %%i
Documentation=man:onecloudriver(1)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s mount %s -a %%i
ExecStop=/bin/fusermount3 -uz %s
ExecReload=/bin/fusermount3 -uz %s
Restart=on-failure
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
`, binary, mountpoint, mountpoint, mountpoint)
}

// concreteMountpoint expands the %h (home) and %i (account) placeholders of a
// template mountpoint into the concrete directory used by an instance.
func concreteMountpoint(mountpoint, account string) string {
	if home, err := os.UserHomeDir(); err == nil {
		mountpoint = strings.ReplaceAll(mountpoint, "%h", home)
	}
	return strings.ReplaceAll(mountpoint, "%i", account)
}

// ensureMountpointDir creates the concrete mountpoint directory for an account
// if it does not exist yet, and informs the user when it does. Without this,
// fs.Mount would reject a missing directory and the service would fail to
// start on a fresh machine (mount point '...' does not exist).
func ensureMountpointDir(mountpoint, account string) error {
	dir := concreteMountpoint(mountpoint, account)
	if _, err := os.Stat(dir); err == nil {
		return nil // already exists
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("error checking mountpoint %q: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("error creating mountpoint %q: %w", dir, err)
	}
	fmt.Printf("%s Mountpoint directory created: %s\n", printer.Folder, dir)
	return nil
}

// InstallService creates the service file and reloads systemd.
func InstallService(mountpoint, account string) error {
	path, err := ServiceFilePath()
	if err != nil {
		return err
	}

	// Resolve the binary BEFORE writing anything: a broken ExecStart must be
	// reported now, not when systemd tries to exec it.
	binary, err := resolveBinary(os.Args[0])
	if err != nil {
		return err
	}

	// Create the directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("error creating systemd directory: %w", err)
	}

	// Write the service file
	content := ServiceUnit(mountpoint, binary)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return fmt.Errorf("error writing service file: %w", err)
	}

	// Create the mountpoint directory so the service can mount on first start
	if err := ensureMountpointDir(mountpoint, account); err != nil {
		return err
	}

	fmt.Printf("%s Service installed at %s\n", printer.Success, path)
	fmt.Printf("   Account:    %s\n", account)
	fmt.Printf("   Mountpoint: %s\n", mountpoint)

	// Reload systemd daemon
	//#nosec G204 -- systemctl command with fixed arguments, no user input
	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("error reloading systemd daemon: %w", err)
	}
	fmt.Println(printer.Success, "systemd daemon reloaded")

	return nil
}

// UninstallService stops the running instances, removes the service file and
// reloads systemd.
//
// With accounts, each listed account is unmounted, stopped and disabled (the
// former --all CLI behaviour). With no accounts, the running instances are
// discovered via `systemctl list-units onecloudriver@*` (the single-account
// behaviour). Both paths share the same tail: remove the service file and
// reload the daemon.
// Failures while stopping, disabling or unmounting an instance do not abort
// the uninstall (the unit file must still go away), but they are reported as
// warnings instead of being discarded: an instance that could not be stopped
// keeps a mountpoint busy after the command says it succeeded.
func UninstallService(accounts ...string) error {
	path, err := ServiceFilePath()
	if err != nil {
		return err
	}

	allAccounts := len(accounts) > 0
	if allAccounts {
		for _, account := range accounts {
			if err := UnmountMountpoint(account); err != nil {
				warnf("could not unmount the mountpoint of %s: %v", account, err)
			}
			if err := Systemctl("stop", account); err != nil {
				warnf("%v", err)
			}
			if err := Systemctl("disable", account); err != nil {
				warnf("%v", err)
			}
		}
	} else {
		// Stop all active instances
		//#nosec G204 -- systemctl command with fixed arguments, no user input
		output, err := exec.Command("systemctl", "--user", "list-units", "--plain",
			"--no-legend", "onecloudriver@*").Output()
		if err != nil {
			// Without the unit list the running instances cannot be stopped,
			// so the user must know the uninstall was only partial.
			warnf("could not list the running instances (they were not stopped): %v", err)
		}
		for _, line := range strings.Split(string(output), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// The first column is the unit name
			unit := strings.Fields(line)[0]
			fmt.Printf("Stopping %s...\n", unit)
			//#nosec G204 -- unit comes from systemctl list-units, not user input
			if err := exec.Command("systemctl", "--user", "stop", unit).Run(); err != nil { //#nosec G204
				warnf("could not stop %s: %v", unit, err)
			}
			//#nosec G204 -- unit comes from systemctl list-units, not user input
			if err := exec.Command("systemctl", "--user", "disable", unit).Run(); err != nil { //#nosec G204
				warnf("could not disable %s: %v", unit, err)
			}
		}
	}

	// Remove the service file
	if _, err := os.Stat(path); err == nil {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("error removing service file: %w", err)
		}
		fmt.Printf("%s Service file removed: %s\n", printer.Success, path)
	} else {
		fmt.Println(printer.Info, "The service was not installed.")
		return nil
	}

	// Reload systemd
	//#nosec G204 -- systemctl command with fixed arguments
	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("error reloading systemd daemon: %w", err)
	}

	if allAccounts {
		fmt.Println(printer.Success, "Service uninstalled for all accounts.")
	} else {
		fmt.Println(printer.Success, "Service uninstalled successfully.")
	}
	return nil
}

// warnf prints a non-fatal diagnostic on stderr. Used by the text-mode
// helpers for failures that must not abort the operation but must not be
// hidden either (stdout is reserved for the command's own output).
func warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s %s\n", printer.Warning, fmt.Sprintf(format, args...))
}

// EnableUnit enables and starts a systemd unit for an account. A failing
// `systemctl enable --now` is returned to the caller: the CLI must not exit 0
// after printing "enabling and starting..." for a unit that never started.
func EnableUnit(account string) error {
	fmt.Printf("%s Enabling and starting onecloudriver@%s...\n", printer.Rocket, account)
	unit := unitName(account)
	//#nosec G204 -- systemctl with controlled parameters
	cmd := exec.Command("systemctl", "--user", "enable", "--now", unit)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()

	fmt.Println()
	fmt.Println("   To view logs:")
	fmt.Printf("     journalctl --user -u %s -f\n", unit)

	if runErr != nil {
		return fmt.Errorf("error enabling systemd unit %s: %w", unit, runErr)
	}
	return nil
}

// Systemctl runs a systemctl --user command for an account.
func Systemctl(action, account string) error {
	unit := unitName(account)

	//#nosec G204 -- systemctl with controlled parameters (action and account are fixed)
	cmd := exec.Command("systemctl", "--user", action, unit)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error running systemctl %s %s: %w", action, unit, err)
	}
	return nil
}

// DefaultMountpointFor returns the default mountpoint for an account.
func DefaultMountpointFor(account string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "OneDrive", account)
}

// UnmountMountpoint unmounts the FUSE filesystem for an account.
// Uses fusermount3 -uz (lazy-unmount) to guarantee the mountpoint
// is freed even if there are processes accessing it.
//
// It returns an error when the mount table cannot be read or when both
// fusermount variants fail, and prints the confirmation line only when the
// mountpoint was really released: a mountpoint that is still mounted must not
// be reported as unmounted.
func UnmountMountpoint(account string) error {
	mp := DefaultMountpointFor(account)
	if mp == "" {
		return nil
	}

	mounted, err := isMounted(mp)
	if err != nil {
		return err
	}
	if !mounted {
		return nil // not mounted, nothing to do
	}

	fmt.Printf("%s Unmounting %s...\n", printer.Unplug, mp)
	if err := unmount(mp); err != nil {
		return err
	}
	fmt.Printf("%s %s unmounted\n", printer.Success, mp)
	return nil
}

// isMounted reports whether mp appears in the mount table. A failure to read
// the table is returned rather than treated as "not mounted", which would
// skip the unmount silently.
func isMounted(mp string) (bool, error) {
	//#nosec G204 -- mount command without arguments, read-only mount table
	out, err := exec.Command("mount").Output()
	if err != nil {
		return false, fmt.Errorf("could not read the mount table: %w", err)
	}
	return strings.Contains(string(out), mp), nil
}

// unmount lazy-unmounts mp with fusermount3, falling back to fusermount on
// systems that only ship the FUSE 2 helper. Both failures are reported: a
// mountpoint that stays mounted is not a successful unmount.
func unmount(mp string) error {
	//#nosec G204 -- fusermount3 with path derived from account, not arbitrary input
	err3 := exec.Command("fusermount3", "-uz", mp).Run()
	if err3 == nil {
		return nil
	}
	//#nosec G204 -- fallback to fusermount without 3
	if err := exec.Command("fusermount", "-uz", mp).Run(); err != nil {
		return fmt.Errorf("could not unmount %s: %w",
			mp, errors.Join(err3, err))
	}
	return nil
}
