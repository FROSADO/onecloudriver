package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var renameCmd = &cobra.Command{
	Use:   "rename",
	Short: "Rename a file or folder in OneDrive",
	Long: `Renames an item (file or folder) in OneDrive.

The item must be specified by ID or by path (not both), and the new name:

  onecloudriver rename --account user@mail.com --id 01BYE5RZ... --name "new-name.pdf"
  onecloudriver rename --account user@mail.com --path /Documents/old.txt --name "new.txt"`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := getManager(cmd)
		if err != nil {
			return err
		}
		acc, err := resolveAccount(cmd, manager)
		if err != nil {
			return err
		}

		itemID, _ := cmd.Flags().GetString("id")
		itemPath, _ := cmd.Flags().GetString("path")

		r, err := buildResource(itemID, itemPath, "")
		if err != nil {
			return err
		}

		newName, _ := cmd.Flags().GetString("name")
		if newName == "" {
			return fmt.Errorf("you must specify the new name with --name")
		}

		graphClient := getClient(cmd)

		etag, _ := cmd.Flags().GetString("etag")

		item, err := graphClient.RenameItem(cmd.Context(), acc, r, newName, etag)
		if err != nil {
			return fmt.Errorf("error renaming: %w", err)
		}

		fmt.Printf("Item renamed to: %s (ID: %s)\n", item.Name, item.ID)

		return nil
	},
}

// registerRenameCmd adds the rename command's flags and registers it in root.
func registerRenameCmd(root *cobra.Command) {
	renameCmd.Flags().StringP("account", "a", "", "Account name to use. If omitted, uses the only configured account.")
	renameCmd.Flags().String("id", "", "ID of the item to rename")
	renameCmd.Flags().String("path", "", "Path of the item to rename (e.g.: /Documents/old.txt)")
	renameCmd.Flags().StringP("name", "n", "", "New name of the item (required)")
	renameCmd.Flags().String("etag", "", "ETag of the item for concurrency control (optional)")
	renameCmd.MarkFlagRequired("name")

	root.AddCommand(renameCmd)
}
