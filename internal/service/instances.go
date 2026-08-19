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
	unit    string
	enabled string
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
			Enabled:     installed.enabled,
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

func (c systemdClient) listInstalledUnits() ([]installedUnit, error) {
	stdout, stderr, err := c.run(
		"systemctl",
		"--user",
		"list-unit-files",
		"--type=service",
		"--no-legend",
		"--plain",
		"onecloudriver@*.service",
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

// parseInstalledUnits parses the first two columns of list-unit-files output.
// The template unit is deliberately excluded because it is not an account
// instance.
func parseInstalledUnits(output string) ([]installedUnit, error) {
	byUnit := make(map[string]string)
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
		if len(fields) < 2 {
			return nil, fmt.Errorf("malformed list-unit-files row %q", line)
		}
		if _, ok := accountFromUnit(fields[0]); !ok {
			return nil, fmt.Errorf("invalid onecloudriver unit name %q", fields[0])
		}
		byUnit[fields[0]] = fields[1]
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading list-unit-files output: %w", err)
	}

	units := make([]installedUnit, 0, len(byUnit))
	for unit, enabled := range byUnit {
		units = append(units, installedUnit{unit: unit, enabled: enabled})
	}
	sort.Slice(units, func(i, j int) bool {
		return units[i].unit < units[j].unit
	})
	return units, nil
}

func isNoInstalledUnitsMessage(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "no files found") || strings.Contains(message, "no units found")
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
