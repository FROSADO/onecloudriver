package service

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallService_WritesUnitAndCreatesMountpoint(t *testing.T) {
	binDir := t.TempDir()
	writeScript(t, filepath.Join(binDir, "onecloudriver"), "#!/bin/sh\nexit 0\n")
	writeScript(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	mountTemplate := "%h/OneDrive/%i"
	if err := InstallService(mountTemplate, "user@example.com"); err != nil {
		t.Fatalf("InstallService: %v", err)
	}

	servicePath, err := ServiceFilePath()
	if err != nil {
		t.Fatalf("ServiceFilePath: %v", err)
	}
	unit, err := os.ReadFile(servicePath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", servicePath, err)
	}
	if !strings.Contains(string(unit), "ExecStart="+filepath.Join(binDir, "onecloudriver")+" mount %h/OneDrive/%i -a %i") {
		t.Errorf("unit does not contain resolved binary and mountpoint:\n%s", unit)
	}
	info, err := os.Stat(servicePath)
	if err != nil {
		t.Fatalf("Stat(%q): %v", servicePath, err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("service file permissions = %o, want 600", got)
	}
	mountpoint := filepath.Join(os.Getenv("HOME"), "OneDrive", "user@example.com")
	if info, err := os.Stat(mountpoint); err != nil || !info.IsDir() {
		t.Fatalf("mountpoint %q was not created: %v", mountpoint, err)
	}
}

func TestInstallService_ReturnsDaemonReloadError(t *testing.T) {
	binDir := t.TempDir()
	writeScript(t, filepath.Join(binDir, "onecloudriver"), "#!/bin/sh\nexit 0\n")
	writeScript(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\nexit 1\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	err := InstallService("%h/OneDrive/%i", "user@example.com")
	if err == nil || !strings.Contains(err.Error(), "error reloading systemd daemon") {
		t.Fatalf("error = %v, want daemon reload failure", err)
	}
}

func TestEnableUnit_UsesExpectedSystemctlCommand(t *testing.T) {
	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "calls.log")
	writeScript(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\nprintf '%s\\n' \"$*\" > \""+logPath+"\"\nexit 0\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	EnableUnit("user@example.com")
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile calls: %v", err)
	}
	if got := strings.TrimSpace(string(calls)); got != "--user enable --now onecloudriver@user@example.com.service" {
		t.Errorf("systemctl call = %q", got)
	}
}

func TestUnmountMountpoint_NotMounted(t *testing.T) {
	binDir := t.TempDir()
	marker := filepath.Join(binDir, "fusermount-called")
	writeScript(t, filepath.Join(binDir, "mount"), "#!/bin/sh\nprintf '%s\\n' other-mount\n")
	writeScript(t, filepath.Join(binDir, "fusermount3"), "#!/bin/sh\ntouch \""+marker+"\"\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())

	UnmountMountpoint("user@example.com")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("fusermount3 was called for an unmounted path")
	}
}

func TestUnmountMountpoint_FallsBackToLegacyFusermount(t *testing.T) {
	binDir := t.TempDir()
	legacyMarker := filepath.Join(binDir, "legacy-called")
	mountpoint := filepath.Join(t.TempDir(), "OneDrive", "user@example.com")
	writeScript(t, filepath.Join(binDir, "mount"), "#!/bin/sh\nprintf '%s\\n' '"+mountpoint+" type fuse'\n")
	writeScript(t, filepath.Join(binDir, "fusermount3"), "#!/bin/sh\nexit 1\n")
	writeScript(t, filepath.Join(binDir, "fusermount"), "#!/bin/sh\ntouch \""+legacyMarker+"\"\nexit 0\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", filepath.Dir(filepath.Dir(mountpoint)))

	UnmountMountpoint("user@example.com")
	if _, err := os.Stat(legacyMarker); err != nil {
		t.Fatalf("legacy fusermount was not called: %v", err)
	}
}

func TestStatus_SingleUnitAndListing(t *testing.T) {
	t.Run("single unit", func(t *testing.T) {
		binDir := t.TempDir()
		writeScript(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\nprintf '%s\\n' 'ActiveState=active' 'SubState=running' 'MainPID=42' 'ExecStart={ argv[]=/usr/bin/onecloudriver mount /srv/OneDrive/user@example.com -a user@example.com ; }' 'UnitFileState=enabled'\n")
		writeScript(t, filepath.Join(binDir, "journalctl"), "#!/bin/sh\nprintf '%s\\n' journal-line\n")
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		output := captureServiceStdout(t, func() error {
			return Status([]string{"user@example.com"})
		})
		for _, want := range []string{"State:    running (active)", "Account:   user@example.com", "PID:      42"} {
			if !strings.Contains(output, want) {
				t.Errorf("single-unit output missing %q:\n%s", want, output)
			}
		}
	})

	t.Run("listing", func(t *testing.T) {
		binDir := t.TempDir()
		writeScript(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\nprintf '%s\\n' 'onecloudriver@user@example.com.service loaded active running OneCloudRiver'\n")
		writeScript(t, filepath.Join(binDir, "journalctl"), "#!/bin/sh\nprintf '%s\\n' journal-line\n")
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())

		output := captureServiceStdout(t, func() error {
			return Status(nil)
		})
		for _, want := range []string{"Active onecloudriver instances:", "onecloudriver@user@example.com.service", "Service not installed"} {
			if !strings.Contains(output, want) {
				t.Errorf("listing output missing %q:\n%s", want, output)
			}
		}
	})
}

func captureServiceStdout(t *testing.T, fn func() error) string {
	t.Helper()
	old := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = writer
	defer func() { os.Stdout = old }()

	if err := fn(); err != nil {
		_ = writer.Close()
		_ = reader.Close()
		t.Fatalf("function: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("io.ReadAll: %v", err)
	}
	_ = reader.Close()
	return string(output)
}
