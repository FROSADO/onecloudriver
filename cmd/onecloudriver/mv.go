package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var mvCmd = &cobra.Command{
	Use:   "mv",
	Short: "Move a file or folder to another location in OneDrive",
	Long: `Moves an item (file or folder) to a new parent folder in OneDrive.

The source and destination are specified by ID or by path, independently:

  onecloudriver mv --account user@mail.com --id 01BYE5RZ... --dest-id FOLDER456
  onecloudriver mv --account user@mail.com --path /Docs/old.txt --dest-path /Archive
  onecloudriver mv --account user@mail.com --id 01BYE5RZ... --dest-path /Archive`,

	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		acc, err := resolveAccountFromCmd(cmd)
		if err != nil {
			return err
		}
		itemID, _ := cmd.Flags().GetString("id")
		itemPath, _ := cmd.Flags().GetString("path")

		r, err := buildResource(itemID, itemPath, " for the source")
		if err != nil {
			return err
		}

		destID, _ := cmd.Flags().GetString("dest-id")
		destPath, _ := cmd.Flags().GetString("dest-path")

		dest, err := buildDestResource(destID, destPath)
		if err != nil {
			return err
		}
		graphClient := getClient(cmd)

		etag, _ := cmd.Flags().GetString("etag")

		moved, err := graphClient.MoveItem(cmd.Context(), acc, r, dest, etag)
		if err != nil {
			return fmt.Errorf("error moving: %w", err)
		}

		fmt.Printf("Item '%s' moved successfully (ID: %s)\n", moved.Name, moved.ID)

		return nil
	},
}

// registerMvCmd adds the mv command's flags and registers it in root.
func registerMvCmd(root *cobra.Command) {
	addAccountFlag(mvCmd)
	addIDPathFlags(mvCmd, "ID of the item to move", "Path of the item to move (e.g.: /Documents/old.txt)")
	addDestFlags(mvCmd)
	addEtagFlag(mvCmd)

	root.AddCommand(mvCmd)
}
