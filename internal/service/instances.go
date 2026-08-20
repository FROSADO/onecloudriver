package service

import (
	"bufio"
	"fmt"
	"sort"
	"strings"
)

// InstanceInfo describes one installed onecloudriver user-service instance.
type InstanceInfo struct {
	Unit        string `json:"unit" yaml:"unit"`
	Account     string `json:"account" yaml:"account"`
	Enabled     string `json:"enabled" yaml:"enabled"`
	ActiveState string `json:"active_state" yaml:"active_state"`
	SubState    string `json:"sub_state" yaml:"sub_state"`
	State       string `json:"state" yaml:"state"`
	Mountpoint  string `json:"mountpoint,omitempty" yaml:"mountpoint,omitempty"`
}

type installedUnit struct {
	unit string
}

const (
	serviceUnitPrefix = "onecloudriver@"
	serviceUnitSuffix = ".service"
)

// ListInstances returns every installed instantiated onecloudriver user unit,
// including disabled, stopped, and never-started instances.
func ListInstances() ([]InstanceInfo, error) {
	return defaultSystemdClient.listInstances()
}

func (c systemdClient) listInstances() ([]InstanceInfo, error) {
	units, err := c.listInstalledUnits()
	if err != nil {
		return nil, err
	}
	instances := make([]InstanceInfo, 0, len(units))
	for _, installed := range units {
		show, err := c.showUnit(installed.unit)
		if err != nil {
			return nil, fmt.Errorf("querying installed unit %s: %w", installed.unit, err)
		}

		account, ok := accountFromUnit(installed.unit)
		if !ok {
			return nil, fmt.Errorf("invalid onecloudriver unit name %q", installed.unit)
		}
		status := buildUnitStatus(installed.unit, account, show)
		instances = append(instances, InstanceInfo{
			Unit:        installed.unit,
			Account:     account,
			Enabled:     show.enabled,
			ActiveState: status.ActiveState,
			SubState:    status.SubState,
			State:       status.State,
			Mountpoint:  status.Mountpoint,
		})
	}

	sort.Slice(instances, func(i, j int) bool {
		if instances[i].Account == instances[j].Account {
			return instances[i].Unit < instances[j].Unit
		}
		return instances[i].Account < instances[j].Account
	})
	return instances, nil
}

// listInstalledUnits discovers instantiated onecloudriver units via
// systemctl list-units --all. Unlike list-unit-files (which only shows
// unit files on disk and therefore never lists instantiated units derived
// from a template), list-units --all reports every loaded unit including
// inactive and disabled instances.
func (c systemdClient) listInstalledUnits() ([]installedUnit, error) {
	stdout, stderr, err := c.run(
		"systemctl",
		"--user",
		"list-units",
		"--all",
		"--type=service",
		"--no-legend",
		"--plain",
		"onecloudriver@*",
	)
	if err != nil {
		message := strings.TrimSpace(string(stderr))
		if len(stdout) == 0 && isNoInstalledUnitsMessage(message) {
			return []installedUnit{}, nil
		}
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("could not list installed systemd units: %s: %w", message, err)
	}
	return parseInstalledUnits(string(stdout))
}

// parseInstalledUnits parses the output of list-units --all. The first
// column contains the unit name; the remaining columns (LOAD, ACTIVE, SUB,
// DESCRIPTION) are not needed here because showUnit retrieves full details.
// The template unit is deliberately excluded because it is not an account
// instance.
func parseInstalledUnits(output string) ([]installedUnit, error) {
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 || !isOnecloudriverUnit(fields[0]) {
			continue
		}
		if fields[0] == serviceUnitPrefix+serviceUnitSuffix {
			continue
		}
		if _, ok := accountFromUnit(fields[0]); !ok {
			return nil, fmt.Errorf("invalid onecloudriver unit name %q", fields[0])
		}
		seen[fields[0]] = true
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading list-units output: %w", err)
	}

	units := make([]installedUnit, 0, len(seen))
	for unit := range seen {
		units = append(units, installedUnit{unit: unit})
	}
	sort.Slice(units, func(i, j int) bool {
		return units[i].unit < units[j].unit
	})
	return units, nil
}

func isNoInstalledUnitsMessage(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "no files found") ||
		strings.Contains(message, "no units found") ||
		strings.Contains(message, "no units matching") ||
		strings.Contains(message, "0 units listed")
}

func isOnecloudriverUnit(unit string) bool {
	return strings.HasPrefix(unit, serviceUnitPrefix) && strings.HasSuffix(unit, serviceUnitSuffix)
}

func accountFromUnit(unit string) (string, bool) {
	if !isOnecloudriverUnit(unit) {
		return "", false
	}
	account := strings.TrimSuffix(strings.TrimPrefix(unit, serviceUnitPrefix), serviceUnitSuffix)
	return account, account != ""
}
