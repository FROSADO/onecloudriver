# API: internal/printer

> Auto-generated with `go doc -all`. Date: 2026-08-14 00:40:10

```
package printer // import "github.com/frosado/onecloudriver/internal/printer"

Package printer provides environment-aware output symbols: emoji when writing to
a terminal, ASCII fallbacks when the output is piped, redirected, captured by a
journal, or rendered in a non-unicode terminal.

VARIABLES

var (
	Rocket    = symbolFor(isTTY(), "🚀", "[*]")
	Folder    = symbolFor(isTTY(), "📁", "[D]")
	Clock     = symbolFor(isTTY(), "⏱️", "[T]")
	Refresh   = symbolFor(isTTY(), "🔄", "[R]")
	Disk      = symbolFor(isTTY(), "💾", "[S]")
	Unplug    = symbolFor(isTTY(), "🔌", "[-]")
	Success   = symbolFor(isTTY(), "✅", "OK")
	Warning   = symbolFor(isTTY(), "⚠️", "WARN")
	Info      = symbolFor(isTTY(), "ℹ️", "INFO")
	Error     = symbolFor(isTTY(), "❌", "ERR")
	Stop      = symbolFor(isTTY(), "🛑", "[!]")
	Clipboard = symbolFor(isTTY(), "📋", "[i]")
	Globe     = symbolFor(isTTY(), "🌐", "[web]")
	Hourglass = symbolFor(isTTY(), "⏳", "[wait]")
)
    Output symbols. Each one is selected once at package init based on whether
    stdout is a terminal: emoji for interactive use, ASCII for everything
    else (pipes, redirection, systemd journal, screen readers, non-unicode
    terminals).

```
