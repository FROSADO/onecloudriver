package main

import (
	"fmt"

	"github.com/frosado/onecloudriver/internal/graph"
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

		r, err := buildResource(itemID, itemPath, " for the source")
		if err != nil {
			return err
		}

		destID, _ := cmd.Flags().GetString("dest-id")
		destPath, _ := cmd.Flags().GetString("dest-path")

		if err := validateDestFlags(destID, destPath); err != nil {
			return err
		}
		graphClient := getClient(cmd)

		dest := graph.Resource(graph.ItemPath(destPath))
		if destID != "" {
			dest = graph.ItemID(destID)
		}

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
	mvCmd.Flags().StringP("account", "a", "", "Account name to use. If omitted, uses the only configured account.")
	mvCmd.Flags().String("id", "", "ID of the item to move")
	mvCmd.Flags().String("path", "", "Path of the item to move (e.g.: /Documents/old.txt)")
	mvCmd.Flags().String("dest-id", "", "ID of the destination folder")
	mvCmd.Flags().String("dest-path", "", "Path of the destination folder (e.g.: /Archive)")
	mvCmd.Flags().String("etag", "", "ETag of the item for concurrency control (optional)")

	root.AddCommand(mvCmd)
}
