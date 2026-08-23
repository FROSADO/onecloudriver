package service

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ActionResult is the machine-readable result envelope for the service action
// subcommands (install, uninstall, start, stop). The CLI serializes it for
// structured output modes; the service package never renders it.
//
// Field semantics:
//   - Action is one of install, uninstall, start, or stop.
//   - OK reports whether the operation completed (a successful no-op counts).
//   - Account is set for single-account operations.
//   - AffectedAccounts is set for --all operations and sorted deterministically.
//   - ServiceFile and Mountpoint are set when the operation has unambiguous values.
//   - Warning carries a non-fatal advisory (e.g. a saved mountpoint that was
//     ignored), mirroring what text mode prints so structured consumers see it.
//   - Message is a concise human-readable explanation (not an API contract).
//   - Error is set when the action failed after a valid result context existed.
type ActionResult struct {
	Action           string   `json:"action" yaml:"action"`
	OK               bool     `json:"ok" yaml:"ok"`
	Account          string   `json:"account,omitempty" yaml:"account,omitempty"`
	AffectedAccounts []string `json:"affected_accounts,omitempty" yaml:"affected_accounts,omitempty"`
	ServiceFile      string   `json:"service_file,omitempty" yaml:"service_file,omitempty"`
	Mountpoint       string   `json:"mountpoint,omitempty" yaml:"mountpoint,omitempty"`
	Warning          string   `json:"warning,omitempty" yaml:"warning,omitempty"`
	Message          string   `json:"message,omitempty" yaml:"message,omitempty"`
	Error            string   `json:"error,omitempty" yaml:"error,omitempty"`
}

// runSystemctlQuiet runs `systemctl --user <action> <unit>` capturing its
// output so structured stdout is never contaminated.
func runSystemctlQuiet(action, unit string) error {
	//#nosec G204 -- systemctl with controlled parameters (action and unit are fixed)
	_, _, err := runSystemCommand("systemctl", "--user", action, unit)
	if err != nil {
		return fmt.Errorf("error running systemctl %s %s: %w", action, unit, err)
	}
	return nil
}

// unmountQuiet is the non-printing counterpart of UnmountMountpoint. The
// error is returned (not printed) so structured callers can surface it in the
// ActionResult instead of dropping it.
func unmountQuiet(account string) error {
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

	return unmount(mp)
}

// ensureMountpointDirQuiet creates the concrete mountpoint directory for an
// account without printing, mirroring ensureMountpointDir.
func ensureMountpointDirQuiet(mountpoint, account string) error {
	dir := concreteMountpoint(mountpoint, account)
	if _, err := os.Stat(dir); err == nil {
		return nil // already exists
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("error checking mountpoint %q: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("error creating mountpoint %q: %w", dir, err)
	}
	return nil
}

// InstallServiceResult creates the service file, ensures the mountpoint and
// reloads systemd without writing to stdout. A failure returns the partially
// populated result (OK=false, Error set) alongside the wrapped error so the
// CLI can still emit one machine-readable document and exit non-zero.
func InstallServiceResult(mountpoint, account string) (ActionResult, error) {
	result := ActionResult{
		Action:     "install",
		Account:    account,
		Mountpoint: mountpoint,
	}

	path, err := ServiceFilePath()
	if err != nil {
		result.Error = err.Error()
		return result, err
	}
	result.ServiceFile = path

	// Resolve the binary BEFORE writing anything: a broken ExecStart must be
	// reported now, not when systemd tries to exec it.
	binary, err := resolveBinary(os.Args[0])
	if err != nil {
		result.Error = err.Error()
		return result, err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		result.Error = err.Error()
		return result, fmt.Errorf("error creating systemd directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(ServiceUnit(mountpoint, binary)), 0600); err != nil {
		result.Error = err.Error()
		return result, fmt.Errorf("error writing service file: %w", err)
	}

	if err := ensureMountpointDirQuiet(mountpoint, account); err != nil {
		result.Error = err.Error()
		return result, err
	}

	//#nosec G204 -- systemctl command with fixed arguments, no user input
	if _, _, err := runSystemCommand("systemctl", "--user", "daemon-reload"); err != nil {
		result.Error = err.Error()
		return result, fmt.Errorf("error reloading systemd daemon: %w", err)
	}

	result.OK = true
	return result, nil
}

// EnableUnitQuiet enables and starts a systemd unit for an account without
// writing to stdout. It is the structured-output counterpart of EnableUnit.
func EnableUnitQuiet(account string) error {
	unit := unitName(account)
	//#nosec G204 -- systemctl with controlled parameters
	_, _, err := runSystemCommand("systemctl", "--user", "enable", "--now", unit)
	if err != nil {
		return fmt.Errorf("error enabling systemd unit %s: %w", unit, err)
	}
	return nil
}

// UninstallServiceResult stops and disables instances, removes the service
// file and reloads systemd without writing to stdout, returning an
// ActionResult. With accounts (the former --all behaviour) each listed account
// is processed; otherwise the running instances are discovered via
// `systemctl list-units onecloudriver@*`.
func UninstallServiceResult(accounts ...string) (ActionResult, error) {
	result := ActionResult{Action: "uninstall"}

	path, err := ServiceFilePath()
	if err != nil {
		result.Error = err.Error()
		return result, err
	}

	// Stopping, disabling and unmounting are best-effort (the unit file is
	// removed regardless), but their failures are collected into Warning so a
	// partial uninstall is visible to structured consumers.
	var warnings []string
	affected := make([]string, 0)
	if len(accounts) > 0 {
		affected = append(affected, accounts...)
		for _, account := range accounts {
			if err := unmountQuiet(account); err != nil {
				warnings = append(warnings, err.Error())
			}
			if err := runSystemctlQuiet("stop", unitName(account)); err != nil {
				warnings = append(warnings, err.Error())
			}
			if err := runSystemctlQuiet("disable", unitName(account)); err != nil {
				warnings = append(warnings, err.Error())
			}
		}
	} else {
		//#nosec G204 -- systemctl command with fixed arguments, no user input
		stdout, _, err := runSystemCommand("systemctl", "--user", "list-units",
			"--plain", "--no-legend", "onecloudriver@*")
		if err != nil {
			// The running instances cannot be discovered, so they are not
			// stopped: report it instead of silently skipping the loop.
			warnings = append(warnings,
				fmt.Sprintf("could not list the running instances (they were not stopped): %v", err))
		} else {
			units, perr := parseInstalledUnits(string(stdout))
			if perr != nil {
				result.Error = perr.Error()
				return result, perr
			}
			for _, u := range units {
				account, ok := accountFromUnit(u.unit)
				if !ok {
					continue
				}
				affected = append(affected, account)
				//#nosec G204 -- unit comes from systemctl list-units, not user input
				if err := runSystemctlQuiet("stop", u.unit); err != nil {
					warnings = append(warnings, err.Error())
				}
				//#nosec G204 -- unit comes from systemctl list-units, not user input
				if err := runSystemctlQuiet("disable", u.unit); err != nil {
					warnings = append(warnings, err.Error())
				}
			}
		}
	}
	sort.Strings(affected)
	result.AffectedAccounts = affected
	result.Warning = strings.Join(warnings, "; ")

	if _, err := os.Stat(path); err == nil {
		if err := os.Remove(path); err != nil {
			result.Error = err.Error()
			return result, fmt.Errorf("error removing service file: %w", err)
		}
		result.ServiceFile = path
		if len(accounts) > 0 {
			result.Message = "Service uninstalled for all accounts."
		} else {
			result.Message = "Service uninstalled successfully."
		}
	} else {
		result.Message = "The service was not installed."
		result.OK = true
		return result, nil
	}

	//#nosec G204 -- systemctl command with fixed arguments
	if _, _, err := runSystemCommand("systemctl", "--user", "daemon-reload"); err != nil {
		result.Error = err.Error()
		return result, fmt.Errorf("error reloading systemd daemon: %w", err)
	}

	result.OK = true
	return result, nil
}

// StartServiceResult starts the unit for an account without writing to
// stdout, returning an ActionResult.
func StartServiceResult(account string) (ActionResult, error) {
	result := ActionResult{Action: "start", Account: account}
	if err := runSystemctlQuiet("start", unitName(account)); err != nil {
		result.Error = err.Error()
		return result, err
	}
	result.OK = true
	return result, nil
}

// StopServiceResult unmounts the account's FUSE mountpoint and stops its unit
// without writing to stdout, returning an ActionResult.
func StopServiceResult(account string) (ActionResult, error) {
	result := ActionResult{Action: "stop", Account: account}
	if err := unmountQuiet(account); err != nil {
		// systemd's ExecStop also unmounts, so a failed explicit unmount is
		// not fatal — but it must be visible in the result.
		result.Warning = err.Error()
	}
	if err := runSystemctlQuiet("stop", unitName(account)); err != nil {
		result.Error = err.Error()
		return result, err
	}
	result.OK = true
	return result, nil
}
