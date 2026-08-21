package main

import (
	"fmt"

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
		acc, err := resolveAccountFromCmd(cmd)
		if err != nil {
			return err
		}

		r, target, err := resourceFromCmdWithLabel(cmd, "")
		if err != nil {
			return err
		}

		force, _ := cmd.Flags().GetBool("force")
		if !force {
			return fmt.Errorf("destructive operation: use --force to confirm deletion")
		}
		graphClient := getClient(cmd)

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
	addAccountFlag(rmCmd)
	addIDPathFlags(rmCmd, "ID of the item to delete", "Path of the item to delete (e.g.: /Documents/photo.jpg)")
	rmCmd.Flags().BoolP("force", "f", false, "Confirm deletion (required)")
	addEtagFlag(rmCmd)

	root.AddCommand(rmCmd)
}
