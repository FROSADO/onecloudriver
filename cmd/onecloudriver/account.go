package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/frosado/onecloudriver/internal/auth"
	"github.com/frosado/onecloudriver/internal/fs"
	"github.com/spf13/cobra"
)

var accountCmd = &cobra.Command{
	Use:   "account",
	Short: "Manage configured Microsoft accounts",
}

var accountAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new Microsoft account",
	RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := getManager(cmd)
		if err != nil {
			return err
		}
		_, err = manager.AddAccount(cmd.Context(), auth.AuthConfig{}, true, os.Stdin)
		if err != nil {
			return fmt.Errorf("error adding account: %w", err)
		}
		return nil
	},
}

var accountListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured accounts",
	Run: func(cmd *cobra.Command, args []string) {
		manager, err := getManager(cmd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return
		}
		accounts := manager.ListAccounts()
		if len(accounts) == 0 {
			fmt.Println("No accounts configured. Use 'onecloudriver account add'")
			return
		}
		fmt.Println("Configured accounts:")
		for _, acc := range accounts {
			fmt.Printf("  - %s\n", acc)
		}
	},
}

var accountRemoveCmd = &cobra.Command{
	Use:   "remove [account_name]",
	Short: "Remove a configured account",
	Long: `Removes a Microsoft account and, by default, asks whether to delete
its local cache (~/.cache/onecloudriver/<account>).

Use --purge to delete the cache without asking, or --keep to preserve it.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := getManager(cmd)
		if err != nil {
			return err
		}
		accountName := args[0]

		purge, _ := cmd.Flags().GetBool("purge")
		keep, _ := cmd.Flags().GetBool("keep")

		if purge && keep {
			return fmt.Errorf("--purge and --keep are mutually exclusive")
		}

		acc, err := manager.GetAccount(accountName)
		if err != nil {
			return err
		}
		cacheDir := fs.DefaultMountConfig(accountName, &acc.Mount).CacheDir

		if err := manager.RemoveAccount(accountName); err != nil {
			return err
		}
		fmt.Printf("Account '%s' successfully removed.\n", accountName)

		return confirmCacheDeletion(cacheDir, purge, keep, os.Stdin)
	},
}

// confirmCacheDeletion handles the local-cache cleanup after an account has
// been removed. It respects --keep and --purge, or prompts interactively via
// stdin when neither is given. Behavior matches the inline logic it replaces.
func confirmCacheDeletion(cacheDir string, purge, keep bool, stdin io.Reader) error {
	if keep {
		fmt.Printf("Cache preserved at: %s\n", cacheDir)
		return nil
	}

	if !purge {
		fmt.Printf("Also delete the local cache at %s? [y/N]: ", cacheDir)
		reader := bufio.NewReader(stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Println("Cache preserved.")
			return nil
		}
	}

	if err := os.RemoveAll(cacheDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("error deleting cache: %w", err)
	}
	fmt.Printf("Cache deleted: %s\n", cacheDir)
	return nil
}

// registerAccountCmd adds the flags for account remove, assembles the
// subcommands, and registers accountCmd in root.
func registerAccountCmd(root *cobra.Command) {
	accountRemoveCmd.Flags().Bool("purge", false, "Delete the local cache without asking for confirmation")
	accountRemoveCmd.Flags().Bool("keep", false, "Preserve the local cache (do not delete it)")

	accountCmd.AddCommand(accountAddCmd, accountListCmd, accountRemoveCmd)
	root.AddCommand(accountCmd)
}
