// Package printer provides environment-aware output symbols: emoji when
// writing to a terminal, ASCII fallbacks when the output is piped, redirected,
// captured by a journal, or rendered in a non-unicode terminal.
package printer

import (
	"os"

	"github.com/mattn/go-isatty"
)

// Output symbols. Each one is selected once at package init based on whether
// stdout is a terminal: emoji for interactive use, ASCII for everything else
// (pipes, redirection, systemd journal, screen readers, non-unicode terminals).
var (
	Rocket  = symbolFor(isTTY(), "🚀", "[*]")
	Folder  = symbolFor(isTTY(), "📁", "[D]")
	Clock   = symbolFor(isTTY(), "⏱️", "[T]")
	Refresh = symbolFor(isTTY(), "🔄", "[R]")
	Disk    = symbolFor(isTTY(), "💾", "[S]")
	Unplug  = symbolFor(isTTY(), "🔌", "[-]")
	Success = symbolFor(isTTY(), "✅", "OK")
	Warning = symbolFor(isTTY(), "⚠️", "WARN")
	Info    = symbolFor(isTTY(), "ℹ️", "INFO")
)

// isTTY reports whether stdout is attached to a terminal.
func isTTY() bool {
	return isatty.IsTerminal(os.Stdout.Fd())
}

// symbolFor returns emoji when tty is true, otherwise the ASCII fallback.
// Kept as a pure function so tests can cover both branches deterministically.
func symbolFor(tty bool, emoji, ascii string) string {
	if tty {
		return emoji
	}
	return ascii
}
