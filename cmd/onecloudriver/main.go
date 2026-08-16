package main

import (
	"fmt"
	"os"

	"github.com/frosado/onecloudriver/internal/auth"
	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/spf13/cobra"
)

var (
	manager *auth.Manager

	// version is injected at build time via -ldflags "-X main.version=..."
	// (see Makefile: make build). It defaults to "dev" for ad-hoc builds.
	version = "dev"
)

var rootCmd = &cobra.Command{
	Use:     "onecloudriver",
	Short:   "Native filesystem for OneDrive on Linux",
	Version: version,
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

		// Create the shared Graph client once and make it available to every
		// command through the context (issue #10).
		cmd.SetContext(contextWithClient(cmd.Context(), graph.NewClient()))
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
