package main

import (
	"fmt"

	"github.com/dustin/go-humanize"
	"github.com/frosado/onecloudriver/internal/auth"
	"github.com/frosado/onecloudriver/internal/fs"
	"github.com/frosado/onecloudriver/internal/printer"
	"github.com/spf13/cobra"
)

var mountCmd = &cobra.Command{
	Use:   "mount [mountpoint]",
	Short: "Mount OneDrive at the specified directory",
	Long: `Mounts OneDrive as a FUSE filesystem at the specified directory.

If no mountpoint is specified and the account has a saved defaultMountpoint
(from the last successful mount), it is reused automatically.

Configuration flags override the values persisted in the account JSON.
New values are automatically saved for the next session, except
--cache-dir, which is a session-only override and is never persisted.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := getManager(cmd)
		if err != nil {
			return err
		}
		var mountPoint string
		// 1. Get the account from the manager
		acc, err := resolveAccount(cmd, manager)
		if err != nil {
			return err
		}

		// 2. Determine the mountpoint:
		//    a) Positional argument (explicit)
		//    b) DefaultMountpoint from account JSON (last successful mount)
		//    c) ./<account> (fallback)
		if len(args) >= 1 {
			mountPoint = args[0]
		} else if acc.Mount.DefaultMountpoint != "" {
			mountPoint = acc.Mount.DefaultMountpoint
			fmt.Printf("Using saved mountpoint: %s\n", mountPoint)
		} else {
			mountPoint = fmt.Sprintf("./%s", acc.Name)
			fmt.Printf("No mountpoint specified. Using '%s'\n", mountPoint)
		}

		// 3. Verify we can get a token
		_, err = acc.GetAccessToken(cmd.Context())
		if err != nil {
			return fmt.Errorf("could not obtain token for %s: %w", acc.Name, err)
		}

		// 4. Build configuration from persisted values + CLI flags
		config := fs.DefaultMountConfig(acc.Name, &acc.Mount)

		// --cache-dir is a session-only override: it is used for this mount
		// but NEVER persisted, so a temporary path (e.g. /tmp/...) cannot
		// poison the account config for future mounts (issue #85).
		cacheDirFromFlag, _ := cmd.Flags().GetString("cache-dir")
		if cacheDirFromFlag != "" {
			config.CacheDir = cacheDirFromFlag
		}
		if cacheTTL, _ := cmd.Flags().GetDuration("cache-ttl"); cacheTTL > 0 {
			config.CacheTTL = cacheTTL
		}
		if cacheMaxEntries, _ := cmd.Flags().GetInt("cache-max-entries"); cacheMaxEntries > 0 {
			config.CacheMaxEntries = cacheMaxEntries
		}
		if cacheMaxSizeStr, _ := cmd.Flags().GetString("cache-max-size"); cacheMaxSizeStr != "" && cacheMaxSizeStr != "0" {
			maxSize, err := humanize.ParseBytes(cacheMaxSizeStr)
			if err != nil {
				return fmt.Errorf("invalid value for --cache-max-size: %w", err)
			}
			config.CacheMaxSize = int64(maxSize)
		}

		// Advanced flags
		if deltaInterval, _ := cmd.Flags().GetDuration("delta-interval"); deltaInterval > 0 {
			config.DeltaInterval = deltaInterval
		}
		if maxUploads, _ := cmd.Flags().GetInt("max-uploads"); maxUploads > 0 {
			config.MaxUploadsInFlight = maxUploads
		}
		if maxRetries, _ := cmd.Flags().GetInt("upload-retries"); maxRetries > 0 {
			config.MaxUploadRetries = maxRetries
		}
		if graphRetries, _ := cmd.Flags().GetInt("graph-retries"); graphRetries > 0 {
			config.GraphRetries = graphRetries
		}
		if httpTimeout, _ := cmd.Flags().GetDuration("http-timeout"); httpTimeout > 0 {
			config.HTTPTimeout = httpTimeout
		}
		if err := applyPreWarmDepthFlag(cmd, &config); err != nil {
			return err
		}
		if debug, _ := cmd.Flags().GetBool("debug"); debug {
			config.DebugAddr, _ = cmd.Flags().GetString("debug-addr")
			// Diagnosing requires the verbose levels; raising them here (rather
			// than forcing --log-level) keeps the default Info for everyone else.
			_ = auth.SetLogLevel("debug")
		} else {
			config.DebugAddr = ""
		}

		fmt.Printf("%s Starting mount of '%s' at '%s'...\n", printer.Rocket, acc.Name, mountPoint)
		if cacheDirFromFlag != "" {
			fmt.Printf("   %s Cache: %s (session only, not saved to account config)\n", printer.Folder, config.CacheDir)
		} else {
			fmt.Printf("   %s Cache: %s\n", printer.Folder, config.CacheDir)
		}
		fmt.Printf("   %s Metadata TTL: %v\n", printer.Clock, config.CacheTTL)
		fmt.Printf("   %s Delta: %v\n", printer.Refresh, config.DeltaInterval)
		if config.CacheMaxSize > 0 {
			fmt.Printf("   %s Content limit: %s\n", printer.Disk, humanize.Bytes(uint64(config.CacheMaxSize)))
		}

		// 5. Save configuration to account JSON for the next session.
		// --cache-dir is excluded (session-only override, see step 4).
		acc.Lock()
		acc.Mount = buildPersistedMountConfig(acc.Mount, mountPoint, config, cacheDirFromFlag)
		acc.Unlock()

		if err := acc.Save(); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s Could not save configuration: %v\n", printer.Warning, err)
		}

		// 6. FUSE call! (This function is blocking)
		handles, err := fs.Mount(mountPoint, acc, config)
		if err != nil {
			return fmt.Errorf("error mounting FUSE: %w", err)
		}
		defer handles.Metadata.Close()

		return nil
	},
}

// buildPersistedMountConfig computes the account config to save after a
// mount. cacheDirFromFlag is the --cache-dir flag value ("" if not given):
// it is a session-only override and is never persisted, so a temporary path
// cannot overwrite the configured cache directory for future mounts
// (issue #85). All other flag overrides are safe scalars and are saved.
func buildPersistedMountConfig(persisted auth.AccountPersistedConfig, mountPoint string, config fs.MountConfig, cacheDirFromFlag string) auth.AccountPersistedConfig {
	persisted.DefaultMountpoint = mountPoint
	if cacheDirFromFlag == "" {
		persisted.CacheDir = config.CacheDir
	}
	persisted.CacheTTL = config.CacheTTL
	persisted.CacheMaxEntries = config.CacheMaxEntries
	persisted.CacheMaxSize = config.CacheMaxSize
	persisted.DeltaInterval = config.DeltaInterval
	persisted.MaxUploadsInFlight = config.MaxUploadsInFlight
	persisted.MaxUploadRetries = config.MaxUploadRetries
	persisted.GraphRetries = config.GraphRetries
	persisted.HTTPTimeout = config.HTTPTimeout
	persisted.PreWarmDepth = &config.PreWarmDepth
	return persisted
}

// applyPreWarmDepthFlag applies the --pre-warm-depth flag to cfg when it was
// explicitly set. Using Changed() (rather than testing the raw value) is what
// allows an explicit 0 to disable pre-warming, even though 0 is also the flag's
// default. Returns an error for values outside [0, 10].
func applyPreWarmDepthFlag(cmd *cobra.Command, cfg *fs.MountConfig) error {
	if !cmd.Flags().Changed("pre-warm-depth") {
		return nil
	}
	depth, err := cmd.Flags().GetInt("pre-warm-depth")
	if err != nil {
		return err
	}
	if depth < 0 || depth > 10 {
		return fmt.Errorf("--pre-warm-depth must be in range [0, 10], got %d", depth)
	}
	cfg.PreWarmDepth = depth
	return nil
}

// registerMountCmd adds the mount command's flags and registers it in root.
func registerMountCmd(root *cobra.Command) {
	mountCmd.Flags().StringP("account", "a", "", "Account name to mount (e.g.: user@outlook.com)")

	// ──── Basic ────
	mountCmd.Flags().String("cache-dir", "", "Root cache directory for THIS mount only; not saved to the account config (default: ~/.cache/onecloudriver/<account>)")
	mountCmd.Flags().Duration("cache-ttl", 0, "Base TTL for cached metadata (e.g.: 60s, 5m). 0 = use persisted or default")
	mountCmd.Flags().Int("cache-max-entries", 0, "Max folders with cached children in memory. 0 = use persisted or default")
	mountCmd.Flags().String("cache-max-size", "0", "Max ContentCache size on disk (e.g.: 1GB, 500MB). 0 = unlimited")

	// ──── Advanced ────
	mountCmd.Flags().Duration("delta-interval", 0, "Delta polling interval (e.g.: 5m, 60s). 0 = use persisted or default")
	mountCmd.Flags().Int("max-uploads", 0, "Max concurrent uploads (default: 5). 0 = use persisted or default")
	mountCmd.Flags().Int("upload-retries", 0, "Max retries per upload (default: 5). 0 = use persisted or default")
	mountCmd.Flags().Int("graph-retries", 0, "HTTP retries on 429/503 (default: 3). 0 = use persisted or default")
	mountCmd.Flags().Duration("http-timeout", 0, "HTTP request timeout to Graph (default: 15s). 0 = use persisted or default")
	mountCmd.Flags().Int("pre-warm-depth", 0, "Metadata pre-warm depth after mount (0=off, 1=root, 2=root+1, up to 10; default 2)")

	// ──── Debug / observability ────
	mountCmd.Flags().Bool("debug", false, "Start a local expvar + pprof debug server (default 127.0.0.1:6060) and raise the log level to debug")
	mountCmd.Flags().String("debug-addr", "127.0.0.1:6060", "Address for the --debug server (loopback by default; never expose on a public interface unless intended)")

	root.AddCommand(mountCmd)
}
