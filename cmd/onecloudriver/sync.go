package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/frosado/onecloudriver/internal/fs"
	"github.com/frosado/onecloudriver/internal/i18n"
	"github.com/frosado/onecloudriver/internal/printer"
	"github.com/spf13/cobra"
)

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
