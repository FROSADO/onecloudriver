package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List files at the root of an account's OneDrive",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		acc, err := resolveAccountFromCmd(cmd)
		if err != nil {
			return err
		}

		formatter, err := resolveFormatter(cmd)
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.ErrOrStderr(), "Querying OneDrive for '%s'...\n", acc.Name)
		graphClient := getClient(cmd)
		items, err := graphClient.ListDriveRoot(cmd.Context(), acc)
		if err != nil {
			return fmt.Errorf("error listing files: %w", err)
		}

		if len(items) == 0 {
			fmt.Println("The root folder is empty.")
			return nil
		}

		return formatOutput(formatter, items)
	},
}

// registerListCmd adds the list command's flags and registers it in root.
func registerListCmd(root *cobra.Command) {
	addAccountFlag(listCmd)
	addOutputFlag(listCmd)

	root.AddCommand(listCmd)
}
