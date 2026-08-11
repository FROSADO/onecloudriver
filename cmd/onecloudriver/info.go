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
		acc, err := resolveAccount(cmd, manager)
		if err != nil {
			return err
		}

		format, _ := cmd.Flags().GetString("output")
		formatter, err := getFormatter(format)
		if err != nil {
			return err
		}

		itemID, _ := cmd.Flags().GetString("id")
		itemPath, _ := cmd.Flags().GetString("path")

		r, err := buildResource(itemID, itemPath, "")
		if err != nil {
			return err
		}

		graphClient := graph.NewClient()

		var item *graph.DriveItem
		item, err = graphClient.GetItem(cmd.Context(), acc, r)
		if err != nil {
			return fmt.Errorf("error fetching information: %w", err)
		}

		output, err := formatter.FormatDriveItem(item)
		if err != nil {
			return err
		}
		fmt.Print(output)

		return nil
	},
}

// registerInfoCmd adds the info command's flags and registers it in root.
func registerInfoCmd(root *cobra.Command) {
	infoCmd.Flags().StringP("account", "a", "", "Account name to query. If omitted, uses the only configured account.")
	infoCmd.Flags().StringP("id", "i", "", "ID of the DriveItem to query")
	infoCmd.Flags().StringP("path", "p", "", "Path of the DriveItem to query (e.g.: /Documents/photo.jpg)")
	infoCmd.Flags().StringP("output", "o", "yaml", "Output format: text, json, yaml")

	root.AddCommand(infoCmd)
}
