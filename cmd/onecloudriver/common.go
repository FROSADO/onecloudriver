package main

import (
	"context"
	"errors"
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

// managerKey is the context key under which the auth.Manager is stored.
// It is a private zero-size type, so it cannot collide with other context
// keys.
type managerKey struct{}

// contextWithManager stores the auth.Manager in ctx.
func contextWithManager(ctx context.Context, mgr *auth.Manager) context.Context {
	return context.WithValue(ctx, managerKey{}, mgr)
}

// getManager returns the auth.Manager from the command context. If the
// context has none (e.g. a RunE invoked without going through rootCmd's
// PersistentPreRun, as in some tests), it returns an error so callers
// are forced to handle the missing dependency explicitly.
func getManager(cmd *cobra.Command) (*auth.Manager, error) {
	mgr, ok := cmd.Context().Value(managerKey{}).(*auth.Manager)
	if !ok || mgr == nil {
		return nil, fmt.Errorf("auth manager not initialized in context")
	}
	return mgr, nil
}

// ──── Flag registration helpers ────
//
// The item commands shared the same flags registered inline with slightly
// different help text. These helpers are the single source of truth for that
// boilerplate (issue #48).

// addAccountFlag registers the canonical --account/-a flag.
func addAccountFlag(cmd *cobra.Command) {
	cmd.Flags().StringP("account", "a", "",
		"Account name to use. If omitted, uses the only configured account.")
}

// addIDPathFlags registers the mutually-exclusive --id/--path pair. The usage
// strings are caller-provided because they legitimately differ per command
// ("ID of the item to delete" vs "ID of the parent folder").
func addIDPathFlags(cmd *cobra.Command, idUsage, pathUsage string) {
	cmd.Flags().String("id", "", idUsage)
	cmd.Flags().String("path", "", pathUsage)
}

// addIDPathFlagsWithShorthand is addIDPathFlags plus the -i/-p shorthands that
// only the info command exposes.
func addIDPathFlagsWithShorthand(cmd *cobra.Command, idUsage, pathUsage string) {
	cmd.Flags().StringP("id", "i", "", idUsage)
	cmd.Flags().StringP("path", "p", "", pathUsage)
}

// addEtagFlag registers --etag (identical across rm, rename and mv).
func addEtagFlag(cmd *cobra.Command) {
	cmd.Flags().String("etag", "", "ETag of the item for concurrency control (optional)")
}

// addDestFlags registers --dest-id/--dest-path (mv and copy).
func addDestFlags(cmd *cobra.Command) {
	cmd.Flags().String("dest-id", "", "ID of the destination folder")
	cmd.Flags().String("dest-path", "", "Path of the destination folder (e.g.: /Archive)")
}

// addOutputFlag registers --output/-o (list and info).
func addOutputFlag(cmd *cobra.Command) {
	cmd.Flags().StringP("output", "o", "yaml", "Output format: text, json, yaml")
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

// resolveAccountFromCmd collapses the repeated getManager + resolveAccount
// preamble into a single call, so each command body only keeps its own logic.
func resolveAccountFromCmd(cmd *cobra.Command) (*auth.Account, error) {
	manager, err := getManager(cmd)
	if err != nil {
		return nil, err
	}
	return resolveAccount(cmd, manager)
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

// resourceFromCmd reads --id/--path and builds the graph.Resource they
// address. It is the single call site for the id/path preamble every item
// command starts with. label gives context in the error message (e.g.
// " for the source").
func resourceFromCmd(cmd *cobra.Command, label string) (graph.Resource, error) {
	r, _, err := resourceFromCmdWithLabel(cmd, label)
	return r, err
}

// resourceFromCmdWithLabel is resourceFromCmd plus the display string of the
// flag that was actually used, for commands that echo the target back to the
// user (rm).
func resourceFromCmdWithLabel(cmd *cobra.Command, label string) (graph.Resource, string, error) {
	itemID, _ := cmd.Flags().GetString("id")
	itemPath, _ := cmd.Flags().GetString("path")

	r, err := buildResource(itemID, itemPath, label)
	if err != nil {
		return nil, "", err
	}
	return r, resourceLabel(itemID, itemPath), nil
}

// destFlagsFromCmd reads the --dest-id/--dest-path pair (mv and copy).
func destFlagsFromCmd(cmd *cobra.Command) (destID, destPath string) {
	destID, _ = cmd.Flags().GetString("dest-id")
	destPath, _ = cmd.Flags().GetString("dest-path")
	return destID, destPath
}

// requiredStringFlag returns the value of a string flag, failing with a
// caller-provided message when it is empty. cobra's MarkFlagRequired only
// rejects a missing flag, not an explicitly empty one (--name "").
func requiredStringFlag(cmd *cobra.Command, name, missingMsg string) (string, error) {
	value, _ := cmd.Flags().GetString(name)
	if value == "" {
		return "", errors.New(missingMsg)
	}
	return value, nil
}

// buildDestResource builds a graph.Resource for a required destination from
// exactly one of destID or destPath (mv). It reuses validateDestFlags so the
// "exactly one of" validation and the resource construction stay in sync.
func buildDestResource(destID, destPath string) (graph.Resource, error) {
	if err := validateDestFlags(destID, destPath); err != nil {
		return nil, err
	}
	if destID != "" {
		return graph.ItemID(destID), nil
	}
	return graph.ItemPath(destPath), nil
}

// buildOptionalDestResource builds a graph.Resource for an optional
// destination (copy, where --name alone is valid). It returns (nil, nil) when
// neither --dest-id nor --dest-path is given.
func buildOptionalDestResource(destID, destPath string) (graph.Resource, error) {
	if err := validateOptionalDestFlags(destID, destPath); err != nil {
		return nil, err
	}
	if destID == "" && destPath == "" {
		return nil, nil
	}
	if destID != "" {
		return graph.ItemID(destID), nil
	}
	return graph.ItemPath(destPath), nil
}

// buildOptionalResource builds a graph.Resource from at most one of itemID or
// itemPath. It returns (nil, nil) when neither is provided, so callers can
// fall back to a default target (list uses the root folder). Both flags
// together are an error: they are mutually exclusive. The error wording
// reuses buildResource's so tests can assert on the same message.
func buildOptionalResource(itemID, itemPath string) (graph.Resource, error) {
	if itemID != "" && itemPath != "" {
		return nil, fmt.Errorf("you must specify exactly one of --id or --path")
	}
	if itemID == "" && itemPath == "" {
		// Return a nil interface, not a zero ItemPath: graph.ItemPath("")
		// inside a Resource interface is non-nil, and callers check r == nil
		// to decide whether to fall back to the root folder.
		return nil, nil
	}
	if itemID != "" {
		return graph.ItemID(itemID), nil
	}
	return graph.ItemPath(itemPath), nil
}

// resourceLabel returns the non-empty id/path for display, assuming
// buildResource already validated that exactly one of them is set.
func resourceLabel(itemID, itemPath string) string {
	if itemID != "" {
		return itemID
	}
	return itemPath
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

// resolveFormatter resolves the --output flag to a Formatter. list and info
// call it before any Graph request so an invalid --output fails fast.
func resolveFormatter(cmd *cobra.Command) (Formatter, error) {
	format, _ := cmd.Flags().GetString("output")
	return getFormatter(format)
}

// formatOutput renders v with the given formatter and prints it to stdout. It
// is the single call site for the FormatX → fmt.Print pattern shared by list
// and info.
func formatOutput(formatter Formatter, v any) error {
	var out string
	var err error
	switch vv := v.(type) {
	case []graph.DriveItem:
		out, err = formatter.FormatDriveItems(vv)
	case *graph.DriveItem:
		out, err = formatter.FormatDriveItem(vv)
	default:
		return fmt.Errorf("unsupported output type %T", v)
	}
	if err != nil {
		return err
	}

	fmt.Print(out)
	return nil
}
