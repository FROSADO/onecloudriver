package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
JSON is reused only when it contains the %i instance placeholder. A concrete
saved mountpoint (no %i) is ignored, and ~/OneDrive/%i is used as fallback.

If --account is not specified and only ONE account is configured, it is
used automatically. With multiple accounts, --account is required.

With --enable, activates and starts the service automatically.

Examples:
  onecloudriver service install
  onecloudriver service install --mountpoint ~/OneDrive/%%i
  onecloudriver service install --mountpoint ~/OneDrive/%%i -a user@outlook.com --enable`,
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := resolveServiceOutput(cmd)
		if err != nil {
			return err
		}
		manager, err := getManager(cmd)
		if err != nil {
			return err
		}
		mountpoint, _ := cmd.Flags().GetString("mountpoint")
		enable, _ := cmd.Flags().GetBool("enable")
		allAccounts, _ := cmd.Flags().GetBool("all")

		if allAccounts {
			if format == "text" {
				return installAllText(cmd, manager, mountpoint, enable)
			}
			return installAllStructured(cmd, manager, mountpoint, enable, format)
		}

		acc, err := resolveServiceAccount(cmd, manager, format != "text")
		if err != nil {
			return err
		}
		diag := cmd.OutOrStdout()
		if format != "text" {
			diag = cmd.ErrOrStderr()
		}
		mp, warn := resolveInstallMountpoint(mountpoint, acc, diag)

		if format == "text" {
			if err := service.InstallService(mp, acc.Name); err != nil {
				return err
			}
			// Enable and start automatically if requested
			if enable {
				service.EnableUnit(acc.Name)
			}
			return nil
		}

		result, err := service.InstallServiceResult(mp, acc.Name)
		result.Warning = warn
		if err != nil {
			_ = writeServiceStructured(cmd, format, result)
			return err
		}
		if enable {
			if err := service.EnableUnitQuiet(acc.Name); err != nil {
				result.OK = false
				result.Error = err.Error()
				_ = writeServiceStructured(cmd, format, result)
				return err
			}
		}
		return writeServiceStructured(cmd, format, result)
	},
}

var serviceUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall the user systemd service",
	Long: `Stops all active instances, disables the service
and removes the service file from ~/.config/systemd/user/.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := resolveServiceOutput(cmd)
		if err != nil {
			return err
		}
		manager, err := getManager(cmd)
		if err != nil {
			return err
		}
		allAccounts, _ := cmd.Flags().GetBool("all")

		if allAccounts {
			accounts := manager.ListAccounts()
			if len(accounts) == 0 {
				if format == "text" {
					fmt.Println(printer.Info, "No accounts configured.")
					return nil
				}
				return fmt.Errorf("no accounts configured")
			}
			if format == "text" {
				for _, account := range accounts {
					fmt.Printf("\n─── %s ───\n", account)
				}
				return service.UninstallService(accounts...)
			}
			sort.Strings(accounts)
			result, err := service.UninstallServiceResult(accounts...)
			if err != nil {
				_ = writeServiceStructured(cmd, format, result)
				return err
			}
			return writeServiceStructured(cmd, format, result)
		}

		if format == "text" {
			return service.UninstallService()
		}
		result, err := service.UninstallServiceResult()
		if err != nil {
			_ = writeServiceStructured(cmd, format, result)
			return err
		}
		return writeServiceStructured(cmd, format, result)
	},
}

var serviceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed onecloudriver service instances",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := resolveServiceOutput(cmd)
		if err != nil {
			return err
		}
		instances, err := service.ListInstances()
		if err != nil {
			return err
		}
		if format == "text" {
			formatServiceInstances(cmd.OutOrStdout(), instances)
			return nil
		}
		return writeServiceStructured(cmd, format, instances)
	},
}

var serviceStatusCmd = &cobra.Command{
	Use:   "status [account]",
	Short: "Show service status (all accounts or a specific one)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := resolveServiceOutput(cmd)
		if err != nil {
			return err
		}
		if format == "text" {
			return service.Status(args)
		}
		if len(args) > 0 {
			status, journalErr, err := service.QueryUnitStatus(args[0])
			if err != nil {
				return err
			}
			if journalErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s Could not read the service journal: %v\n", printer.Warning, journalErr)
			}
			return writeServiceStructured(cmd, format, status)
		}
		instances, err := service.ListInstances()
		if err != nil {
			return err
		}
		return writeServiceStructured(cmd, format, instances)
	},
}

var serviceStartCmd = &cobra.Command{
	Use:   "start <account>",
	Short: "Start the service for an account",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := resolveServiceOutput(cmd)
		if err != nil {
			return err
		}
		if format == "text" {
			if err := service.Systemctl("start", args[0]); err != nil {
				unit := fmt.Sprintf("onecloudriver@%s.service", args[0])
				fmt.Fprintf(cmd.ErrOrStderr(), "\n%s The service failed to start. To inspect the failure:\n", printer.Warning)
				fmt.Fprintf(cmd.ErrOrStderr(), "     systemctl --user status %s\n", unit)
				fmt.Fprintf(cmd.ErrOrStderr(), "     journalctl --user -u %s -e\n", unit)
				return err
			}
			return nil
		}
		result, err := service.StartServiceResult(args[0])
		if err != nil {
			_ = writeServiceStructured(cmd, format, result)
			return err
		}
		return writeServiceStructured(cmd, format, result)
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
		format, err := resolveServiceOutput(cmd)
		if err != nil {
			return err
		}
		manager, err := getManager(cmd)
		if err != nil {
			return err
		}
		allAccounts, _ := cmd.Flags().GetBool("all")

		if allAccounts {
			accounts := manager.ListAccounts()
			if len(accounts) == 0 {
				return fmt.Errorf("no accounts configured")
			}
			if format == "text" {
				for _, account := range accounts {
					fmt.Printf("\n─── %s ───\n", account)
					service.UnmountMountpoint(account)
					_ = service.Systemctl("stop", account)
				}
				fmt.Println("\n"+printer.Success, "All accounts stopped.")
				return nil
			}
			return stopAllStructured(cmd, accounts, format)
		}

		if len(args) == 0 {
			return fmt.Errorf("specify an account or use --all to stop all")
		}

		if format == "text" {
			// 1. Unmount explicitly before stopping systemd
			service.UnmountMountpoint(args[0])

			// 2. Stop the service (ExecStop also attempts to unmount)
			return service.Systemctl("stop", args[0])
		}

		result, err := service.StopServiceResult(args[0])
		if err != nil {
			_ = writeServiceStructured(cmd, format, result)
			return err
		}
		return writeServiceStructured(cmd, format, result)
	},
}

// resolveServiceOutput validates and returns the inherited --output format for
// the service command. It runs before any systemd or account side effect.
func resolveServiceOutput(cmd *cobra.Command) (string, error) {
	format, _ := cmd.Flags().GetString("output")
	if err := validateOutputFormat(format); err != nil {
		return "", err
	}
	return format, nil
}

// writeServiceStructured serializes v to stdout in the requested structured
// format. It is only called for json/yaml; text is rendered by the caller.
func writeServiceStructured(cmd *cobra.Command, format string, v any) error {
	out, err := formatStructuredValue(format, v)
	if err != nil {
		return err
	}
	fmt.Fprint(cmd.OutOrStdout(), out)
	return nil
}

// resolveServiceAccount resolves the account for a service operation. In
// structured modes (quiet=true) the auto-detect informational line is
// suppressed so stdout carries only the serialized document.
func resolveServiceAccount(cmd *cobra.Command, manager *auth.Manager, quiet bool) (*auth.Account, error) {
	accountName, _ := cmd.Flags().GetString("account")
	if accountName == "" {
		name, err := manager.ResolveMainAccountName()
		if err != nil {
			return nil, fmt.Errorf("you must specify an account with --account")
		}
		accountName = name
		if !quiet {
			fmt.Printf("Using the only default account '%s'\n", accountName)
		}
	}
	acc, err := manager.GetAccount(accountName)
	if err != nil {
		return nil, fmt.Errorf("account '%s' does not exist. Use 'onecloudriver account add' to add it: %w", accountName, err)
	}
	return acc, nil
}

// installAllText installs the service for every configured account, preserving
// the original human-oriented text output.
func installAllText(cmd *cobra.Command, manager *auth.Manager, mountpoint string, enable bool) error {
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
		mp, _ := resolveInstallMountpoint(mountpoint, acc, cmd.OutOrStdout())
		if err := service.InstallService(mp, account); err != nil {
			return err
		}
		if enable {
			service.EnableUnit(account)
		}
	}
	return nil
}

// installAllStructured installs the service for every configured account and
// aggregates the result into a single ActionResult document.
func installAllStructured(cmd *cobra.Command, manager *auth.Manager, mountpoint string, enable bool, format string) error {
	accounts := manager.ListAccounts()
	sort.Strings(accounts)
	if len(accounts) == 0 {
		return fmt.Errorf("no accounts configured. Use 'onecloudriver account add' to add one")
	}

	result := service.ActionResult{Action: "install"}
	affected := make([]string, 0, len(accounts))
	var serviceFile string
	seenWarnings := map[string]struct{}{}
	var warnings []string
	for _, account := range accounts {
		acc, err := manager.GetAccount(account)
		if err != nil {
			result.Error = err.Error()
			result.AffectedAccounts = affected
			_ = writeServiceStructured(cmd, format, result)
			return err
		}
		mp, warn := resolveInstallMountpoint(mountpoint, acc, cmd.ErrOrStderr())
		if warn != "" {
			if _, ok := seenWarnings[warn]; !ok {
				seenWarnings[warn] = struct{}{}
				warnings = append(warnings, warn)
			}
		}
		res, err := service.InstallServiceResult(mp, account)
		if err != nil {
			result.Error = res.Error
			result.AffectedAccounts = affected
			_ = writeServiceStructured(cmd, format, result)
			return err
		}
		serviceFile = res.ServiceFile
		if enable {
			if err := service.EnableUnitQuiet(account); err != nil {
				result.Error = err.Error()
				result.AffectedAccounts = affected
				_ = writeServiceStructured(cmd, format, result)
				return err
			}
		}
		affected = append(affected, account)
	}
	result.OK = true
	result.AffectedAccounts = affected
	result.ServiceFile = serviceFile
	if len(warnings) > 0 {
		result.Warning = strings.Join(warnings, "; ")
	}
	return writeServiceStructured(cmd, format, result)
}

// stopAllStructured stops and unmounts every configured account, aggregating
// the result into a single ActionResult document.
func stopAllStructured(cmd *cobra.Command, accounts []string, format string) error {
	sort.Strings(accounts)
	result := service.ActionResult{Action: "stop"}
	affected := make([]string, 0, len(accounts))
	for _, account := range accounts {
		res, err := service.StopServiceResult(account)
		if err != nil {
			result.Error = res.Error
			result.AffectedAccounts = affected
			_ = writeServiceStructured(cmd, format, result)
			return err
		}
		affected = append(affected, account)
	}
	result.OK = true
	result.AffectedAccounts = affected
	result.Message = "All accounts stopped."
	return writeServiceStructured(cmd, format, result)
}

func formatServiceInstances(w io.Writer, instances []service.InstanceInfo) {
	if len(instances) == 0 {
		fmt.Fprintln(w, printer.Info, "No onecloudriver services installed. Use 'onecloudriver service install' to install one.")
		return
	}

	fmt.Fprintf(w, "%-32s %-10s %-14s %-16s %s\n", "ACCOUNT", "ENABLED", "STATE", "SUBSTATE", "MOUNTPOINT")
	for _, instance := range instances {
		symbol := printer.Warning
		switch instance.State {
		case "running":
			symbol = printer.Success
		case "failed":
			symbol = printer.Error
		}
		state := fmt.Sprintf("%s %s", symbol, instance.State)
		fmt.Fprintf(w, "%-32s %-10s %-14s %-16s %s\n",
			instance.Account,
			instance.Enabled,
			state,
			instance.SubState,
			instance.Mountpoint,
		)
	}
}

func registerServiceCmd(root *cobra.Command) {
	// Persistent --output/-o: inherited by every service subcommand so their
	// results can be rendered as text (default), json or yaml.
	serviceCmd.PersistentFlags().StringP("output", "o", "text", "Output format: text, json, yaml")

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
		serviceListCmd,
		serviceStatusCmd,
		serviceStartCmd,
		serviceStopCmd,
	)

	root.AddCommand(serviceCmd)
}

// expandHomePrefix expands a leading '~' (or '~/') to the user's absolute
// home directory. Used by resolveInstallMountpoint so the value printed by
// 'service install' matches the absolute path written to the systemd unit
// (systemd does not expand '~' in ExecStart). Values without a leading '~'
// (and forms like '~otheruser/...') are returned unchanged.
//
// Pairs with service.normalizeMountpointForUnit (same normalization, but to
// the %h specifier, applied defensively at unit generation time).
func expandHomePrefix(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

// resolveInstallMountpoint determines the mountpoint for service install:
//  1. If the user passed --mountpoint explicitly → use it (expanded)
//  2. If the account has a defaultMountpoint containing the %i instance
//     specifier in the JSON → use it (expanded)
//  3. Fallback: the absolute form of ~/OneDrive/%%i
//
// A defaultMountpoint without %i is the concrete last mountpoint saved by the
// interactive mount command. Reusing it verbatim in the shared @.service
// template would make every account mount at the same directory, so it is
// ignored with a warning instead of being used.
//
// Diagnostics are written to w (may be nil to suppress them in structured
// output modes). The returned warning is non-empty when the saved mountpoint
// was ignored; text mode already carries it to w, and structured callers
// surface it in the ActionResult.
func resolveInstallMountpoint(explicitFlag string, acc *auth.Account, w io.Writer) (mountpoint, warning string) {
	if explicitFlag != "" {
		return expandHomePrefix(explicitFlag), ""
	}

	fallback := expandHomePrefix("~/OneDrive/%i")
	saved := acc.Mount.DefaultMountpoint

	switch {
	case saved != "" && strings.Contains(saved, "%i"):
		mp := expandHomePrefix(saved)
		if w != nil {
			fmt.Fprintf(w, "Using saved mountpoint: %s\n", mp)
		}
		return mp, ""
	case saved != "":
		warning = fmt.Sprintf("Ignoring account-specific saved mountpoint %q (no %%i placeholder); using %s", saved, fallback)
		if w != nil {
			fmt.Fprintf(w, "%s %s\n", printer.Warning, warning)
		}
	default:
		if w != nil {
			fmt.Fprintf(w, "Using default mountpoint: %s\n", fallback)
		}
	}
	return fallback, warning
}
