package main

import (
	"fmt"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/spf13/cobra"
)

var copyCmd = &cobra.Command{
	Use:   "copy",
	Short: "Copy a file or folder in OneDrive",
	Long: `Copies an item (file or folder) to a new location and/or with a new name.

The copy operation is asynchronous and returns a URL to monitor progress.

The source is specified by --id or --path. Optionally you can specify:
  --name: new name for the copy
  --dest-id / --dest-path: destination folder

At least one of --name or --dest-* must be specified:

  onecloudriver copy --account user@mail.com --id 01BYE5RZ... --name "copy.pdf"
  onecloudriver copy --account user@mail.com --path /Docs/photo.jpg --dest-path /Backup`,

	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
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

		newName, _ := cmd.Flags().GetString("name")
		destID, _ := cmd.Flags().GetString("dest-id")
		destPath, _ := cmd.Flags().GetString("dest-path")

		if newName == "" && destID == "" && destPath == "" {
			return fmt.Errorf("you must specify at least --name or --dest-id/--dest-path")
		}
		if err := validateOptionalDestFlags(destID, destPath); err != nil {
			return err
		}
		graphClient := getClient(cmd)

		var dest graph.Resource
		if destID != "" {
			dest = graph.ItemID(destID)
		} else if destPath != "" {
			dest = graph.ItemPath(destPath)
		}

		monitorURL, err := graphClient.CopyItem(cmd.Context(), acc, r, newName, dest)
		if err != nil {
			return fmt.Errorf("error copying: %w", err)
		}

		fmt.Printf("Copy started. Monitor progress at:\\n%s\\n", monitorURL)

		return nil
	},
}

// registerCopyCmd adds the copy command's flags and registers it in root.
func registerCopyCmd(root *cobra.Command) {
	copyCmd.Flags().StringP("account", "a", "", "Account name to use. If omitted, uses the only configured account.")
	copyCmd.Flags().String("id", "", "ID of the item to copy")
	copyCmd.Flags().String("path", "", "Path of the item to copy (e.g.: /Documents/photo.jpg)")
	copyCmd.Flags().StringP("name", "n", "", "New name for the copy")
	copyCmd.Flags().String("dest-id", "", "ID of the destination folder")
	copyCmd.Flags().String("dest-path", "", "Path of the destination folder (e.g.: /Backup)")

	root.AddCommand(copyCmd)
}
