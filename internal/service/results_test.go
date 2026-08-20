package service

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestStartServiceResult(t *testing.T) {
	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "calls.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + logPath + "\"\nexit 0\n"
	writeScript(t, filepath.Join(binDir, "systemctl"), script)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	res, err := StartServiceResult("user@outlook.com")
	if err != nil {
		t.Fatalf("StartServiceResult: %v", err)
	}
	if !res.OK || res.Action != "start" || res.Account != "user@outlook.com" {
		t.Errorf("result = %#v, want ok start for user@outlook.com", res)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if want := "--user start onecloudriver@user@outlook.com.service"; !strings.Contains(string(data), want) {
		t.Errorf("expected systemctl call %q, got:\n%s", want, data)
	}
}

func TestStartServiceResult_Error(t *testing.T) {
	binDir := t.TempDir()
	writeScript(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\nexit 1\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	res, err := StartServiceResult("user@outlook.com")
	if err == nil {
		t.Fatal("expected error from failing systemctl")
	}
	if res.OK {
		t.Error("expected OK=false on failure")
	}
	if res.Error == "" {
		t.Error("expected Error field to be populated on failure")
	}
}

func TestStopServiceResult(t *testing.T) {
	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "calls.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + logPath + "\"\nexit 0\n"
	writeScript(t, filepath.Join(binDir, "systemctl"), script)
	writeScript(t, filepath.Join(binDir, "mount"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())

	res, err := StopServiceResult("user@outlook.com")
	if err != nil {
		t.Fatalf("StopServiceResult: %v", err)
	}
	if !res.OK || res.Action != "stop" || res.Account != "user@outlook.com" {
		t.Errorf("result = %#v, want ok stop for user@outlook.com", res)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if want := "--user stop onecloudriver@user@outlook.com.service"; !strings.Contains(string(data), want) {
		t.Errorf("expected systemctl call %q, got:\n%s", want, data)
	}
}

func TestUninstallServiceResult(t *testing.T) {
	binDir := t.TempDir()
	writeScript(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\nexit 0\n")
	writeScript(t, filepath.Join(binDir, "mount"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	path, err := ServiceFilePath()
	if err != nil {
		t.Fatalf("ServiceFilePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("[Unit]\n"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	res, err := UninstallServiceResult("acc2", "acc1")
	if err != nil {
		t.Fatalf("UninstallServiceResult: %v", err)
	}
	if !res.OK || res.Action != "uninstall" {
		t.Errorf("result = %#v, want ok uninstall", res)
	}
	if res.ServiceFile != path {
		t.Errorf("ServiceFile = %q, want %q", res.ServiceFile, path)
	}
	if want := []string{"acc1", "acc2"}; !reflect.DeepEqual(res.AffectedAccounts, want) {
		t.Errorf("AffectedAccounts = %#v, want sorted %#v", res.AffectedAccounts, want)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected service file to be removed, stat err=%v", err)
	}
}

func TestUninstallServiceResult_NotInstalled(t *testing.T) {
	binDir := t.TempDir()
	writeScript(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\nexit 0\n")
	writeScript(t, filepath.Join(binDir, "mount"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	res, err := UninstallServiceResult("acc1")
	if err != nil {
		t.Fatalf("UninstallServiceResult: %v", err)
	}
	if !res.OK {
		t.Errorf("result.OK = false, want true for a successful no-op")
	}
	if res.Message != "The service was not installed." {
		t.Errorf("Message = %q, want not-installed message", res.Message)
	}
}

func TestQueryUnitStatus_RejectsEmptyAccount(t *testing.T) {
	if _, _, err := QueryUnitStatus("  "); err == nil {
		t.Fatal("QueryUnitStatus should reject an empty account")
	}
}
