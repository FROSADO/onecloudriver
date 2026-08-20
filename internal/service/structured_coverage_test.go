package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallServiceResult_CreatesUnitAndReloadsDaemon(t *testing.T) {
	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "calls.log")
	writeScript(t, filepath.Join(binDir, "onecloudriver"), "#!/bin/sh\nexit 0\n")
	writeScript(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+logPath+"\"\nexit 0\n")

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	mountTemplate := filepath.Join(os.Getenv("HOME"), "OneDrive", "%i")
	result, err := InstallServiceResult(mountTemplate, "user@example.com")
	if err != nil {
		t.Fatalf("InstallServiceResult: %v", err)
	}
	if !result.OK || result.Action != "install" {
		t.Fatalf("result = %#v, want successful install", result)
	}
	if result.ServiceFile == "" || result.Mountpoint != mountTemplate {
		t.Errorf("result = %#v, want service file and original mountpoint", result)
	}

	unit, err := os.ReadFile(result.ServiceFile)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", result.ServiceFile, err)
	}
	if !strings.Contains(string(unit), "ExecStart=") || !strings.Contains(string(unit), "-a %i") {
		t.Errorf("generated unit does not contain the expected instance command:\n%s", unit)
	}
	mountpoint := filepath.Join(os.Getenv("HOME"), "OneDrive", "user@example.com")
	if _, err := os.Stat(mountpoint); err != nil {
		t.Fatalf("mountpoint %q was not created: %v", mountpoint, err)
	}
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile calls: %v", err)
	}
	if !strings.Contains(string(calls), "--user daemon-reload") {
		t.Errorf("systemctl calls = %q, want daemon-reload", calls)
	}
}

func TestEnableUnitQuiet(t *testing.T) {
	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "calls.log")
	writeScript(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+logPath+"\"\nexit 0\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := EnableUnitQuiet("user@example.com"); err != nil {
		t.Fatalf("EnableUnitQuiet: %v", err)
	}
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile calls: %v", err)
	}
	if !strings.Contains(string(calls), "--user enable --now onecloudriver@user@example.com.service") {
		t.Errorf("systemctl calls = %q", calls)
	}
}

func TestEnableUnitQuiet_ReturnsError(t *testing.T) {
	binDir := t.TempDir()
	writeScript(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\nprintf 'enable failed' >&2\nexit 1\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := EnableUnitQuiet("user@example.com"); err == nil || !strings.Contains(err.Error(), "onecloudriver@user@example.com.service") {
		t.Fatalf("EnableUnitQuiet error = %v, want unit context", err)
	}
}

func TestGetUnitStatusAndJournalTail(t *testing.T) {
	binDir := t.TempDir()
	writeScript(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\nprintf 'ActiveState=active\\nSubState=running\\nMainPID=42\\nExecStart={ argv[]=/usr/bin/onecloudriver mount /srv/OneDrive/%i -a user@example.com ; }\\nUnitFileState=enabled\\n'\n")
	writeScript(t, filepath.Join(binDir, "journalctl"), "#!/bin/sh\nprintf 'journal line\\n'\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	status, err := GetUnitStatus("user@example.com")
	if err != nil {
		t.Fatalf("GetUnitStatus: %v", err)
	}
	if status.State != "running" || status.PID != 42 {
		t.Errorf("status = %#v, want running/42", status)
	}
	lines, err := JournalTail("onecloudriver@user@example.com.service", 1)
	if err != nil {
		t.Fatalf("JournalTail: %v", err)
	}
	if len(lines) != 1 || lines[0] != "journal line" {
		t.Errorf("JournalTail = %#v, want one journal line", lines)
	}
}

func TestListInstancesPublicWrapper(t *testing.T) {
	binDir := t.TempDir()
	writeScript(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\ncase \"$*\" in\n  *list-units*) printf 'onecloudriver@user@example.com.service loaded active running OneCloudRiver\\n' ;;\n  *) printf 'ActiveState=active\\nSubState=running\\nMainPID=9\\nExecStart={ argv[]=/usr/bin/onecloudriver mount /srv/OneDrive/user -a user@example.com ; }\\nUnitFileState=enabled\\n' ;;\nesac\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	instances, err := ListInstances()
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(instances) != 1 || instances[0].Account != "user@example.com" {
		t.Errorf("instances = %#v, want one user@example.com instance", instances)
	}
}

func TestUninstallServiceResult_DiscoveryPath(t *testing.T) {
	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "calls.log")
	writeScript(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+logPath+"\"\ncase \"$*\" in\n  *list-units*) printf 'onecloudriver@user@example.com.service loaded active running OneCloudRiver\\n' ;;\nesac\nexit 0\n")
	writeScript(t, filepath.Join(binDir, "mount"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	path, err := ServiceFilePath()
	if err != nil {
		t.Fatalf("ServiceFilePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("unit"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := UninstallServiceResult()
	if err != nil {
		t.Fatalf("UninstallServiceResult: %v", err)
	}
	if !result.OK || len(result.AffectedAccounts) != 1 || result.AffectedAccounts[0] != "user@example.com" {
		t.Errorf("result = %#v, want discovered account", result)
	}
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile calls: %v", err)
	}
	for _, want := range []string{"--user list-units", "--user stop onecloudriver@user@example.com.service", "--user disable onecloudriver@user@example.com.service", "--user daemon-reload"} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("systemctl calls = %q, missing %q", calls, want)
		}
	}
}

func TestUninstallServiceResult_DiscoveryListErrorIsNonFatal(t *testing.T) {
	binDir := t.TempDir()
	writeScript(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\nprintf 'list failed' >&2\nexit 1\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	result, err := UninstallServiceResult()
	if err != nil {
		t.Fatalf("UninstallServiceResult: %v", err)
	}
	if !result.OK || result.Message != "The service was not installed." {
		t.Errorf("result = %#v, want successful not-installed result", result)
	}
}

func TestStopServiceResult_ReturnsSystemctlError(t *testing.T) {
	binDir := t.TempDir()
	writeScript(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\nexit 1\n")
	writeScript(t, filepath.Join(binDir, "mount"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())

	result, err := StopServiceResult("user@example.com")
	if err == nil {
		t.Fatal("StopServiceResult should return the systemctl error")
	}
	if result.OK || result.Error == "" {
		t.Errorf("result = %#v, want failed result with error", result)
	}
}

func TestRunSystemctlQuiet_CapturesStderr(t *testing.T) {
	binDir := t.TempDir()
	writeScript(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\nprintf 'permission denied' >&2\nexit 1\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := runSystemctlQuiet("start", "onecloudriver@user@example.com.service")
	if err == nil || !strings.Contains(err.Error(), "start") {
		t.Fatalf("runSystemctlQuiet error = %v, want action context", err)
	}
}

func TestEnsureMountpointDirQuiet_RejectsFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	filePath := filepath.Join(home, "occupied")
	if err := os.WriteFile(filePath, []byte("not a directory"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := ensureMountpointDirQuiet(filepath.Join(filePath, "child"), "account"); err == nil {
		t.Fatal("ensureMountpointDirQuiet should reject an existing file")
	}
}

func TestIsNoInstalledUnitsMessage(t *testing.T) {
	for _, message := range []string{"No files found.", "No units found.", "No units matching pattern.", "0 units listed."} {
		if !isNoInstalledUnitsMessage(message) {
			t.Errorf("isNoInstalledUnitsMessage(%q) = false", message)
		}
	}
	if isNoInstalledUnitsMessage("permission denied") {
		t.Error("permission denied should not be treated as an empty result")
	}
}
