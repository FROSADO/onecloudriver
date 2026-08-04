package main

import (
	"fmt"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/spf13/cobra"
)

var rmCmd = &cobra.Command{
	Use:   "rm",
	Short: "Permanently delete a file or folder from OneDrive",
	Long: `Permanently deletes an item (file or folder) from OneDrive.

The item must be specified by ID or by path (not both), and confirmed with --force:

  onecloudriver rm --account user@mail.com --id 01BYE5RZ... --force
  onecloudriver rm --account user@mail.com --path /Documents/photo.jpg -f`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		accountName, _ := cmd.Flags().GetString("account")
		if accountName == "" {
			resolved, err := manager.ResolveMainAccountName()
			if err != nil {
				return fmt.Errorf("you must specify an account with --account")
			}
			accountName = resolved
			fmt.Printf("Using the only default account '%s'\n", accountName)
		}

		itemID, _ := cmd.Flags().GetString("id")
		itemPath, _ := cmd.Flags().GetString("path")

		if (itemID == "" && itemPath == "") || (itemID != "" && itemPath != "") {
			return fmt.Errorf("you must specify exactly one of --id or --path")
		}

		force, _ := cmd.Flags().GetBool("force")
		if !force {
			return fmt.Errorf("destructive operation: use --force to confirm deletion")
		}

		acc, err := manager.GetAccount(accountName)
		if err != nil {
			return err
		}

		graphClient := graph.NewClient()

		r := graph.Resource(graph.ItemPath(itemPath))
		if itemID != "" {
			r = graph.ItemID(itemID)
		}

		target := itemID
		if target == "" {
			target = itemPath
		}

		etag, _ := cmd.Flags().GetString("etag")

		if err := graphClient.DeleteItem(cmd.Context(), acc, r, etag); err != nil {
			return fmt.Errorf("error deleting: %w", err)
		}

		fmt.Printf("Item '%s' successfully deleted.\n", target)

		return nil
	},
}

// registerRmCmd adds the rm command's flags and registers it in root.
func registerRmCmd(root *cobra.Command) {
	rmCmd.Flags().StringP("account", "a", "", "Account name to use. If omitted, uses the only configured account.")
	rmCmd.Flags().String("id", "", "ID of the item to delete")
	rmCmd.Flags().String("path", "", "Path of the item to delete (e.g.: /Documents/photo.jpg)")
	rmCmd.Flags().BoolP("force", "f", false, "Confirm deletion (required)")
	rmCmd.Flags().String("etag", "", "ETag of the item for concurrency control (optional)")

	root.AddCommand(rmCmd)
}
