package main

import (
	"fmt"
	"os"

	"github.com/frosado/onecloudriver/internal/auth"
	"github.com/spf13/cobra"
)

var (
	manager *auth.Manager
)

var rootCmd = &cobra.Command{
	Use:   "onecloudriver",
	Short: "Native filesystem for OneDrive on Linux",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if err := auth.InitLogging(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not initialize logging: %v\n", err)
		}

		var err error
		manager, err = auth.NewManager()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Critical error initializing the manager: %v\n", err)
			os.Exit(1)
		}
	},
}

// init registers all commands by calling each file's register function.
// This way each command owns its own flags, keeping main.go clean and readable.
func init() {
	registerAccountCmd(rootCmd)
	registerMountCmd(rootCmd)
	registerServiceCmd(rootCmd)
	registerListCmd(rootCmd)
	registerInfoCmd(rootCmd)
	registerDownloadCmd(rootCmd)
	registerMkdirCmd(rootCmd)
	registerRmCmd(rootCmd)
	registerRenameCmd(rootCmd)
	registerMvCmd(rootCmd)
	registerCopyCmd(rootCmd)
	registerUploadCmd(rootCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
