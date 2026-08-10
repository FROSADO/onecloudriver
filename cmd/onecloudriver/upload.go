package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/spf13/cobra"
)

var uploadCmd = &cobra.Command{
	Use:   "upload",
	Short: "Upload a local file to OneDrive",
	Long: `Uploads a file from the local disk to OneDrive.

For small files (≤4 MB) it uses a simple PUT request.
For large files (>4 MB) it uses upload sessions with 320 KiB chunks.

The destination folder is specified by ID or by path:

  onecloudriver upload --account user@mail.com --id FOLDER123 --file ./photo.jpg
  onecloudriver upload --account user@mail.com --path /Documents --file ./document.pdf`,

	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		acc, err := resolveAccount(cmd, manager)
		if err != nil {
			return err
		}

		itemID, _ := cmd.Flags().GetString("id")
		itemPath, _ := cmd.Flags().GetString("path")

		r, err := buildResource(itemID, itemPath, " for the destination folder")
		if err != nil {
			return err
		}

		filePath, _ := cmd.Flags().GetString("file")
		if filePath == "" {
			return fmt.Errorf("you must specify the local file with --file")
		}
		// security: gosec (G304) flags this open as "file inclusion
		// via variable". filePath comes from the --file flag that the
		// user running the command specifies for their own local
		// filesystem: it does not cross a trust boundary (the user already
		// has read permissions on whatever --file points to; there is no
		// way for this flag to grant access to something they didn't
		// already have).
		file, err := os.Open(filePath) //nolint:gosec // G304: filePath is the user's own --file flag on their local filesystem
		if err != nil {
			return fmt.Errorf("error opening local file: %w", err)
		}
		defer file.Close()

		stat, err := file.Stat()
		if err != nil {
			return fmt.Errorf("error getting file information: %w", err)
		}

		fileName := filepath.Base(filePath)
		graphClient := graph.NewClient()

		var uploaded *graph.DriveItem
		if stat.Size() > 4*1024*1024 {
			fmt.Fprintf(cmd.ErrOrStderr(), "Uploading '%s' (%d bytes) via upload session...\n", filePath, stat.Size())
			uploaded, err = graphClient.UploadItemStream(cmd.Context(), acc, r, fileName, file, stat.Size())
		} else {
			fmt.Fprintf(cmd.ErrOrStderr(), "Uploading '%s' (%d bytes)...\n", filePath, stat.Size())
			uploaded, err = graphClient.UploadItem(cmd.Context(), acc, r, fileName, file)
		}
		if err != nil {
			return fmt.Errorf("error uploading: %w", err)
		}

		fmt.Printf("File uploaded: %s (ID: %s, %d bytes)\n", uploaded.Name, uploaded.ID, uploaded.Size)

		return nil
	},
}

// registerUploadCmd adds the upload command's flags and registers it in root.
func registerUploadCmd(root *cobra.Command) {
	uploadCmd.Flags().StringP("account", "a", "", "Account name to use. If omitted, uses the only configured account.")
	uploadCmd.Flags().String("id", "", "ID of the destination folder")
	uploadCmd.Flags().String("path", "", "Path of the destination folder (e.g.: /Documents)")
	uploadCmd.Flags().StringP("file", "f", "", "Path of the local file to upload (required)")
	uploadCmd.MarkFlagRequired("file")

	root.AddCommand(uploadCmd)
}
