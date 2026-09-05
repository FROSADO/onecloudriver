package control

// APIInfo identifies the control API name and version.
type APIInfo struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

// DaemonInfo contains runtime information about the mount process.
type DaemonInfo struct {
	PID           int    `json:"pid"`
	StartedAt     string `json:"startedAt"`
	GoVersion     string `json:"goVersion"`
	BinaryVersion string `json:"binaryVersion,omitempty"`
}

// AccountInfo contains account-related information.
type AccountInfo struct {
	Name string `json:"name"`
}

// MountInfo contains information about the mounted filesystem.
type MountInfo struct {
	State      string `json:"state"`
	Mountpoint string `json:"mountpoint"`
	CacheDir   string `json:"cacheDir"`
	ConfigFile string `json:"configFile,omitempty"`
}

// ConfigInfo contains the effective configuration of the mount.
// Durations are serialized as strings (e.g. "60s") for JSON stability.
type ConfigInfo struct {
	CacheTTL           string `json:"cacheTTL"`
	CacheMaxEntries    int    `json:"cacheMaxEntries"`
	CacheMaxSize       string `json:"cacheMaxSize"`
	DeltaInterval      string `json:"deltaInterval"`
	MaxUploadsInFlight int    `json:"maxUploadsInFlight"`
	MaxUploadRetries   int    `json:"maxUploadRetries"`
	GraphRetries       int    `json:"graphRetries"`
	HTTPTimeout        string `json:"httpTimeout"`
	PreWarmDepth       int    `json:"preWarmDepth"`
	DebugAddr          string `json:"debugAddr"`
}

// Info is the response payload for GET /v1/info.
type Info struct {
	API     APIInfo     `json:"api"`
	Daemon  DaemonInfo  `json:"daemon"`
	Account AccountInfo `json:"account"`
	Mount   MountInfo   `json:"mount"`
	Config  ConfigInfo  `json:"config"`
}

// InfoProvider is the interface that the mount process implements to provide
// state to the control server. This allows the control package to remain
// decoupled from internal/fs.
type InfoProvider interface {
	// Info returns the current state of the mount process.
	Info() Info
}
