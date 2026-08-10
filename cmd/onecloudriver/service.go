package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/frosado/onecloudriver/internal/auth"
	"github.com/spf13/cobra"
)

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage the systemd service for auto-mount on login",
	Long: `Installs, uninstalls and manages a user systemd service that
automatically mounts OneDrive on login.

The service is installed as a template (@.service), allowing
multiple accounts to be managed simultaneously:

  onecloudriver service install --account user@outlook.com --enable

Each instance mounts at ~/OneDrive/<account> (configurable with --mountpoint).`,
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the user systemd service",
	Long: `Creates the systemd service file at ~/.config/systemd/user/onecloudriver@.service
and reloads the systemd daemon.

If --mountpoint is not specified, the defaultMountpoint from the account
JSON is used (saved from the last successful mount). If that also doesn't
exist, ~/OneDrive/%%i is used as fallback.

If --account is not specified and only ONE account is configured, it is
used automatically. With multiple accounts, --account is required.

With --enable, activates and starts the service automatically.

Examples:
  onecloudriver service install
  onecloudriver service install --mountpoint ~/OneDrive/%%i
  onecloudriver service install --mountpoint ~/OneDrive/%%i -a user@outlook.com --enable`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mountpoint, _ := cmd.Flags().GetString("mountpoint")
		accountName, _ := cmd.Flags().GetString("account")
		enable, _ := cmd.Flags().GetBool("enable")
		allAccounts, _ := cmd.Flags().GetBool("all")

		// --all mode: install for ALL accounts
		if allAccounts {
			accounts := manager.ListAccounts()
			if len(accounts) == 0 {
				return fmt.Errorf("no accounts configured. Use 'onecloudriver account add' to add one")
			}
			for _, account := range accounts {
				fmt.Printf("\n─── %s ───\n", account)
				acc, err := manager.GetAccount(account)
				if err != nil {
					return err
				}
				mp := resolveInstallMountpoint(mountpoint, acc)
				if err := installService(mp, account); err != nil {
					return err
				}
				if enable {
					enableUnit(account)
				}
			}
			return nil
		}
		acc, err := resolveAccount(cmd, manager)
		if err != nil {
			return err
		}
		mp := resolveInstallMountpoint(mountpoint, acc)
		if err := installService(mp, accountName); err != nil {
			return err
		}

		// Enable and start automatically if requested
		if enable {
			enableUnit(accountName)
		}

		return nil
	},
}

var serviceUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall the user systemd service",
	Long: `Stops all active instances, disables the service
and removes the service file from ~/.config/systemd/user/.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		allAccounts, _ := cmd.Flags().GetBool("all")

		// --all mode: uninstall for all accounts explicitly
		if allAccounts {
			accounts := manager.ListAccounts()
			if len(accounts) == 0 {
				fmt.Println("ℹ️  No accounts configured.")
				return nil
			}
			for _, account := range accounts {
				fmt.Printf("\n─── %s ───\n", account)
				unmountMountpoint(account)
				_ = runSystemctl("stop", account)
				_ = runSystemctl("disable", account)
			}
			// Remove the common service file
			path, _ := serviceFilePath()
			if _, err := os.Stat(path); err == nil {
				if err := os.Remove(path); err != nil {
					return fmt.Errorf("error removing service file: %w", err)
				}
				fmt.Printf("\n✅ Service file removed: %s\n", path)
			}
			//#nosec G204 -- systemctl command with fixed arguments
			_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
			fmt.Println("✅ Service uninstalled for all accounts.")
			return nil
		}

		return uninstallService()
	},
}

var serviceStatusCmd = &cobra.Command{
	Use:   "status [account]",
	Short: "Show service status (all accounts or a specific one)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return serviceStatus(args)
	},
}

var serviceStartCmd = &cobra.Command{
	Use:   "start <account>",
	Short: "Start the service for an account",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSystemctl("start", args[0])
	},
}

var serviceStopCmd = &cobra.Command{
	Use:   "stop [account]",
	Short: "Stop the service and unmount the filesystem",
	Long: `Stops the systemd service and unmounts the FUSE filesystem
for the specified account.

With --all, stops and unmounts all configured accounts.

Unmounting is done both via systemd (ExecStop=fusermount3 -uz) and
with a direct fusermount3 call as fallback, ensuring the mountpoint
is freed even if systemd does not complete ExecStop.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		allAccounts, _ := cmd.Flags().GetBool("all")

		if allAccounts {
			accounts := manager.ListAccounts()
			if len(accounts) == 0 {
				return fmt.Errorf("no accounts configured")
			}
			for _, account := range accounts {
				fmt.Printf("\n─── %s ───\n", account)
				unmountMountpoint(account)
				_ = runSystemctl("stop", account)
			}
			fmt.Println("\n✅ All accounts stopped.")
			return nil
		}

		if len(args) == 0 {
			return fmt.Errorf("specify an account or use --all to stop all")
		}

		// 1. Unmount explicitly before stopping systemd
		unmountMountpoint(args[0])

		// 2. Stop the service (ExecStop also attempts to unmount)
		return runSystemctl("stop", args[0])
	},
}

func registerServiceCmd(root *cobra.Command) {
	// Flags for service install
	serviceInstallCmd.Flags().String("mountpoint", "",
		"Base mount path. Use %i for the placeholder. If omitted, uses the defaultMountpoint from account JSON or ~/OneDrive/%i")
	serviceInstallCmd.Flags().StringP("account", "a", "",
		"Microsoft account to use. If omitted, uses the only configured account.")
	serviceInstallCmd.Flags().Bool("enable", false,
		"Enable and start the service immediately after installing it")
	serviceInstallCmd.Flags().Bool("all", false,
		"Install the service for ALL configured accounts")

	// Flags for service stop
	serviceStopCmd.Flags().Bool("all", false,
		"Stop the service for ALL configured accounts")

	// Flags for service uninstall
	serviceUninstallCmd.Flags().Bool("all", false,
		"Uninstall the service for ALL configured accounts")

	serviceCmd.AddCommand(
		serviceInstallCmd,
		serviceUninstallCmd,
		serviceStatusCmd,
		serviceStartCmd,
		serviceStopCmd,
	)

	root.AddCommand(serviceCmd)
}

// serviceFilePath returns the path to the user's systemd service file.
func serviceFilePath() (string, error) {
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

// serviceUnit generates the content of the systemd service file.
func serviceUnit(mountpoint string) string {
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

[Install]
WantedBy=default.target
`, binary, mountpoint, mountpoint, mountpoint)
}

// installService creates the service file and reloads systemd.
func installService(mountpoint, account string) error {
	path, err := serviceFilePath()
	if err != nil {
		return err
	}

	// Create the directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("error creating systemd directory: %w", err)
	}

	// Write the service file
	content := serviceUnit(mountpoint)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return fmt.Errorf("error writing service file: %w", err)
	}

	fmt.Printf("✅ Service installed at %s\n", path)
	fmt.Printf("   Account:    %s\n", account)
	fmt.Printf("   Mountpoint: %s\n", mountpoint)

	// Reload systemd daemon
	//#nosec G204 -- systemctl command with fixed arguments, no user input
	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("error reloading systemd daemon: %w", err)
	}
	fmt.Println("✅ systemd daemon reloaded")

	return nil
}

// uninstallService stops all instances, disables and removes the file.
func uninstallService() error {
	path, err := serviceFilePath()
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
		fmt.Printf("✅ Service file removed: %s\n", path)
	} else {
		fmt.Println("ℹ️  The service was not installed.")
		return nil
	}

	// Reload systemd
	//#nosec G204 -- systemctl command with fixed arguments
	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("error reloading systemd daemon: %w", err)
	}

	fmt.Println("✅ Service uninstalled successfully.")
	return nil
}

// serviceStatus shows the status of the service.
func serviceStatus(args []string) error {
	if len(args) > 0 {
		// Status of a specific account
		return runSystemctl("status", args[0])
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
	path, _ := serviceFilePath()
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("\n✅ Service installed at: %s\n", path)
	} else {
		fmt.Println("\n⚠️  Service not installed. Use 'onecloudriver service install' to install it.")
	}

	return nil
}

// enableUnit enables and starts a systemd unit for an account.
func enableUnit(account string) {
	fmt.Printf("🚀 Enabling and starting onecloudriver@%s...\n", account)
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

// resolveInstallMountpoint determines the mountpoint for service install:
//  1. If the user passed --mountpoint explicitly → use it
//  2. If the account has defaultMountpoint in the JSON → use it
//  3. Fallback: ~/OneDrive/%%i
func resolveInstallMountpoint(explicitFlag string, acc *auth.Account) string {
	if explicitFlag != "" {
		return explicitFlag
	}
	if acc.Mount.DefaultMountpoint != "" {
		fmt.Printf("Using saved mountpoint: %s\n", acc.Mount.DefaultMountpoint)
		return acc.Mount.DefaultMountpoint
	}
	fallback := "~/OneDrive/%i"
	fmt.Printf("Using default mountpoint: %s\n", fallback)
	return fallback
}

// defaultMountpointFor returns the default mountpoint for an account.
func defaultMountpointFor(account string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "OneDrive", account)
}

// unmountMountpoint unmounts the FUSE filesystem for an account.
// Uses fusermount3 -uz (lazy-unmount) to guarantee the mountpoint
// is freed even if there are processes accessing it.
func unmountMountpoint(account string) {
	mp := defaultMountpointFor(account)
	if mp == "" {
		return
	}

	// Check if the mountpoint is actually mounted
	//#nosec G204 -- mount command without arguments, read-only mount table
	out, _ := exec.Command("mount").Output()
	if !strings.Contains(string(out), mp) {
		return // not mounted, nothing to do
	}

	fmt.Printf("🔌 Unmounting %s...\n", mp)
	//#nosec G204 -- fusermount3 with path derived from account, not arbitrary input
	if err := exec.Command("fusermount3", "-uz", mp).Run(); err != nil {
		//#nosec G204 -- fallback to fusermount without 3
		_ = exec.Command("fusermount", "-uz", mp).Run()
	}
	fmt.Printf("✅ %s unmounted\n", mp)
}

// runSystemctl runs a systemctl --user command for an account.
func runSystemctl(action, account string) error {
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
