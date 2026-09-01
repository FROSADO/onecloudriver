package main

import (
	"context"
	"fmt"

	"github.com/frosado/onecloudriver/internal/auth"
	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/frosado/onecloudriver/internal/i18n"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List the contents of a OneDrive folder (root by default)",
	Long: `Lists the files and folders of a OneDrive folder.
With no flags it lists the root folder. Use --id or --path (not both) to
target an arbitrary folder:

  onecloudriver list --account user@mail.com --path /Documents
  onecloudriver list --account user@mail.com --id 01BYE5RZ...`,
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
		r, err := buildOptionalResource(itemID, itemPath)
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", i18n.Ld("cmd.common.querying", map[string]any{"Account": acc.Name}))
		graphClient := getClient(cmd)

		var items []graph.DriveItem
		if r == nil {
			items, err = graphClient.ListDriveRoot(cmd.Context(), acc)
		} else {
			items, err = listFolderContents(cmd.Context(), graphClient, acc, r)
		}
		if err != nil {
			return fmt.Errorf("error listing files: %w", err)
		}

		if len(items) == 0 {
			if r == nil {
				fmt.Println(i18n.L("cmd.list.empty.root"))
			} else {
				fmt.Println(i18n.L("cmd.list.empty.folder"))
			}
			return nil
		}

		return formatOutput(formatter, items)
	},
}

// listFolderContents lists the children of the folder addressed by r. If r
// points to a file, it lists the folder that contains it instead (the user
// asked for the location of that file, so the siblings are the useful view).
func listFolderContents(ctx context.Context, graphClient *graph.Client, acc *auth.Account, r graph.Resource) ([]graph.DriveItem, error) {
	item, err := graphClient.GetItem(ctx, acc, r)
	if err != nil {
		return nil, err
	}

	if item.IsFolder() {
		return graphClient.ListChildren(ctx, acc, graph.ItemID(item.ID))
	}

	if item.Parent == nil || item.Parent.ID == "" {
		return nil, fmt.Errorf("cannot determine the parent folder of %q", item.Name)
	}
	return graphClient.ListChildren(ctx, acc, graph.ItemID(item.Parent.ID))
}

// registerListCmd adds the list command's flags and registers it in root.
func registerListCmd(root *cobra.Command) {
	addAccountFlag(listCmd)
	addIDPathFlags(listCmd,
		"ID of the folder to list (default: root)",
		"Path of the folder to list (default: root, e.g.: /Documents)")
	addOutputFlag(listCmd)

	root.AddCommand(listCmd)
}
