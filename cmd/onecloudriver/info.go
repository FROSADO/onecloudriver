package main

import (
	"fmt"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/spf13/cobra"
)

var infoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show detailed information about a OneDrive file or folder",
	Long: `Displays the complete metadata of a DriveItem (file or folder) from OneDrive.

The item must be specified by ID or by path (not both):

  onecloudriver info --account user@mail.com --id 01BYE5RZ...
  onecloudriver info --account user@mail.com --path /Documents/photo.jpg`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		acc, err := resolveAccountFromCmd(cmd)
		if err != nil {
			return err
		}

		formatter, err := resolveFormatter(cmd)
		if err != nil {
			return err
		}

		itemID, _ := cmd.Flags().GetString("id")
		itemPath, _ := cmd.Flags().GetString("path")

		r, err := buildResource(itemID, itemPath, "")
		if err != nil {
			return err
		}

		graphClient := getClient(cmd)

		var item *graph.DriveItem
		item, err = graphClient.GetItem(cmd.Context(), acc, r)
		if err != nil {
			return fmt.Errorf("error fetching information: %w", err)
		}

		return formatOutput(formatter, item)
	},
}

// registerInfoCmd adds the info command's flags and registers it in root.
func registerInfoCmd(root *cobra.Command) {
	addAccountFlag(infoCmd)
	addIDPathFlagsWithShorthand(infoCmd, "ID of the DriveItem to query", "Path of the DriveItem to query (e.g.: /Documents/photo.jpg)")
	addOutputFlag(infoCmd)

	root.AddCommand(infoCmd)
}
