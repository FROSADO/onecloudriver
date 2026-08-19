package service

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseUnitShowOutput(t *testing.T) {
	output := "ActiveState=failed\nSubState=exit-code\nMainPID=1234\nExecStart={ path=/usr/local/bin/onecloudriver ; argv[]=/usr/local/bin/onecloudriver mount /home/user/OneDrive/user@outlook.com -a user@outlook.com ; ignore_errors=no }\n"

	got, err := parseUnitShowOutput(output)
	if err != nil {
		t.Fatalf("parseUnitShowOutput: %v", err)
	}
	if got.activeState != "failed" || got.subState != "exit-code" || got.pid != 1234 {
		t.Fatalf("parsed state = %#v, want failed/exit-code/1234", got)
	}
	if got.execStart == "" {
		t.Fatal("parsed ExecStart is empty")
	}
}

func TestParseUnitShowOutput_RejectsMalformedOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{name: "missing active state", output: "SubState=dead\n"},
		{name: "missing sub state", output: "ActiveState=inactive\n"},
		{name: "invalid pid", output: "ActiveState=active\nSubState=running\nMainPID=not-a-pid\n"},
		{name: "malformed property", output: "ActiveState\nSubState=running\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseUnitShowOutput(tt.output); err == nil {
				t.Fatal("parseUnitShowOutput should reject malformed output")
			}
		})
	}
}

func TestNormalizeUnitState(t *testing.T) {
	tests := []struct {
		name        string
		activeState string
		subState    string
		want        string
	}{
		{name: "running", activeState: "active", subState: "running", want: "running"},
		{name: "failed", activeState: "failed", subState: "exit-code", want: "failed"},
		{name: "stopped", activeState: "inactive", subState: "dead", want: "stopped"},
		{name: "exited", activeState: "active", subState: "exited", want: "stopped"},
		{name: "restarting", activeState: "activating", subState: "auto-restart", want: "restarting"},
		{name: "starting", activeState: "activating", subState: "start", want: "starting"},
		{name: "stopping", activeState: "deactivating", subState: "stop-sigterm", want: "stopping"},
		{name: "unknown", activeState: "maintenance", subState: "custom", want: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeUnitState(tt.activeState, tt.subState); got != tt.want {
				t.Errorf("normalizeUnitState(%q, %q) = %q, want %q", tt.activeState, tt.subState, got, tt.want)
			}
		})
	}
}

func TestParseMountpoint(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "systemd argv representation",
			input: "{ path=/usr/bin/onecloudriver ; argv[]=/usr/bin/onecloudriver mount /home/user/OneDrive/account -a account ; ignore_errors=no }",
			want:  "/home/user/OneDrive/account",
		},
		{
			name:  "quoted path",
			input: "{ argv[]=/usr/bin/onecloudriver mount \"/home/user/My Drive\" -a account ; }",
			want:  "/home/user/My Drive",
		},
		{
			name:  "escaped space",
			input: "{ argv[]=/usr/bin/onecloudriver mount /home/user/My\\x20Drive -a account ; }",
			want:  "/home/user/My Drive",
		},
		{
			name:  "plain command",
			input: "/usr/bin/onecloudriver mount %h/OneDrive/%i -a account",
			want:  "%h/OneDrive/%i",
		},
		{
			name:  "missing mount argument",
			input: "{ path=/usr/bin/onecloudriver ; argv[]=/usr/bin/onecloudriver -version ; }",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseMountpoint(tt.input); got != tt.want {
				t.Errorf("parseMountpoint(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildUnitStatusExpandsSpecifiers(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	show := unitShow{
		activeState: "active",
		subState:    "running",
		execStart:   "{ argv[]=/usr/bin/onecloudriver mount %h/OneDrive/%i -a account ; }",
	}

	got := buildUnitStatus("onecloudriver@account.service", "account", show)
	if got.State != "running" {
		t.Errorf("State = %q, want running", got.State)
	}
	if got.Mountpoint != "/home/tester/OneDrive/account" {
		t.Errorf("Mountpoint = %q, want %q", got.Mountpoint, "/home/tester/OneDrive/account")
	}
}

func TestSystemdClientQueryUnitStatus_RejectsEmptyAccount(t *testing.T) {
	client := systemdClient{run: func(_ string, _ ...string) ([]byte, []byte, error) {
		t.Fatal("runner should not be called for an empty account")
		return nil, nil, nil
	}}

	if _, _, err := client.queryUnitStatus("  "); err == nil {
		t.Fatal("queryUnitStatus should reject an empty account")
	}
}

func TestSystemdClientQueryUnitStatus_ReturnsSystemctlError(t *testing.T) {
	client := systemdClient{run: func(_ string, _ ...string) ([]byte, []byte, error) {
		return nil, []byte("Unit not found"), errors.New("exit status 5")
	}}

	if _, _, err := client.queryUnitStatus("account"); err == nil || !strings.Contains(err.Error(), "Unit not found") {
		t.Fatalf("queryUnitStatus error = %v, want systemctl stderr", err)
	}
}

func TestSystemdClientQueryUnitStatus(t *testing.T) {
	var calls [][]string
	client := systemdClient{run: func(name string, args ...string) ([]byte, []byte, error) {
		calls = append(calls, append([]string{name}, args...))
		switch name {
		case "systemctl":
			return []byte("ActiveState=failed\nSubState=exit-code\nMainPID=0\nExecStart={ argv[]=/usr/bin/onecloudriver mount /home/user/OneDrive/account -a account ; }\n"), nil, nil
		case "journalctl":
			return []byte("first line\nsecond line\n"), nil, nil
		default:
			return nil, nil, errors.New("unexpected command")
		}
	},
	}

	got, journalErr, err := client.queryUnitStatus("account")
	if err != nil {
		t.Fatalf("queryUnitStatus: %v", err)
	}
	if journalErr != nil {
		t.Fatalf("queryUnitStatus journal error: %v", journalErr)
	}
	if got.State != "failed" || got.ActiveState != "failed" || got.PID != 0 {
		t.Errorf("status = %#v, want failed state with PID 0", got)
	}
	if want := []string{"first line", "second line"}; !reflect.DeepEqual(got.JournalTail, want) {
		t.Errorf("JournalTail = %#v, want %#v", got.JournalTail, want)
	}
	if len(calls) != 2 || calls[0][0] != "systemctl" || calls[1][0] != "journalctl" {
		t.Errorf("commands = %#v, want systemctl then journalctl", calls)
	}
	if !strings.Contains(strings.Join(calls[1], " "), "-n 10") {
		t.Errorf("journal command = %#v, want default line count", calls[1])
	}
}

func TestSystemdClientQueryUnitStatus_DoesNotReadJournalWhenRunning(t *testing.T) {
	journalCalled := false
	client := systemdClient{run: func(name string, _ ...string) ([]byte, []byte, error) {
		if name == "journalctl" {
			journalCalled = true
		}
		return []byte("ActiveState=active\nSubState=running\nMainPID=42\n"), nil, nil
	},
	}

	got, journalErr, err := client.queryUnitStatus("account")
	if err != nil || journalErr != nil {
		t.Fatalf("queryUnitStatus = %#v, %v, %v", got, journalErr, err)
	}
	if got.State != "running" || got.PID != 42 {
		t.Errorf("status = %#v, want running/42", got)
	}
	if journalCalled {
		t.Error("running unit should not query the journal")
	}
}

func TestSystemdClientQueryUnitStatus_JournalFailureIsNonFatal(t *testing.T) {
	client := systemdClient{run: func(name string, _ ...string) ([]byte, []byte, error) {
		if name == "systemctl" {
			return []byte("ActiveState=inactive\nSubState=dead\nMainPID=0\n"), nil, nil
		}
		return nil, []byte("journal unavailable"), errors.New("exit status 1")
	},
	}

	got, journalErr, err := client.queryUnitStatus("account")
	if err != nil {
		t.Fatalf("queryUnitStatus: %v", err)
	}
	if journalErr == nil {
		t.Fatal("journal error should be returned separately")
	}
	if got.State != "stopped" {
		t.Errorf("State = %q, want stopped", got.State)
	}
	if len(got.JournalTail) != 0 {
		t.Errorf("JournalTail = %#v, want empty", got.JournalTail)
	}
}

func TestSystemdClientJournalTail_DefaultsLineCountAndHandlesEmpty(t *testing.T) {
	var gotArgs []string
	client := systemdClient{run: func(name string, args ...string) ([]byte, []byte, error) {
		gotArgs = append([]string{name}, args...)
		return nil, nil, nil
	},
	}

	lines, err := client.journalTail("onecloudriver@account.service", 0)
	if err != nil {
		t.Fatalf("journalTail: %v", err)
	}
	if lines != nil {
		t.Errorf("journalTail = %#v, want nil for empty output", lines)
	}
	if !strings.Contains(strings.Join(gotArgs, " "), "-n 10") {
		t.Errorf("journal command = %#v, want -n 10", gotArgs)
	}
}

func TestPrintUnitStatus(t *testing.T) {
	var output bytes.Buffer
	printUnitStatus(&output, UnitStatus{
		ActiveState: "failed",
		SubState:    "exit-code",
		State:       "failed",
		PID:         0,
		Mountpoint:  "/home/user/OneDrive/account",
		JournalTail: []string{"failure reason", "second line"},
	})

	text := output.String()
	for _, want := range []string{
		"State:    failed (failed)",
		"Account:   ",
		"Unit:      ",
		"SubState:  exit-code",
		"PID:      -",
		"Mount:    /home/user/OneDrive/account",
		"Last 10 journal lines:",
		"failure reason",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("status output missing %q:\n%s", want, text)
		}
	}
}
