package main

import (
	"fmt"

	"github.com/frosado/onecloudriver/internal/auth"
	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/spf13/cobra"
)

// ResolveAccountName resolves the account name from the command flags (--account)
// or auto detect the default account in the auth manager.
func resolveAccountName(cmd *cobra.Command, manager *auth.Manager) (string, error) {
	accountName, _ := cmd.Flags().GetString("account")
	if accountName == "" {
		accountName, err := manager.ResolveMainAccountName()
		if err != nil {
			return "", fmt.Errorf("you must specify an account with --account")
		}
		fmt.Printf("Using the only default account '%s'\n", accountName)
	}
	return accountName, nil
}

// resolveAccount resolves the account from the command flags (--account)
// or auto detect the default account in the auth manager.
func resolveAccount(cmd *cobra.Command, manager *auth.Manager) (*auth.Account, error) {
	accountName, err := resolveAccountName(cmd, manager)
	if err != nil {
		return nil, err
	}
	acc, err := manager.GetAccount(accountName)
	if err != nil {
		return nil, fmt.Errorf("account '%s' does not exist. Use 'onecloudriver account add' to add it: %w", accountName, err)
	}
	return acc, nil

}

// buildResource builds a graph.Resource from exactly one of itemID or itemPath.
// Returns an error if both or neither are provided. The label parameter is
// appended to the error message to give context (e.g. " for the source").
func buildResource(itemID, itemPath, label string) (graph.Resource, error) {
	if (itemID == "" && itemPath == "") || (itemID != "" && itemPath != "") {
		return nil, fmt.Errorf("you must specify exactly one of --id or --path%s", label)
	}
	if itemID != "" {
		return graph.ItemID(itemID), nil
	}
	return graph.ItemPath(itemPath), nil
}
