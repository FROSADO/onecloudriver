package service

import (
	"errors"
	"testing"
)

func TestRunningMountpoint(t *testing.T) {
	const runningOutput = "ActiveState=active\nSubState=running\nMainPID=42\nExecStart={ path=/usr/local/bin/onecloudriver ; argv[]=/usr/local/bin/onecloudriver mount /home/user/OneDrive/user@outlook.com -a user@outlook.com ; ignore_errors=no }\nUnitFileState=enabled\n"
	const stoppedOutput = "ActiveState=inactive\nSubState=dead\nMainPID=0\nUnitFileState=disabled\n"

	tests := []struct {
		name    string
		run     commandRunner
		wantMp  string
		wantRun bool
	}{
		{
			name: "running service returns its mountpoint",
			run: func(_ string, _ ...string) ([]byte, []byte, error) {
				return []byte(runningOutput), nil, nil
			},
			wantMp:  "/home/user/OneDrive/user@outlook.com",
			wantRun: true,
		},
		{
			name: "stopped service is not running",
			run: func(_ string, _ ...string) ([]byte, []byte, error) {
				return []byte(stoppedOutput), nil, nil
			},
			wantRun: false,
		},
		{
			name: "systemctl failure is not running",
			run: func(_ string, _ ...string) ([]byte, []byte, error) {
				return nil, []byte("Failed to connect"), errors.New("no systemd")
			},
			wantRun: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := systemdClient{run: tt.run}
			mp, running := client.runningMountpoint("user@outlook.com")
			if running != tt.wantRun {
				t.Errorf("running = %v, want %v", running, tt.wantRun)
			}
			if mp != tt.wantMp {
				t.Errorf("mountpoint = %q, want %q", mp, tt.wantMp)
			}
		})
	}
}
