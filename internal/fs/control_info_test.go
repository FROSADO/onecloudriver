package fs

import (
	"testing"
	"time"
)

func TestControlInfoProvider_Info(t *testing.T) {
	cfg := MountConfig{
		CacheDir:           "/tmp/cache/account@x.com",
		CacheTTL:           60 * time.Second,
		CacheMaxEntries:    2000,
		CacheMaxSize:       1048576,
		DeltaInterval:      5 * time.Minute,
		MaxUploadsInFlight: 5,
		MaxUploadRetries:   5,
		GraphRetries:       3,
		HTTPTimeout:        15 * time.Second,
		PreWarmDepth:       2,
		DebugAddr:          "",
		ControlSocket:      "/tmp/cache/account@x.com/control.sock",
	}

	p := &controlInfoProvider{
		accountName: "account@x.com",
		mountpoint:  "/home/user/OneDrive/account@x.com",
		config:      cfg,
	}

	info := p.Info()

	if info.API.Name != "onecloudriver-control" || info.API.Version != 1 {
		t.Errorf("unexpected API info: %+v", info.API)
	}
	if info.Account.Name != "account@x.com" {
		t.Errorf("unexpected account: %q", info.Account.Name)
	}
	if info.Mount.State != "running" {
		t.Errorf("expected state running, got %q", info.Mount.State)
	}
	if info.Mount.Mountpoint != "/home/user/OneDrive/account@x.com" {
		t.Errorf("unexpected mountpoint: %q", info.Mount.Mountpoint)
	}
	if info.Mount.CacheDir != cfg.CacheDir {
		t.Errorf("unexpected cache dir: %q", info.Mount.CacheDir)
	}
	if info.Config.CacheTTL != "1m0s" || info.Config.DeltaInterval != "5m0s" || info.Config.HTTPTimeout != "15s" {
		t.Errorf("unexpected duration fields: %+v", info.Config)
	}
	if info.Config.CacheMaxEntries != 2000 || info.Config.MaxUploadsInFlight != 5 {
		t.Errorf("unexpected numeric fields: %+v", info.Config)
	}
	if info.Config.CacheMaxSize != "1.0 MB" {
		t.Errorf("expected humanized CacheMaxSize, got %q", info.Config.CacheMaxSize)
	}
}

func TestFormatCacheSize(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0"},
		{-1, "0"},
		{1024, "1.0 kB"},
		{1048576, "1.0 MB"},
	}
	for _, tt := range tests {
		if got := formatCacheSize(tt.input); got != tt.want {
			t.Errorf("formatCacheSize(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
