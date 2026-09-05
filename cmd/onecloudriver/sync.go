package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/frosado/onecloudriver/internal/control"
	"github.com/frosado/onecloudriver/internal/fs"
	"github.com/frosado/onecloudriver/internal/i18n"
	"github.com/frosado/onecloudriver/internal/printer"
	"github.com/frosado/onecloudriver/internal/service"
	"github.com/spf13/cobra"
)

// mountProbeTimeout bounds each up-front "is there an active mount" probe of
// the sync command. Real probes answer in milliseconds; the timeout only
// guards against a hung control server.
const mountProbeTimeout = 2 * time.Second

// activeHolder describes the instance found holding the target cache.
type activeHolder struct {
	// mountpoint is empty when the holder's mountpoint cannot be discovered
	// (control channel unreachable and no systemd service to ask).
	mountpoint string
}

// detectActiveMount probes whether another instance is serving cacheDir and
// would therefore own the exclusive BoltDB lock that a manual sync needs
// (issue #146). Order:
//
//  1. Control socket of <cacheDir>/control.sock (#147): precise "same cache"
//     signal that also yields the real mountpoint of the holder.
//  2. BoltDB lock probe (fs.CacheDirInUse): catches any holder even when the
//     mount disabled its control channel (--control-socket off/custom).
//  3. systemd service: enriches the message with the mountpoint when the lock
//     is held but the socket is unreachable and the holder is the service.
//
// It is best-effort (a probe error means "no holder found"): InitBoltDB with
// its full timeout remains the authoritative guard. Returns nil when the cache
// is free.
func detectActiveMount(account, cacheDir string) *activeHolder {
	return detectActiveMountWith(account,
		control.NewClient(filepath.Join(cacheDir, "control.sock")).Info,
		func() (bool, error) { return fs.CacheDirInUse(cacheDir) },
		func() (string, bool) { return service.RunningMountpoint(account) },
	)
}

// detectActiveMountWith is the injectable core of detectActiveMount. sockInfo
// returns the control Info of the cache holder (or control.ErrNotRunning),
// cacheInUse reports the BoltDB lock, and svcRunning reports a running systemd
// service's mountpoint.
func detectActiveMountWith(account string, sockInfo func(context.Context) (*control.Info, error), cacheInUse func() (bool, error), svcRunning func() (string, bool)) *activeHolder {
	if sockInfo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), mountProbeTimeout)
		defer cancel()
		if info, err := sockInfo(ctx); err == nil && info != nil &&
			info.Account.Name == account && info.Mount.State == "running" && info.Mount.Mountpoint != "" {
			return &activeHolder{mountpoint: info.Mount.Mountpoint}
		}
	}

	if cacheInUse != nil {
		if busy, err := cacheInUse(); err == nil && busy {
			if svcRunning != nil {
				if mp, running := svcRunning(); running && mp != "" {
					return &activeHolder{mountpoint: mp}
				}
			}
			return &activeHolder{}
		}
	}

	return nil
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Force an immediate delta synchronization",
	Long: `Force an immediate delta synchronization for an account, independent of
the background poll interval of a mount.

Applies the remote changes (items created, modified or deleted from other
clients) to the account's persisted cache right now and prints how many
changes were applied. It works without a mount, reusing the same DeltaSync
machinery as the background loop.

A running mount already polls on its own schedule (--delta-interval) and holds
an exclusive lock on the account's cache, so run this while the account is not
mounted (or stop the mount/service first).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		acc, err := resolveAccountFromCmd(cmd)
		if err != nil {
			return err
		}

		config := fs.DefaultMountConfig(acc.Name, &acc.Mount)

		// Detect an active mount/service for this cache BEFORE opening BoltDB
		// (issue #146): a running instance owns the exclusive lock and its
		// delta loop already applies remote changes, so a manual sync would
		// only fail after the 5s lock timeout. The detection is best-effort;
		// InitBoltDB below remains the authoritative guard.
		if holder := detectActiveMount(acc.Name, config.CacheDir); holder != nil {
			if holder.mountpoint != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s %s\n", printer.Warning,
					i18n.Ld("cmd.sync.mounted_active", map[string]any{
						"Account": acc.Name,
						"Path":    holder.mountpoint,
					}))
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s %s\n", printer.Warning,
					i18n.Ld("cmd.sync.cache_in_use", map[string]any{
						"CacheDir": config.CacheDir,
					}))
			}
			return fmt.Errorf("sync failed: the cache directory is in use by an active mount or service")
		}

		// Ensure the cache tree exists before opening BoltDB inside it.
		if err := os.MkdirAll(config.CacheDir, 0700); err != nil {
			return fmt.Errorf("error creating cache directory %s: %w", config.CacheDir, err)
		}

		// A running mount holds an exclusive lock on the account's inodes.db:
		// forcing a second writer would corrupt the cache, and the mount's own
		// delta loop is already live anyway. Fail with a clear message instead
		// of the opaque lock timeout.
		inodeCache := fs.NewInodeCache()
		if err := inodeCache.InitBoltDB(filepath.Join(config.CacheDir, "inodes.db")); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s %s\n", printer.Warning, i18n.L("cmd.sync.tip_mounted"))
			return fmt.Errorf("sync failed: %w", err)
		}
		defer inodeCache.Close()

		contentCache, err := fs.NewContentCache(filepath.Join(config.CacheDir, "content"))
		if err != nil {
			return fmt.Errorf("error creating ContentCache: %w", err)
		}
		defer contentCache.CloseAll()

		deltaSync := fs.NewDeltaSync(getClient(cmd), acc, inodeCache, contentCache)

		fmt.Fprintf(cmd.ErrOrStderr(), "%s %s\n", printer.Refresh, i18n.Ld("cmd.sync.syncing", map[string]any{"Account": acc.Name}))
		n, err := deltaSync.PollOnce(cmd.Context())
		if err != nil {
			return fmt.Errorf("error during delta sync: %w", err)
		}

		if n == 1 {
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", printer.Success, i18n.Ld("cmd.sync.complete_one", map[string]any{"Count": n}))
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", printer.Success, i18n.Ld("cmd.sync.complete_other", map[string]any{"Count": n}))
		}
		return nil
	},
}

// registerSyncCmd adds the sync command's flags and registers it in root.
func registerSyncCmd(root *cobra.Command) {
	addAccountFlag(syncCmd)
	root.AddCommand(syncCmd)
}
