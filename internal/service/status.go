package service

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/frosado/onecloudriver/internal/printer"
)

const defaultJournalLines = 10

// UnitStatus contains the machine-readable state of one service instance.
// Raw systemd states are kept alongside State so callers do not lose detail
// when the CLI renders a concise label.
type UnitStatus struct {
	Unit        string   `json:"unit" yaml:"unit"`
	Account     string   `json:"account" yaml:"account"`
	ActiveState string   `json:"active_state" yaml:"active_state"`
	SubState    string   `json:"sub_state" yaml:"sub_state"`
	State       string   `json:"state" yaml:"state"`
	PID         int64    `json:"pid,omitempty" yaml:"pid,omitempty"`
	Mountpoint  string   `json:"mountpoint,omitempty" yaml:"mountpoint,omitempty"`
	JournalTail []string `json:"journal_tail,omitempty" yaml:"journal_tail,omitempty"`
}

// commandRunner executes a command and returns stdout, stderr, and its error.
// Keeping this boundary small makes systemd parsing tests independent of a
// running user session.
type commandRunner func(name string, args ...string) (stdout, stderr []byte, err error)

type systemdClient struct {
	run commandRunner
}

var defaultSystemdClient = systemdClient{run: runSystemCommand}

// runSystemCommand executes a fixed executable with separate arguments. It
// intentionally does not invoke a shell: account and unit names are data.
func runSystemCommand(name string, args ...string) ([]byte, []byte, error) {
	//#nosec G204 -- executable names are fixed by the internal service API; arguments are passed without a shell
	cmd := exec.Command(name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return []byte(stdout.String()), []byte(stderr.String()), err
	}
	return []byte(stdout.String()), []byte(stderr.String()), nil
}

// unitName returns the systemd unit name for an account.
func unitName(account string) string {
	return fmt.Sprintf("onecloudriver@%s.service", account)
}

// GetUnitStatus returns the current structured status for an account. A
// service being failed is valid status data; an error is returned only when
// systemd state itself cannot be queried or parsed.
func GetUnitStatus(account string) (UnitStatus, error) {
	status, _, err := defaultSystemdClient.queryUnitStatus(account)
	return status, err
}

// QueryUnitStatus returns the current structured status for an account together
// with a best-effort journal error (nil when the journal was read or not
// needed). The journal error is separate because a missing/inaccessible journal
// must not hide an otherwise valid service state; callers surface it on stderr.
func QueryUnitStatus(account string) (UnitStatus, error, error) {
	return defaultSystemdClient.queryUnitStatus(account)
}

// JournalTail returns up to lines from the unit's user journal without using a
// pager. A non-positive line count uses the default of ten lines.
func JournalTail(unit string, lines int) ([]string, error) {
	return defaultSystemdClient.journalTail(unit, lines)
}

// queryUnitStatus queries state and, for non-running units, best-effort journal
// lines. The journal error is kept separate because a missing/inaccessible
// journal should not hide an otherwise valid service state.
func (c systemdClient) queryUnitStatus(account string) (UnitStatus, error, error) {
	if strings.TrimSpace(account) == "" {
		return UnitStatus{}, nil, fmt.Errorf("account must not be empty")
	}

	unit := unitName(account)
	show, err := c.showUnit(unit)
	if err != nil {
		return UnitStatus{}, nil, err
	}

	status := buildUnitStatus(unit, account, show)

	if status.State == "running" {
		return status, nil, nil
	}

	journal, journalErr := c.journalTail(unit, defaultJournalLines)
	if journalErr == nil {
		status.JournalTail = journal
	}
	return status, journalErr, nil
}

type unitShow struct {
	activeState string
	subState    string
	pid         int64
	execStart   string
	enabled     string
}

func (c systemdClient) showUnit(unit string) (unitShow, error) {
	stdout, stderr, err := c.run(
		"systemctl",
		"--user",
		"show",
		unit,
		"--property=ActiveState,SubState,MainPID,ExecStart,UnitFileState",
		"--no-pager",
	)
	if err != nil {
		message := strings.TrimSpace(string(stderr))
		if message == "" {
			message = err.Error()
		}
		return unitShow{}, fmt.Errorf("could not query systemd unit %s: %s: %w", unit, message, err)
	}
	return parseUnitShowOutput(string(stdout))
}

// parseUnitShowOutput parses the key/value form emitted by systemctl show.
func parseUnitShowOutput(output string) (unitShow, error) {
	var result unitShow
	seenActive := false
	seenSubState := false

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return unitShow{}, fmt.Errorf("malformed systemctl property %q", line)
		}
		switch key {
		case "ActiveState":
			result.activeState = value
			seenActive = true
		case "SubState":
			result.subState = value
			seenSubState = true
		case "MainPID":
			if value == "" {
				continue
			}
			pid, err := strconv.ParseInt(value, 10, 64)
			if err != nil || pid < 0 {
				return unitShow{}, fmt.Errorf("invalid MainPID %q", value)
			}
			result.pid = pid
		case "ExecStart":
			result.execStart = value
		case "UnitFileState":
			result.enabled = value
		}
	}
	if err := scanner.Err(); err != nil {
		return unitShow{}, fmt.Errorf("reading systemctl properties: %w", err)
	}
	if !seenActive || !seenSubState {
		return unitShow{}, fmt.Errorf("systemctl output is missing ActiveState or SubState")
	}
	return result, nil
}

func buildUnitStatus(unit, account string, show unitShow) UnitStatus {
	return UnitStatus{
		Unit:        unit,
		Account:     account,
		ActiveState: show.activeState,
		SubState:    show.subState,
		State:       normalizeUnitState(show.activeState, show.subState),
		PID:         show.pid,
		Mountpoint:  expandMountpoint(parseMountpoint(show.execStart), account),
	}
}

func normalizeUnitState(activeState, subState string) string {
	switch {
	case activeState == "active" && subState == "running":
		return "running"
	case activeState == "activating" && subState == "auto-restart":
		return "restarting"
	case activeState == "activating":
		return "starting"
	case activeState == "deactivating":
		return "stopping"
	case activeState == "failed":
		return "failed"
	case activeState == "inactive" || (activeState == "active" && subState == "exited"):
		return "stopped"
	default:
		return "unknown"
	}
}

// parseMountpoint extracts the argument after "mount" from systemd's
// ExecStart representation. Generated units contain an argv[] field; the
// fallback also accepts a plain command for parser fixtures.
func parseMountpoint(execStart string) string {
	command := execStart
	if start := strings.Index(command, "argv[]="); start >= 0 {
		command = command[start+len("argv[]="):]
		if end := strings.Index(command, " ;"); end >= 0 {
			command = command[:end]
		}
	}

	mountStart := strings.Index(command, " mount ")
	if mountStart < 0 {
		return ""
	}
	mountpoint := command[mountStart+len(" mount "):]
	if destStart := strings.Index(mountpoint, " -a "); destStart >= 0 {
		mountpoint = mountpoint[:destStart]
	} else {
		return ""
	}
	return decodeSystemdArgument(strings.TrimSpace(mountpoint))
}

func decodeSystemdArgument(value string) string {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}

	var decoded strings.Builder
	decoded.Grow(len(value))
	for i := 0; i < len(value); i++ {
		if i+3 < len(value) && value[i] == '\\' && value[i+1] == 'x' {
			byteValue, err := strconv.ParseUint(value[i+2:i+4], 16, 8)
			if err == nil {
				decoded.WriteByte(byte(byteValue))
				i += 3
				continue
			}
		}
		decoded.WriteByte(value[i])
	}
	return decoded.String()
}

func expandMountpoint(mountpoint, account string) string {
	if mountpoint == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil {
		mountpoint = strings.ReplaceAll(mountpoint, "%h", home)
	}
	return strings.ReplaceAll(mountpoint, "%i", account)
}

func (c systemdClient) journalTail(unit string, lines int) ([]string, error) {
	if lines <= 0 {
		lines = defaultJournalLines
	}
	stdout, stderr, err := c.run(
		"journalctl",
		"--user",
		"-u",
		unit,
		"-n",
		strconv.Itoa(lines),
		"--no-pager",
	)
	if err != nil {
		message := strings.TrimSpace(string(stderr))
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("could not read journal for %s: %s: %w", unit, message, err)
	}
	trimmed := strings.TrimRight(string(stdout), "\r\n")
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\n"), nil
}

// Status shows the status of the service. A failed service is reported as a
// successful status query, with its recent journal lines when available.
func Status(args []string) error {
	if len(args) > 0 {
		status, journalErr, err := defaultSystemdClient.queryUnitStatus(args[0])
		if err != nil {
			return err
		}
		if journalErr != nil {
			fmt.Fprintf(os.Stderr, "%s Could not read the service journal: %v\n", printer.Warning, journalErr)
		}
		printUnitStatus(os.Stdout, status)
		return nil
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

func printUnitStatus(w io.Writer, status UnitStatus) {
	symbol := printer.Warning
	if status.State == "running" {
		symbol = printer.Success
	}
	if status.State == "failed" {
		symbol = printer.Error
	}

	fmt.Fprintf(w, "%s State:    %s (%s)\n", symbol, status.State, status.ActiveState)
	fmt.Fprintf(w, "  Account:   %s\n", status.Account)
	fmt.Fprintf(w, "  Unit:      %s\n", status.Unit)
	fmt.Fprintf(w, "  SubState:  %s\n", status.SubState)
	if status.PID == 0 {
		fmt.Fprintln(w, "  PID:      -")
	} else {
		fmt.Fprintf(w, "  PID:      %d\n", status.PID)
	}
	if status.Mountpoint == "" {
		fmt.Fprintln(w, "  Mount:    -")
	} else {
		fmt.Fprintf(w, "  Mount:    %s\n", status.Mountpoint)
	}

	if len(status.JournalTail) == 0 {
		return
	}
	fmt.Fprintln(w, "\nLast 10 journal lines:")
	for _, line := range status.JournalTail {
		fmt.Fprintln(w, line)
	}
}
