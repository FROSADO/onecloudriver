package main

import (
	"fmt"

	"github.com/dustin/go-humanize"
	"github.com/frosado/onecloudriver/internal/fs"
	"github.com/spf13/cobra"
)

var mountCmd = &cobra.Command{
	Use:   "mount [mountpoint]",
	Short: "Mount OneDrive at the specified directory",
	Long: `Mounts OneDrive as a FUSE filesystem at the specified directory.

If no mountpoint is specified and the account has a saved defaultMountpoint
(from the last successful mount), it is reused automatically.

Configuration flags override the values persisted in the account JSON.
New values are automatically saved for the next session.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var err error
		var mountPoint string
		accountName, _ := cmd.Flags().GetString("account")
		if accountName == "" {
			accountName, err = manager.ResolveMainAccountName()
			if err != nil {
				return fmt.Errorf("you must specify an account with --account")
			}
			fmt.Printf("Using the only default account '%s'\n", accountName)
		}

		// 1. Get the account from the manager
		acc, err := manager.GetAccount(accountName)
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
			mountPoint = fmt.Sprintf("./%s", accountName)
			fmt.Printf("No mountpoint specified. Using '%s'\n", mountPoint)
		}

		// 3. Verify we can get a token
		_, err = acc.GetAccessToken(cmd.Context())
		if err != nil {
			return fmt.Errorf("could not obtain token for %s: %w", accountName, err)
		}

		// 4. Build configuration from persisted values + CLI flags
		config := fs.DefaultMountConfig(accountName, &acc.Mount)

		// Basic flags
		if cacheDir, _ := cmd.Flags().GetString("cache-dir"); cacheDir != "" {
			config.CacheDir = cacheDir
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

		fmt.Printf("🚀 Starting mount of '%s' at '%s'...\n", accountName, mountPoint)
		fmt.Printf("   📁 Cache: %s\n", config.CacheDir)
		fmt.Printf("   ⏱️  Metadata TTL: %v\n", config.CacheTTL)
		fmt.Printf("   🔄 Delta: %v\n", config.DeltaInterval)
		if config.CacheMaxSize > 0 {
			fmt.Printf("   💾 Content limit: %s\n", humanize.Bytes(uint64(config.CacheMaxSize)))
		}

		// 5. Save configuration to account JSON for the next session
		acc.Lock()
		acc.Mount.DefaultMountpoint = mountPoint
		acc.Mount.CacheDir = config.CacheDir
		acc.Mount.CacheTTL = config.CacheTTL
		acc.Mount.CacheMaxEntries = config.CacheMaxEntries
		acc.Mount.CacheMaxSize = config.CacheMaxSize
		acc.Mount.DeltaInterval = config.DeltaInterval
		acc.Mount.MaxUploadsInFlight = config.MaxUploadsInFlight
		acc.Mount.MaxUploadRetries = config.MaxUploadRetries
		acc.Mount.GraphRetries = config.GraphRetries
		acc.Mount.HTTPTimeout = config.HTTPTimeout
		acc.Unlock()

		if err := acc.Save(); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "⚠️  Could not save configuration: %v\n", err)
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

// registerMountCmd adds the mount command's flags and registers it in root.
func registerMountCmd(root *cobra.Command) {
	mountCmd.Flags().StringP("account", "a", "", "Account name to mount (e.g.: user@outlook.com)")

	// ──── Basic ────
	mountCmd.Flags().String("cache-dir", "", "Root cache directory (default: ~/.cache/onecloudriver/<account>)")
	mountCmd.Flags().Duration("cache-ttl", 0, "Base TTL for cached metadata (e.g.: 60s, 5m). 0 = use persisted or default")
	mountCmd.Flags().Int("cache-max-entries", 0, "Max folders with cached children in memory. 0 = use persisted or default")
	mountCmd.Flags().String("cache-max-size", "0", "Max ContentCache size on disk (e.g.: 1GB, 500MB). 0 = unlimited")

	// ──── Advanced ────
	mountCmd.Flags().Duration("delta-interval", 0, "Delta polling interval (e.g.: 5m, 60s). 0 = use persisted or default")
	mountCmd.Flags().Int("max-uploads", 0, "Max concurrent uploads (default: 5). 0 = use persisted or default")
	mountCmd.Flags().Int("upload-retries", 0, "Max retries per upload (default: 5). 0 = use persisted or default")
	mountCmd.Flags().Int("graph-retries", 0, "HTTP retries on 429/503 (default: 3). 0 = use persisted or default")
	mountCmd.Flags().Duration("http-timeout", 0, "HTTP request timeout to Graph (default: 15s). 0 = use persisted or default")

	root.AddCommand(mountCmd)
}
