package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frosado/onecloudriver/internal/service"
	"github.com/spf13/cobra"
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

func TestServiceCmd_PersistentOutputFlag(t *testing.T) {
	f := serviceCmd.PersistentFlags().Lookup("output")
	if f == nil {
		t.Fatal("service command is missing the persistent --output flag")
	}
	if f.Shorthand != "o" {
		t.Errorf("--output shorthand = %q, want o", f.Shorthand)
	}
	if f.DefValue != "text" {
		t.Errorf("--output default = %q, want text", f.DefValue)
	}
}

func TestResolveServiceOutput(t *testing.T) {
	newCmd := func() *cobra.Command {
		cmd := &cobra.Command{}
		cmd.Flags().StringP("output", "o", "text", "")
		return cmd
	}

	t.Run("defaults to text", func(t *testing.T) {
		format, err := resolveServiceOutput(newCmd())
		if err != nil {
			t.Fatalf("resolveServiceOutput: %v", err)
		}
		if format != "text" {
			t.Errorf("format = %q, want text", format)
		}
	})

	t.Run("accepts json and yaml", func(t *testing.T) {
		for _, format := range []string{"json", "yaml"} {
			cmd := newCmd()
			cmd.Flags().Set("output", format)
			got, err := resolveServiceOutput(cmd)
			if err != nil {
				t.Fatalf("resolveServiceOutput(%q): %v", format, err)
			}
			if got != format {
				t.Errorf("format = %q, want %q", got, format)
			}
		}
	})

	t.Run("rejects unsupported formats", func(t *testing.T) {
		cmd := newCmd()
		cmd.Flags().Set("output", "xml")
		_, err := resolveServiceOutput(cmd)
		if err == nil || !strings.Contains(err.Error(), "unsupported format") {
			t.Fatalf("expected unsupported format error, got %v", err)
		}
	})
}

func TestWriteServiceStructured(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	instances := []service.InstanceInfo{
		{
			Unit:        "onecloudriver@a@example.com.service",
			Account:     "a@example.com",
			Enabled:     "enabled",
			ActiveState: "active",
			SubState:    "running",
			State:       "running",
			Mountpoint:  "/home/user/OneDrive/a@example.com",
		},
	}

	if err := writeServiceStructured(cmd, "json", instances); err != nil {
		t.Fatalf("writeServiceStructured: %v", err)
	}

	var got []service.InstanceInfo
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("structured output is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(got) != 1 || got[0].Account != "a@example.com" {
		t.Errorf("decoded instances = %#v, want a@example.com", got)
	}
}

func TestWriteServiceStructured_EmptyList(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := writeServiceStructured(cmd, "json", []service.InstanceInfo{}); err != nil {
		t.Fatalf("writeServiceStructured: %v", err)
	}
	if strings.TrimSpace(buf.String()) != "[]" {
		t.Errorf("empty list JSON = %q, want []", buf.String())
	}
}

func TestWriteServiceStructured_ActionResultWarning(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	res := service.ActionResult{
		Action:  "install",
		OK:      true,
		Warning: `Ignoring account-specific saved mountpoint "/x" (no %i placeholder); using /home/user/OneDrive/%i`,
	}

	if err := writeServiceStructured(cmd, "json", res); err != nil {
		t.Fatalf("writeServiceStructured: %v", err)
	}

	var decoded service.ActionResult
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("structured output is not valid JSON: %v\n%s", err, buf.String())
	}
	if decoded.Warning != res.Warning {
		t.Errorf("warning = %q, want %q", decoded.Warning, res.Warning)
	}
}
