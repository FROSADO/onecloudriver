package service

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseInstalledUnits(t *testing.T) {
	output := "onecloudriver@.service loaded inactive dead OneCloudRiver template\n" +
		"onecloudriver@z@example.com.service loaded inactive dead OneCloudRiver for z\n" +
		"other.service loaded active running Other service\n" +
		"onecloudriver@a@example.com.service loaded active running OneCloudRiver for a\n" +
		"onecloudriver@z@example.com.service loaded inactive dead OneCloudRiver for z\n"

	got, err := parseInstalledUnits(output)
	if err != nil {
		t.Fatalf("parseInstalledUnits: %v", err)
	}
	want := []installedUnit{
		{unit: "onecloudriver@a@example.com.service"},
		{unit: "onecloudriver@z@example.com.service"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseInstalledUnits = %#v, want %#v", got, want)
	}
}

func TestParseInstalledUnits_EmptyOutput(t *testing.T) {
	got, err := parseInstalledUnits("")
	if err != nil {
		t.Fatalf("parseInstalledUnits: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("parseInstalledUnits = %#v, want empty", got)
	}
}

func TestAccountFromUnit(t *testing.T) {
	tests := []struct {
		name  string
		unit  string
		want  string
		valid bool
	}{
		{name: "email account", unit: "onecloudriver@user@example.com.service", want: "user@example.com", valid: true},
		{name: "empty template", unit: "onecloudriver@.service", valid: false},
		{name: "other unit", unit: "other.service", valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, valid := accountFromUnit(tt.unit)
			if got != tt.want || valid != tt.valid {
				t.Errorf("accountFromUnit(%q) = %q, %v; want %q, %v", tt.unit, got, valid, tt.want, tt.valid)
			}
		})
	}
}

func TestSystemdClientListInstances(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	client := systemdClient{run: func(name string, args ...string) ([]byte, []byte, error) {
		if name != "systemctl" {
			return nil, nil, errors.New("unexpected executable")
		}
		command := strings.Join(args, " ")
		if strings.Contains(command, "list-units") {
			return []byte("onecloudriver@z@example.com.service loaded inactive dead OneCloudRiver for z\n" +
				"onecloudriver@a@example.com.service loaded active running OneCloudRiver for a\n"), nil, nil
		}
		switch {
		case strings.Contains(command, "onecloudriver@a@example.com.service"):
			return []byte("ActiveState=active\nSubState=running\nMainPID=12\nExecStart={ argv[]=/usr/bin/onecloudriver mount %h/OneDrive/%i -a a@example.com ; }\nUnitFileState=enabled\n"), nil, nil
		case strings.Contains(command, "onecloudriver@z@example.com.service"):
			return []byte("ActiveState=inactive\nSubState=dead\nMainPID=0\nExecStart={ argv[]=/usr/bin/onecloudriver mount /srv/OneDrive/z -a z@example.com ; }\nUnitFileState=disabled\n"), nil, nil
		default:
			return nil, nil, errors.New("unexpected systemctl arguments")
		}
	},
	}

	got, err := client.listInstances()
	if err != nil {
		t.Fatalf("listInstances: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("listInstances returned %d instances, want 2", len(got))
	}
	if got[0].Account != "a@example.com" || got[0].State != "running" || got[0].Mountpoint != "/home/tester/OneDrive/a@example.com" {
		t.Errorf("first instance = %#v", got[0])
	}
	if got[1].Account != "z@example.com" || got[1].Enabled != "disabled" || got[1].State != "stopped" {
		t.Errorf("second instance = %#v", got[1])
	}
}

func TestSystemdClientListInstances_NoUnitsMessageIsEmpty(t *testing.T) {
	client := systemdClient{run: func(_ string, _ ...string) ([]byte, []byte, error) {
		return nil, []byte("No units matching onecloudriver@* found."), errors.New("exit status 1")
	}}

	got, err := client.listInstances()
	if err != nil {
		t.Fatalf("listInstances: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("listInstances = %#v, want empty non-nil slice", got)
	}
}

func TestSystemdClientListInstances_Empty(t *testing.T) {
	client := systemdClient{run: func(_ string, _ ...string) ([]byte, []byte, error) {
		return []byte("onecloudriver@.service loaded inactive dead OneCloudRiver template\n"), nil, nil
	}}

	got, err := client.listInstances()
	if err != nil {
		t.Fatalf("listInstances: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("listInstances = %#v, want empty non-nil slice", got)
	}
}

func TestSystemdClientListInstances_ReturnsShowError(t *testing.T) {
	client := systemdClient{run: func(_ string, args ...string) ([]byte, []byte, error) {
		if strings.Contains(strings.Join(args, " "), "list-units") {
			return []byte("onecloudriver@account.service loaded inactive dead OneCloudRiver\n"), nil, nil
		}
		return nil, []byte("unit unavailable"), errors.New("exit status 5")
	}}

	if _, err := client.listInstances(); err == nil || !strings.Contains(err.Error(), "onecloudriver@account.service") {
		t.Fatalf("listInstances error = %v, want unit context", err)
	}
}
