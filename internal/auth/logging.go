package auth

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gopkg.in/natefinch/lumberjack.v2"
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

	// The on-disk log is size-based rotated so it never grows unbounded
	// (reported by a user on 0.1.3: the file kept growing with Debug logs
	// that the default Info level did not yet filter). lumberjack is a
	// single well-known writer: 10MB per file, at most 5 gzipped backups,
	// 30 days retention.
	logFile := newRotatingLogFile(logDir)

	// Redirect all zerolog logs to the rotated file
	SetLogOutput(logFile)
	return nil
}

// newRotatingLogFile returns the size-based rotated writer for the on-disk
// log (10MB per file, at most 5 gzipped backups, 30 days retention). The path
// is built entirely from the caller-provided log directory plus a fixed
// constant, so there is no external or user input crossing a trust boundary
// (gosec G304 does not apply to a lumberjack filename for the same reason).
func newRotatingLogFile(logDir string) *lumberjack.Logger {
	return &lumberjack.Logger{
		Filename:   filepath.Join(logDir, "onecloudriver.log"),
		MaxSize:    10, // MB per file
		MaxBackups: 5,  // keep at most 5 rotated files
		MaxAge:     30, // days
		Compress:   true,
	}
}

// DiscardLogs completely silences zerolog logs (useful for tests)
func DiscardLogs() {
	log.Logger = zerolog.Nop()
}
