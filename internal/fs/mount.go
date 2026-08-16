package fs

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/frosado/onecloudriver/internal/auth"
	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/frosado/onecloudriver/internal/printer"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// CacheHandles lets the UI (or other components) interact with the
// caches while hot: query stats, modify configuration, force refreshes.
type CacheHandles struct {
	Metadata *InodeCache
	Content  *ContentCache
	Delta    *DeltaSync
	Uploads  *UploadManager
}

// MountConfig groups the cache configuration that the user can adjust
// from the CLI. Use the DefaultMountConfig() constructor to get
// default values and then override individual fields.
type MountConfig struct {
	// CacheDir is the root of the cache tree. Content is stored in
	// <CacheDir>/content/ and BoltDB at <CacheDir>/inodes.db.
	// Default: ~/.cache/onecloudriver/<account>
	CacheDir string

	// CacheTTL is the base lifetime of cached metadata before it is
	// considered stale (Phase 4). Default: 60s.
	CacheTTL time.Duration

	// CacheMaxEntries is the maximum number of folders with cached children
	// in memory before activating eviction (Phase 4). Default: 2000.
	CacheMaxEntries int

	// CacheMaxSize is the maximum size in bytes of the ContentCache on disk
	// before activating age-based eviction (Phase 4b). 0 = no limit.
	CacheMaxSize int64

	// ──── Advanced (read from AccountPersistedConfig if present) ────

	// DeltaInterval controls how often the /delta endpoint is polled.
	// 0 = use the default (5 min).
	DeltaInterval time.Duration

	// MaxUploadsInFlight limits concurrent uploads (default: 5).
	MaxUploadsInFlight int

	// MaxUploadRetries is the maximum retries per upload (default: 5).
	MaxUploadRetries int

	// GraphRetries is the number of HTTP retries on 429/503 (default: 3).
	GraphRetries int

	// HTTPTimeout is the HTTP client timeout (default: 15s).
	HTTPTimeout time.Duration
}

// DefaultMountConfig returns a configuration with default values
// reasonable values. If persisted is not nil, its non-zero fields override the
// defaults (letting the account JSON persist preferences).
func DefaultMountConfig(accountName string, persisted *auth.AccountPersistedConfig) MountConfig {
	cacheBase := filepath.Join(os.Getenv("HOME"), ".cache", "onecloudriver")

	cfg := MountConfig{
		CacheDir:           filepath.Join(cacheBase, accountName),
		CacheTTL:           60 * time.Second,
		CacheMaxEntries:    2000,
		CacheMaxSize:       0,
		DeltaInterval:      5 * time.Minute,
		MaxUploadsInFlight: 5,
		MaxUploadRetries:   5,
		GraphRetries:       3,
		HTTPTimeout:        15 * time.Second,
	}

	if persisted != nil {
		if persisted.CacheDir != "" {
			cfg.CacheDir = persisted.CacheDir
		}
		if persisted.CacheTTL > 0 {
			cfg.CacheTTL = persisted.CacheTTL
		}
		if persisted.CacheMaxEntries > 0 {
			cfg.CacheMaxEntries = persisted.CacheMaxEntries
		}
		if persisted.CacheMaxSize > 0 {
			cfg.CacheMaxSize = persisted.CacheMaxSize
		}
		if persisted.DeltaInterval > 0 {
			cfg.DeltaInterval = persisted.DeltaInterval
		}
		if persisted.MaxUploadsInFlight > 0 {
			cfg.MaxUploadsInFlight = persisted.MaxUploadsInFlight
		}
		if persisted.MaxUploadRetries > 0 {
			cfg.MaxUploadRetries = persisted.MaxUploadRetries
		}
		if persisted.GraphRetries > 0 {
			cfg.GraphRetries = persisted.GraphRetries
		}
		if persisted.HTTPTimeout > 0 {
			cfg.HTTPTimeout = persisted.HTTPTimeout
		}
	}

	return cfg
}

// healthCheck verifies that the account can authenticate against Microsoft Graph
// before starting the FUSE mount. This avoids the "I mounted but it doesn't
// work" scenario: if the token expired, was revoked, or the account lacks
// permissions, the user gets a clear message instead of an empty or broken
// mountpoint.
//
// Strategy:
//  1. Get token → if it fails due to a network error, warn but continue (offline mode)
//  2. Call /me/drive/root → if 401/403, fail with clear diagnosis
//  3. If a network error occurs on /me, warn and continue (offline mode)
func healthCheck(ctx context.Context, account *auth.Account, graphClient *graph.Client) error {
	// 1. Verify that we can obtain an access token.
	token, err := account.GetAccessToken(ctx)
	if err != nil {
		// If the error is a network error, offline mode may work.
		if isNetworkError(err) {
			fmt.Printf("%s No internet connection. Starting in offline mode (cache read-only).\n", printer.Warning)
			return nil
		}
		return fmt.Errorf("could not obtain access token: %w", err)
	}
	_ = token // the token is passed implicitly via account (TokenProvider)

	// 2. Verify that the token works against Microsoft Graph.
	//    We use /me/drive/root because it's the lightest call that validates
	//    both authentication and Files.ReadWrite permissions.
	_, err = graphClient.GetItem(ctx, account, graph.RootID)
	if err != nil {
		if isNetworkError(err) {
			fmt.Printf("%s Microsoft Graph is not responding. Starting in offline mode (cache read-only).\n", printer.Warning)
			return nil
		}
		// Authentication/authorization error: the token is not valid.
		return fmt.Errorf(
			"token verification against Microsoft Graph failed: %w\n\n"+
				"%s Diagnosis: the access token for '%s' is invalid.\n"+
				"   Common causes:\n"+
				"   • The session expired and the refresh token was revoked\n"+
				"   • The account was deleted or the password changed\n"+
				"   • The application was revoked in the Azure portal\n"+
				"\n"+
				"   Solution: re-authenticate with:\n"+
				"     onecloudriver account remove %s\n"+
				"     onecloudriver account add\n",
			err, printer.Clipboard, account.Name, account.Name,
		)
	}

	return nil
}

// Mount starts the FUSE server and handles safe unmounting on Ctrl+C.
// Returns CacheHandles so the UI can manage the cache in real time.
func Mount(mountpoint string, account *auth.Account, config MountConfig) (*CacheHandles, error) {
	// Check that the mountpoint exists and is a directory before calling Mount().
	if info, err := os.Stat(mountpoint); err != nil {
		return nil, fmt.Errorf("mount point '%s' does not exist", mountpoint)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("mount point '%s' is not a directory", mountpoint)
	}

	// Build Graph client with advanced parameters if specified
	var graphOpts []graph.Option
	if config.HTTPTimeout > 0 {
		graphOpts = append(graphOpts, graph.WithTimeout(config.HTTPTimeout))
	}
	graphClient := graph.NewClient(graphOpts...)
	// Override the RetryDoer with the configured number of retries
	if config.GraphRetries > 0 {
		graphClient.HTTPClient = graph.NewRetryDoer(graphClient.HTTPClient, config.GraphRetries)
	}

	// Health check BEFORE creating caches or mounting FUSE.
	// If the token is invalid, we fail fast with a clear message.
	if err := healthCheck(context.Background(), account, graphClient); err != nil {
		return nil, err
	}

	contentCache, err := NewContentCache(filepath.Join(config.CacheDir, "content"))
	if err != nil {
		return nil, fmt.Errorf("error creating ContentCache: %w", err)
	}

	// Create the inode cache
	inodeCache := NewInodeCache()

	inodeCache.SetBaseTTL(config.CacheTTL)
	inodeCache.SetMaxEntries(config.CacheMaxEntries)
	contentCache.SetMaxSize(config.CacheMaxSize)

	// Start the eviction sweep in background
	inodeCache.StartSweep()

	// Initialize BoltDB for metadata persistence across restarts.
	boltDBPath := filepath.Join(config.CacheDir, "inodes.db")
	if err := inodeCache.InitBoltDB(boltDBPath); err != nil {
		log.Printf("%s Could not initialize BoltDB at %s: %v. The cache will not persist across restarts.", printer.Warning, boltDBPath, err)
	}

	defer func() {
		if err := inodeCache.Close(); err != nil {
			log.Printf("%s Error closing inode cache during cleanup: %v", printer.Warning, err)
		}
	}()

	// UploadManager for asynchronous uploads with retries.
	uploadManager := NewUploadManager(graphClient, account, inodeCache, contentCache, config.MaxUploadsInFlight, config.MaxUploadRetries)

	// Start delta synchronization in the background with the configured interval.
	deltaInterval := config.DeltaInterval
	if deltaInterval <= 0 {
		deltaInterval = 5 * time.Minute
	}
	ctx, cancelDelta := context.WithCancel(context.Background())
	deltaSync := NewDeltaSync(graphClient, account, inodeCache, contentCache)
	// Let DeltaSync skip remote changes for items with a pending upload, so a
	// remote edit never clobbers a local one that is still being uploaded.
	deltaSync.SetUploadQuery(uploadManager)
	deltaSync.Start(ctx, deltaInterval)

	uploadManager.Start()

	root := NewOneCloudFS(graphClient, account, inodeCache, contentCache, uploadManager)

	opts := &fs.Options{
		MountOptions: fuse.MountOptions{
			DirectMountStrict: false,
			Options: []string{
				"rw",
			},
		},
	}

	server, err := fs.Mount(mountpoint, root, opts)
	if err != nil {
		cancelDelta()
		deltaSync.Stop()
		return nil, fmt.Errorf("error mounting FUSE: %w", err)
	}

	log.Printf("%s Filesystem mounted successfully at: %s", printer.Success, mountpoint)
	log.Println("Press Ctrl+C to unmount and exit safely.")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		<-sigChan
		log.Println("\n" + printer.Stop + " Interrupt signal received. Unmounting filesystem...")

		cancelDelta()
		deltaSync.Stop()
		uploadManager.Stop()

		// Persist the dirty inodes since the last delta poll, then the full
		// tree as the final durability guarantee (issue #67; Close() also
		// runs SerializeAll as a backstop).
		if err := inodeCache.SerializeDirty(); err != nil {
			log.Printf("%s Error persisting dirty inode cache: %v", printer.Warning, err)
		}
		if err := inodeCache.SerializeAll(); err != nil {
			log.Printf("%s Error persisting inode cache: %v", printer.Warning, err)
		}

		unmounted := false
		if err := server.Unmount(); err == nil {
			log.Println(printer.Success, "Filesystem unmounted successfully.")
			unmounted = true
		} else {
			log.Println(printer.Warning, "Normal unmount failed (file explorer open?). Trying lazy-unmount...")
			if err := exec.Command("fusermount3", "-u", "-z", mountpoint).Run(); err != nil {
				log.Printf("%s Lazy-unmount also failed: %v. Forcing exit...", printer.Error, err)
			} else {
				log.Println(printer.Success, "Lazy-unmount executed. The kernel will unmount when the resource is released.")
				unmounted = true
			}
		}

		contentCache.CloseAll()

		if err := inodeCache.Close(); err != nil {
			log.Printf("%s Error closing BoltDB: %v", printer.Warning, err)
		}

		if unmounted {
			log.Println("Goodbye!")
		}

		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	}()

	server.Wait()

	return &CacheHandles{Metadata: inodeCache, Content: contentCache, Delta: deltaSync, Uploads: uploadManager}, nil
}
