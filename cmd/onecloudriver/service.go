package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/frosado/onecloudriver/internal/auth"
	"github.com/frosado/onecloudriver/internal/printer"
	"github.com/frosado/onecloudriver/internal/service"
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
				if err := service.InstallService(mp, account); err != nil {
					return err
				}
				if enable {
					service.EnableUnit(account)
				}
			}
			return nil
		}
		acc, err := resolveAccount(cmd, manager)
		if err != nil {
			return err
		}
		mp := resolveInstallMountpoint(mountpoint, acc)
		if err := service.InstallService(mp, accountName); err != nil {
			return err
		}

		// Enable and start automatically if requested
		if enable {
			service.EnableUnit(accountName)
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
				fmt.Println(printer.Info, "No accounts configured.")
				return nil
			}
			for _, account := range accounts {
				fmt.Printf("\n─── %s ───\n", account)
				service.UnmountMountpoint(account)
				_ = service.Systemctl("stop", account)
				_ = service.Systemctl("disable", account)
			}
			// Remove the common service file
			path, _ := service.ServiceFilePath()
			if _, err := os.Stat(path); err == nil {
				if err := os.Remove(path); err != nil {
					return fmt.Errorf("error removing service file: %w", err)
				}
				fmt.Printf("\n%s Service file removed: %s\n", printer.Success, path)
			}
			//#nosec G204 -- systemctl command with fixed arguments
			_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
			fmt.Println(printer.Success, "Service uninstalled for all accounts.")
			return nil
		}

		return service.UninstallService()
	},
}

var serviceStatusCmd = &cobra.Command{
	Use:   "status [account]",
	Short: "Show service status (all accounts or a specific one)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return service.Status(args)
	},
}

var serviceStartCmd = &cobra.Command{
	Use:   "start <account>",
	Short: "Start the service for an account",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return service.Systemctl("start", args[0])
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
				service.UnmountMountpoint(account)
				_ = service.Systemctl("stop", account)
			}
			fmt.Println("\n"+printer.Success, "All accounts stopped.")
			return nil
		}

		if len(args) == 0 {
			return fmt.Errorf("specify an account or use --all to stop all")
		}

		// 1. Unmount explicitly before stopping systemd
		service.UnmountMountpoint(args[0])

		// 2. Stop the service (ExecStop also attempts to unmount)
		return service.Systemctl("stop", args[0])
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
