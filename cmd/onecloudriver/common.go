package main

import (
	"context"
	"fmt"

	"github.com/frosado/onecloudriver/internal/auth"
	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/spf13/cobra"
)

// clientKey is the context key under which the shared graph.Client is stored.
// It is a private zero-size type, so it cannot collide with other context
// keys.
type clientKey struct{}

// contextWithClient stores the shared graph.Client in ctx.
func contextWithClient(ctx context.Context, c *graph.Client) context.Context {
	return context.WithValue(ctx, clientKey{}, c)
}

// getClient returns the shared graph.Client from the command context. If the
// context has none (e.g. a RunE invoked without going through rootCmd's
// PersistentPreRun, as in some tests), it falls back to a fresh client so
// callers always get a usable *graph.Client.
func getClient(cmd *cobra.Command) *graph.Client {
	if c, ok := cmd.Context().Value(clientKey{}).(*graph.Client); ok && c != nil {
		return c
	}
	return graph.NewClient()
}

// ResolveAccountName resolves the account name from the command flags (--account)
// or auto detect the default account in the auth manager.
func resolveAccountName(cmd *cobra.Command, manager *auth.Manager) (string, error) {
	accountName, _ := cmd.Flags().GetString("account")

	if accountName != "" {
		return accountName, nil
	}

	accountName, err := manager.ResolveMainAccountName()
	if err != nil {
		return "", fmt.Errorf("you must specify an account with --account")
	}
	fmt.Printf("Using the only default account '%s'\n", accountName)
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

// validateOutputFlags returns an error when neither or both of --output and
// --output-dir are provided: the flags are mutually exclusive and exactly one
// of them is required.
func validateOutputFlags(outputPath, outputDir string) error {
	if (outputPath == "" && outputDir == "") || (outputPath != "" && outputDir != "") {
		return fmt.Errorf("you must specify exactly one of --output or --output-dir")
	}
	return nil
}

// validateDestFlags returns an error when neither or both of --dest-id and
// --dest-path are provided: the flags are mutually exclusive and exactly one
// of them is required to name the destination folder.
func validateDestFlags(destID, destPath string) error {
	if (destID == "" && destPath == "") || (destID != "" && destPath != "") {
		return destFlagsError()
	}
	return nil
}

// validateOptionalDestFlags returns an error when both --dest-id and
// --dest-path are provided together: the destination is optional (copy allows
// --name instead), but the two flags remain mutually exclusive.
func validateOptionalDestFlags(destID, destPath string) error {
	if destID != "" && destPath != "" {
		return destFlagsError()
	}
	return nil
}

// destFlagsError builds the standardized error message for the
// --dest-id/--dest-path flag pair.
func destFlagsError() error {
	return fmt.Errorf("you must specify exactly one of --dest-id or --dest-path for the destination")
}
