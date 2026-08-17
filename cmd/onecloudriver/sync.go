package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/frosado/onecloudriver/internal/fs"
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
		acc, err := resolveAccount(cmd, manager)
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
			fmt.Fprintf(cmd.ErrOrStderr(), "%s Tip: if a mount for this account is running, its background delta loop already applies remote changes automatically (see --delta-interval). Stop the mount or service first to sync manually.\n", printer.Warning)
			return fmt.Errorf("sync failed: %w", err)
		}
		defer inodeCache.Close()

		contentCache, err := fs.NewContentCache(filepath.Join(config.CacheDir, "content"))
		if err != nil {
			return fmt.Errorf("error creating ContentCache: %w", err)
		}
		defer contentCache.CloseAll()

		deltaSync := fs.NewDeltaSync(getClient(cmd), acc, inodeCache, contentCache)

		fmt.Fprintf(cmd.ErrOrStderr(), "%s Syncing '%s'...\n", printer.Refresh, acc.Name)
		n, err := deltaSync.PollOnce(cmd.Context())
		if err != nil {
			return fmt.Errorf("error during delta sync: %w", err)
		}

		verb := "changes"
		if n == 1 {
			verb = "change"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s Sync complete: %d %s applied\n", printer.Success, n, verb)
		return nil
	},
}

// registerSyncCmd adds the sync command's flags and registers it in root.
func registerSyncCmd(root *cobra.Command) {
	syncCmd.Flags().StringP("account", "a", "", "Account name to sync. If omitted, uses the only configured account.")
	root.AddCommand(syncCmd)
}
