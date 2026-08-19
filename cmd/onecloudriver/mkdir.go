package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var mkdirCmd = &cobra.Command{
	Use:   "mkdir",
	Short: "Create a folder in OneDrive",
	Long: `Creates a new folder inside a parent directory in OneDrive.

The parent folder is specified by ID or by path (not both):

  onecloudriver mkdir --account user@mail.com --id 01BYE5RZ... --name "New Folder"
  onecloudriver mkdir --account user@mail.com --path /Documents --name "Photos"`,
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

		folderName, _ := cmd.Flags().GetString("name")
		if folderName == "" {
			return fmt.Errorf("you must specify the folder name with --name")
		}
		graphClient := getClient(cmd)

		folder, err := graphClient.CreateFolder(cmd.Context(), acc, r, folderName)
		if err != nil {
			return fmt.Errorf("error creating folder: %w", err)
		}

		fmt.Printf("Folder created: %s (ID: %s)\n", folder.Name, folder.ID)

		return nil
	},
}

// registerMkdirCmd adds the mkdir command's flags and registers it in root.
func registerMkdirCmd(root *cobra.Command) {
	mkdirCmd.Flags().StringP("account", "a", "", "Account name to use. If omitted, uses the only configured account.")
	mkdirCmd.Flags().String("id", "", "ID of the parent folder")
	mkdirCmd.Flags().String("path", "", "Path of the parent folder (e.g.: /Documents)")
	mkdirCmd.Flags().StringP("name", "n", "", "Name of the new folder (required)")
	mkdirCmd.MarkFlagRequired("name")

	root.AddCommand(mkdirCmd)
}
