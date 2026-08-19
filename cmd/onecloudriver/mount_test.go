package main

import (
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/auth"
	"github.com/frosado/onecloudriver/internal/fs"
)

// buildPersistedMountConfig must NOT write CacheDir back to the account
// config when --cache-dir was given: it is a session-only override, and a
// temporary path (e.g. /tmp/...) must not poison future mounts (issue #85).
func TestBuildPersistedMountConfig_CacheDirFlagNotPersisted(t *testing.T) {
	persisted := auth.AccountPersistedConfig{
		DefaultMountpoint: "/home/me/OneDrive",
		CacheDir:          "/home/me/.cache/onecloudriver/me@example.com",
		CacheTTL:          60 * time.Second,
	}
	config := fs.MountConfig{
		CacheDir:           "/tmp/session-cache",
		CacheTTL:           5 * time.Minute,
		CacheMaxEntries:    500,
		CacheMaxSize:       1 << 30,
		DeltaInterval:      2 * time.Minute,
		MaxUploadsInFlight: 3,
		MaxUploadRetries:   7,
		GraphRetries:       4,
		HTTPTimeout:        20 * time.Second,
	}

	got := buildPersistedMountConfig(persisted, "/new/mountpoint", config, "/tmp/session-cache")

	// The flag value must not be persisted: the previous configured value stays.
	if got.CacheDir != persisted.CacheDir {
		t.Errorf("CacheDir persisted despite --cache-dir flag: got %q, want %q (unchanged)",
			got.CacheDir, persisted.CacheDir)
	}
	// All other fields are safe scalars and must be saved.
	if got.DefaultMountpoint != "/new/mountpoint" {
		t.Errorf("DefaultMountpoint: got %q, want %q", got.DefaultMountpoint, "/new/mountpoint")
	}
	if got.CacheTTL != config.CacheTTL {
		t.Errorf("CacheTTL: got %v, want %v", got.CacheTTL, config.CacheTTL)
	}
	if got.CacheMaxEntries != config.CacheMaxEntries {
		t.Errorf("CacheMaxEntries: got %d, want %d", got.CacheMaxEntries, config.CacheMaxEntries)
	}
	if got.CacheMaxSize != config.CacheMaxSize {
		t.Errorf("CacheMaxSize: got %d, want %d", got.CacheMaxSize, config.CacheMaxSize)
	}
	if got.DeltaInterval != config.DeltaInterval {
		t.Errorf("DeltaInterval: got %v, want %v", got.DeltaInterval, config.DeltaInterval)
	}
	if got.MaxUploadsInFlight != config.MaxUploadsInFlight {
		t.Errorf("MaxUploadsInFlight: got %d, want %d", got.MaxUploadsInFlight, config.MaxUploadsInFlight)
	}
	if got.MaxUploadRetries != config.MaxUploadRetries {
		t.Errorf("MaxUploadRetries: got %d, want %d", got.MaxUploadRetries, config.MaxUploadRetries)
	}
	if got.GraphRetries != config.GraphRetries {
		t.Errorf("GraphRetries: got %d, want %d", got.GraphRetries, config.GraphRetries)
	}
	if got.HTTPTimeout != config.HTTPTimeout {
		t.Errorf("HTTPTimeout: got %v, want %v", got.HTTPTimeout, config.HTTPTimeout)
	}
}

// Without --cache-dir the (possibly flag-overridden) CacheDir is persisted
// as before, keeping the existing persistence semantics for the default path.
func TestBuildPersistedMountConfig_NoFlagPersistsCacheDir(t *testing.T) {
	persisted := auth.AccountPersistedConfig{CacheDir: "/old/cache"}
	config := fs.MountConfig{CacheDir: "/home/me/.cache/onecloudriver/me@example.com"}

	got := buildPersistedMountConfig(persisted, "/mp", config, "")

	if got.CacheDir != config.CacheDir {
		t.Errorf("CacheDir: got %q, want %q (persisted when no flag is given)",
			got.CacheDir, config.CacheDir)
	}
}

// An empty persisted CacheDir stays empty when --cache-dir is given (it must
// not be populated with the session-only path).
func TestBuildPersistedMountConfig_CacheDirFlagWithEmptyPersisted(t *testing.T) {
	persisted := auth.AccountPersistedConfig{}
	config := fs.MountConfig{CacheDir: "/tmp/session-cache"}

	got := buildPersistedMountConfig(persisted, "/mp", config, "/tmp/session-cache")

	if got.CacheDir != "" {
		t.Errorf("CacheDir: got %q, want \"\" (never populated from a session-only flag)", got.CacheDir)
	}
}

// TestBuildPersistedMountConfig_PreWarmDepth verifies that PreWarmDepth is persisted.
func TestBuildPersistedMountConfig_PreWarmDepth(t *testing.T) {
	persisted := auth.AccountPersistedConfig{
		PreWarmDepth: 2, // Previous setting
	}
	config := fs.MountConfig{
		PreWarmDepth: 5, // New setting from flag
	}

	got := buildPersistedMountConfig(persisted, "/mp", config, "")

	if got.PreWarmDepth != config.PreWarmDepth {
		t.Errorf("PreWarmDepth: got %d, want %d (persisted from config)", got.PreWarmDepth, config.PreWarmDepth)
	}
}

// TestBuildPersistedMountConfig_PreWarmDepthDefault verifies that default PreWarmDepth is handled.
func TestBuildPersistedMountConfig_PreWarmDepthDefault(t *testing.T) {
	persisted := auth.AccountPersistedConfig{
		PreWarmDepth: 0, // Not set
	}
	config := fs.MountConfig{
		PreWarmDepth: 2, // Default
	}

	got := buildPersistedMountConfig(persisted, "/mp", config, "")

	if got.PreWarmDepth != config.PreWarmDepth {
		t.Errorf("PreWarmDepth: got %d, want %d (default)", got.PreWarmDepth, config.PreWarmDepth)
	}
}
