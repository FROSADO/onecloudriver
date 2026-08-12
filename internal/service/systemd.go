// Package service contains the systemd integration logic of the onecloudriver
// CLI: service file generation, systemctl calls and FUSE unmount helpers.
//
// Extracted from cmd/onecloudriver/service.go (issue #6) so the logic is
// testable and independent of the cobra layer.
package service

import (
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

// ServiceUnit generates the content of the systemd service file.
func ServiceUnit(mountpoint string) string {
	// systemd does not expand '~' in ExecStart: normalize it to the %h
	// specifier so the generated unit always resolves to an absolute path.
	mountpoint = normalizeMountpointForUnit(mountpoint)

	// Resolve the absolute path to the current binary. Systemd requires absolute
	// paths in ExecStart. os.Args[0] may be relative if the user ran
	// "./onecloudriver" or in PATH if they used "onecloudriver".
	binary := os.Args[0]
	if abs, err := filepath.Abs(binary); err == nil {
		binary = abs
	} else if resolved, err := exec.LookPath(binary); err == nil {
		binary = resolved
	}

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

	// Create the directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("error creating systemd directory: %w", err)
	}

	// Write the service file
	content := ServiceUnit(mountpoint)
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

// UninstallService stops all instances, disables and removes the file.
func UninstallService() error {
	path, err := ServiceFilePath()
	if err != nil {
		return err
	}

	// Stop all active instances
	//#nosec G204 -- systemctl command with fixed arguments, no user input
	output, _ := exec.Command("systemctl", "--user", "list-units", "--plain",
		"--no-legend", "onecloudriver@*").Output()
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// The first column is the unit name
		unit := strings.Fields(line)[0]
		fmt.Printf("Stopping %s...\n", unit)
		//#nosec G204 -- unit comes from systemctl list-units, not user input
		_ = exec.Command("systemctl", "--user", "stop", unit).Run()    //#nosec G204
		_ = exec.Command("systemctl", "--user", "disable", unit).Run() //#nosec G204
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

	fmt.Println(printer.Success, "Service uninstalled successfully.")
	return nil
}

// Status shows the status of the service.
func Status(args []string) error {
	if len(args) > 0 {
		// Status of a specific account
		return Systemctl("status", args[0])
	}

	// List all instances
	fmt.Println("Active onecloudriver instances:")
	cmd := exec.Command("systemctl", "--user", "list-units", "--plain",
		"--no-legend", "onecloudriver@*")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// systemctl returns code 1 if there are no units, not a real error
		fmt.Println("  (none)")
	}

	// Check if the service is installed
	path, _ := ServiceFilePath()
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("\n%s Service installed at: %s\n", printer.Success, path)
	} else {
		fmt.Println("\n"+printer.Warning, "Service not installed. Use 'onecloudriver service install' to install it.")
	}

	return nil
}

// EnableUnit enables and starts a systemd unit for an account.
func EnableUnit(account string) {
	fmt.Printf("%s Enabling and starting onecloudriver@%s...\n", printer.Rocket, account)
	unit := fmt.Sprintf("onecloudriver@%s.service", account)
	//#nosec G204 -- systemctl with controlled parameters
	cmd := exec.Command("systemctl", "--user", "enable", "--now", unit)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
	fmt.Println()
	fmt.Println("   To view logs:")
	fmt.Printf("     journalctl --user -u %s -f\n", unit)
}

// Systemctl runs a systemctl --user command for an account.
func Systemctl(action, account string) error {
	unit := fmt.Sprintf("onecloudriver@%s.service", account)

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
func UnmountMountpoint(account string) {
	mp := DefaultMountpointFor(account)
	if mp == "" {
		return
	}

	// Check if the mountpoint is actually mounted
	//#nosec G204 -- mount command without arguments, read-only mount table
	out, _ := exec.Command("mount").Output()
	if !strings.Contains(string(out), mp) {
		return // not mounted, nothing to do
	}

	fmt.Printf("%s Unmounting %s...\n", printer.Unplug, mp)
	//#nosec G204 -- fusermount3 with path derived from account, not arbitrary input
	if err := exec.Command("fusermount3", "-uz", mp).Run(); err != nil {
		//#nosec G204 -- fallback to fusermount without 3
		_ = exec.Command("fusermount", "-uz", mp).Run()
	}
	fmt.Printf("%s %s unmounted\n", printer.Success, mp)
}
