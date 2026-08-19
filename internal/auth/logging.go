package auth

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// SetLogOutput redirects zerolog's global logs to the provided writer.
// Use it to send logs to a file instead of the console.
func SetLogOutput(w io.Writer) {
	// Replace the global logger with one that writes to the new writer
	log.Logger = zerolog.New(w).With().Timestamp().Logger()
}

const DefaultLogLevel = "info"

// SetLogLevel configures zerolog's global minimum level. Levels below the
// configured threshold are discarded before they are serialized or written.
func SetLogLevel(level string) error {
	level = strings.ToLower(strings.TrimSpace(level))
	parsed, err := zerolog.ParseLevel(level)
	if err != nil || parsed > zerolog.ErrorLevel {
		return fmt.Errorf("unsupported log level %q (valid: trace, debug, info, warn, error)", level)
	}
	zerolog.SetGlobalLevel(parsed)
	return nil
}

// InitLogging configures the full logging system with the production default
// of Info and above:
// - Logs go to a file in JSON format (for debugging)
// - Trace and Debug records are discarded by default
// - The console stays silent (only explicit CLI Printf calls are shown)
func InitLogging() error {
	return InitLoggingWithLevel(DefaultLogLevel)
}

// InitLoggingWithLevel configures the logging system using a caller-selected
// minimum level. The level is applied before opening the log file so invalid
// configuration never leaves the process at zerolog's more verbose default.
func InitLoggingWithLevel(level string) error {
	if err := SetLogLevel(level); err != nil {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
		return err
	}

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
