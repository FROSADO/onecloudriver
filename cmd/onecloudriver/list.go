package main

import (
	"fmt"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List files at the root of an account's OneDrive",
	Args:  cobra.NoArgs,
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

		fmt.Fprintf(cmd.ErrOrStderr(), "Querying OneDrive for '%s'...\n", acc.Name)
		graphClient := graph.NewClient()
		items, err := graphClient.ListDriveRoot(cmd.Context(), acc)
		if err != nil {
			return fmt.Errorf("error listing files: %w", err)
		}

		if len(items) == 0 {
			fmt.Println("The root folder is empty.")
			return nil
		}

		output, err := formatter.FormatDriveItems(items)
		if err != nil {
			return err
		}
		fmt.Print(output)

		return nil
	},
}

// registerListCmd adds the list command's flags and registers it in root.
func registerListCmd(root *cobra.Command) {
	listCmd.Flags().StringP("account", "a", "", "Account name to query. If omitted, uses the only configured account.")
	listCmd.Flags().StringP("output", "o", "yaml", "Output format: text, json, yaml")

	root.AddCommand(listCmd)
}
