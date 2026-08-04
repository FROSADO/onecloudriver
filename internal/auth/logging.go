package auth

import (
	"io"
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// SetLogOutput redirects zerolog's global logs to the provided writer.
// Use it to send logs to a file instead of the console.
func SetLogOutput(w io.Writer) {
	// Replace the global logger with one that writes to the new writer
	log.Logger = zerolog.New(w).With().Timestamp().Logger()
}

// InitLogging configures the full logging system:
// - Logs go to a file in JSON format (for debugging)
// - The console stays silent (only explicit CLI Printf calls are shown)
func InitLogging() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}

	logDir := configDir + "/onecloudriver"
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return err
	}

	// security: gosec (G304) flags this open as "file inclusion via
	// variable", but the path is built entirely from
	// os.UserConfigDir() (the standard OS configuration directory
	// for the current user) plus a fixed constant ("/onecloudriver/
	// onecloudriver.log"). There is no external or user input in this
	// path, so it does not cross a trust boundary.
	logFile, err := os.OpenFile( //nolint:gosec // G304: path composed only of os.UserConfigDir() + constant, no external input
		logDir+"/onecloudriver.log",
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0600,
	)
	if err != nil {
		return err
	}

	// Redirect all zerolog logs to the file
	SetLogOutput(logFile)
	return nil
}

// DiscardLogs completely silences zerolog logs (useful for tests)
func DiscardLogs() {
	log.Logger = zerolog.Nop()
}
