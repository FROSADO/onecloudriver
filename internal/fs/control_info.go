package fs

import (
	"github.com/dustin/go-humanize"
	"github.com/frosado/onecloudriver/internal/control"
)

// controlInfoProvider adapts the live mount state to the control package's
// InfoProvider. The runtime fields the control server owns (pid, startedAt,
// goVersion) are left empty here and filled in by the server.
type controlInfoProvider struct {
	accountName string
	mountpoint  string
	config      MountConfig
}

// Info returns a snapshot of the mount process state.
func (p *controlInfoProvider) Info() control.Info {
	cfg := p.config
	return control.Info{
		API: control.APIInfo{Name: "onecloudriver-control", Version: 1},
		Account: control.AccountInfo{
			Name: p.accountName,
		},
		Mount: control.MountInfo{
			State:      "running",
			Mountpoint: p.mountpoint,
			CacheDir:   cfg.CacheDir,
		},
		Config: control.ConfigInfo{
			CacheTTL:           cfg.CacheTTL.String(),
			CacheMaxEntries:    cfg.CacheMaxEntries,
			CacheMaxSize:       formatCacheSize(cfg.CacheMaxSize),
			DeltaInterval:      cfg.DeltaInterval.String(),
			MaxUploadsInFlight: cfg.MaxUploadsInFlight,
			MaxUploadRetries:   cfg.MaxUploadRetries,
			GraphRetries:       cfg.GraphRetries,
			HTTPTimeout:        cfg.HTTPTimeout.String(),
			PreWarmDepth:       cfg.PreWarmDepth,
			DebugAddr:          cfg.DebugAddr,
		},
	}
}

// formatCacheSize renders a content-cache byte limit as a human-readable size.
// 0 means unlimited and is reported as "0", mirroring the CLI conventions.
func formatCacheSize(size int64) string {
	if size <= 0 {
		return "0"
	}
	return humanize.Bytes(uint64(size)) //#nosec G115 -- size is > 0 after the guard above, so the int64→uint64 conversion cannot overflow
}
