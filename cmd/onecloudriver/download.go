package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/spf13/cobra"
)

var downloadCmd = &cobra.Command{
	Use:   "download",
	Short: "Download a file from OneDrive to local disk",
	Long: `Downloads a file from OneDrive to local disk using streaming with support
for large files (>10 MB) via Range requests.

The file must be specified by ID or by path (not both), and the destination:

  onecloudriver download --account user@mail.com --id 01BYE5RZ... --output ./file.pdf
  onecloudriver download --account user@mail.com --path /Documents/photo.jpg -o ./photo.jpg
  onecloudriver download --account user@mail.com --id 01BYE5RZ... --output-dir ./downloads`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		acc, err := resolveAccount(cmd, manager)
		if err != nil {
			return err
		}

		outputPath, _ := cmd.Flags().GetString("output")
		outputDir, _ := cmd.Flags().GetString("output-dir")

		if (outputPath == "" && outputDir == "") || (outputPath != "" && outputDir != "") {
			return fmt.Errorf("you must specify exactly one of --output or --output-dir")
		}

		itemID, _ := cmd.Flags().GetString("id")
		itemPath, _ := cmd.Flags().GetString("path")

		if (itemID == "" && itemPath == "") || (itemID != "" && itemPath != "") {
			return fmt.Errorf("you must specify exactly one of --id or --path")
		}
		graphClient := graph.NewClient()

		r := graph.Resource(graph.ItemPath(itemPath))
		if itemID != "" {
			r = graph.ItemID(itemID)
		}

		// If --output-dir is used, fetch metadata to know the original name
		if outputDir != "" {
			item, err := graphClient.GetItem(cmd.Context(), acc, r)
			if err != nil {
				return fmt.Errorf("error fetching metadata: %w", err)
			}

			if item.IsFolder() {
				return fmt.Errorf("cannot download a folder: %s", item.Name)
			}

			if item.Name == "" {
				return fmt.Errorf("the item has no name (possibly the root)")
			}

			// security (CWE-22, path traversal): item.Name comes from the
			// metadata returned by Microsoft Graph for the remote DriveItem.
			// Although normally trusted (it's the authenticated user's own
			// drive), OneDrive allows sharing folders between accounts: a
			// file with write permissions granted by another user could have
			// a malicious name like "../../../../etc/cron.d/evil". Without
			// this validation, filepath.Join(outputDir, item.Name) can
			// escape outputDir (confirmed: Join with "../../tmp/x" produces
			// a path outside the destination directory). We reject any name
			// that is not a simple local path component before constructing
			// the write path.
			if !filepath.IsLocal(item.Name) {
				return fmt.Errorf("unsafe remote filename, download aborted: %q", item.Name)
			}

			outputPath = filepath.Join(outputDir, item.Name)
		}

		// Create output file. gosec (G304) flags this line, but
		// when outputPath was derived from item.Name (--output-dir branch)
		// it was validated above with filepath.IsLocal; when it comes
		// directly from --output, the user is choosing where to write on
		// their own local filesystem, with no trust boundary crossing.
		file, err := os.Create(outputPath) //nolint:gosec // G304: outputPath validated with filepath.IsLocal if from item.Name; if from --output, it's the user's own choice
		if err != nil {
			return fmt.Errorf("error creating output file: %w", err)
		}
		defer file.Close()

		fmt.Fprintf(cmd.ErrOrStderr(), "Downloading '%s'...\n", outputPath)

		n, err := graphClient.GetItemContentStream(cmd.Context(), acc, r, file)
		if err != nil {
			// Clean up partial file on error
			if rmErr := os.Remove(outputPath); rmErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not clean up partial file %s: %v\n", outputPath, rmErr)
			}
			return fmt.Errorf("error downloading: %w", err)
		}

		fmt.Fprintf(cmd.ErrOrStderr(), "Download completed: %s (%d bytes)\n", outputPath, n)

		return nil
	},
}

// registerDownloadCmd adds the download command's flags and registers it in root.
func registerDownloadCmd(root *cobra.Command) {
	downloadCmd.Flags().StringP("account", "a", "", "Account name to query. If omitted, uses the only configured account.")
	downloadCmd.Flags().String("id", "", "ID of the DriveItem to download")
	downloadCmd.Flags().String("path", "", "Path of the DriveItem to download (e.g.: /Documents/photo.jpg)")
	downloadCmd.Flags().StringP("output", "o", "", "Local path where to save the downloaded file")
	downloadCmd.Flags().StringP("output-dir", "d", "", "Directory where to save the file with its original name")

	root.AddCommand(downloadCmd)
}
