package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/frosado/onecloudriver/internal/service"
)

func TestFormatServiceInstances_Empty(t *testing.T) {
	var output bytes.Buffer
	formatServiceInstances(&output, nil)

	if got := output.String(); !strings.Contains(got, "No onecloudriver services installed") {
		t.Errorf("empty output = %q, want installation guidance", got)
	}
}

func TestFormatServiceInstances_Table(t *testing.T) {
	var output bytes.Buffer
	formatServiceInstances(&output, []service.InstanceInfo{
		{
			Account:     "user@example.com",
			Enabled:     "enabled",
			ActiveState: "active",
			SubState:    "running",
			State:       "running",
			Mountpoint:  "/home/user/OneDrive/user@example.com",
		},
		{
			Account:     "failed@example.com",
			Enabled:     "disabled",
			ActiveState: "failed",
			SubState:    "exit-code",
			State:       "failed",
		},
	})

	text := output.String()
	for _, want := range []string{
		"ACCOUNT",
		"ENABLED",
		"STATE",
		"SUBSTATE",
		"MOUNTPOINT",
		"user@example.com",
		"failed@example.com",
		"running",
		"failed",
		"/home/user/OneDrive/user@example.com",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("service list output missing %q:\n%s", want, text)
		}
	}
}
