package control

import (
	"encoding/json"
	"testing"
	"time"
)

func TestInfo_JSONSerialization(t *testing.T) {
	info := sampleInfo("/home/user/.cache/onecloudriver/user@outlook.com")
	info.Daemon = DaemonInfo{PID: 1234, StartedAt: "2026-09-05T10:00:00Z", GoVersion: "go1.26.7", BinaryVersion: "v0.1.5"}
	info.Account.Name = "user@outlook.com"
	info.Mount.Mountpoint = "/home/user/OneDrive/user@outlook.com"
	info.Mount.ConfigFile = "/home/user/.config/onecloudriver/user@outlook.com.json"

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var decoded Info
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if decoded.API.Name != info.API.Name || decoded.API.Version != info.API.Version {
		t.Errorf("API mismatch: got %+v, want %+v", decoded.API, info.API)
	}
	if decoded.Daemon.PID != info.Daemon.PID || decoded.Daemon.StartedAt != info.Daemon.StartedAt {
		t.Errorf("Daemon mismatch: got %+v, want %+v", decoded.Daemon, info.Daemon)
	}
	if decoded.Account.Name != info.Account.Name {
		t.Errorf("Account mismatch: got %q, want %q", decoded.Account.Name, info.Account.Name)
	}
	if decoded.Mount.State != info.Mount.State || decoded.Mount.Mountpoint != info.Mount.Mountpoint ||
		decoded.Mount.CacheDir != info.Mount.CacheDir || decoded.Mount.ConfigFile != info.Mount.ConfigFile {
		t.Errorf("Mount mismatch: got %+v, want %+v", decoded.Mount, info.Mount)
	}
	if decoded.Config.CacheTTL != info.Config.CacheTTL || decoded.Config.CacheMaxEntries != info.Config.CacheMaxEntries {
		t.Errorf("Config mismatch: got %+v, want %+v", decoded.Config, info.Config)
	}
}

func TestConfigInfo_DurationFieldsAreStrings(t *testing.T) {
	cfg := ConfigInfo{
		CacheTTL:      (60 * time.Second).String(),
		DeltaInterval: (5 * time.Minute).String(),
		HTTPTimeout:   (15 * time.Second).String(),
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	for _, key := range []string{"cacheTTL", "deltaInterval", "httpTimeout"} {
		if _, ok := raw[key].(string); !ok {
			t.Errorf("%s should be a JSON string, got %T", key, raw[key])
		}
	}
}
