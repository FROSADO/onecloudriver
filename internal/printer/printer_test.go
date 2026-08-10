package printer

import "testing"

func TestSymbolFor(t *testing.T) {
	tests := []struct {
		name  string
		tty   bool
		emoji string
		ascii string
		want  string
	}{
		{name: "tty uses emoji", tty: true, emoji: "🚀", ascii: "[*]", want: "🚀"},
		{name: "non-tty uses ascii", tty: false, emoji: "🚀", ascii: "[*]", want: "[*]"},
		{name: "tty success", tty: true, emoji: "✅", ascii: "OK", want: "✅"},
		{name: "non-tty success", tty: false, emoji: "✅", ascii: "OK", want: "OK"},
		{name: "tty error", tty: true, emoji: "❌", ascii: "ERR", want: "❌"},
		{name: "non-tty error", tty: false, emoji: "❌", ascii: "ERR", want: "ERR"},
		{name: "tty stop", tty: true, emoji: "🛑", ascii: "[!]", want: "🛑"},
		{name: "non-tty stop", tty: false, emoji: "🛑", ascii: "[!]", want: "[!]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := symbolFor(tt.tty, tt.emoji, tt.ascii); got != tt.want {
				t.Errorf("symbolFor(%v, %q, %q) = %q, want %q", tt.tty, tt.emoji, tt.ascii, got, tt.want)
			}
		})
	}
}

// TestSymbolsNeverEmpty guards against accidentally empty fallbacks: every
// exported symbol must be non-empty regardless of the environment.
func TestSymbolsNeverEmpty(t *testing.T) {
	symbols := []string{Rocket, Folder, Clock, Refresh, Disk, Unplug, Success, Warning, Info, Error, Stop, Clipboard, Globe, Hourglass}
	for i, s := range symbols {
		if s == "" {
			t.Fatalf("symbol index %d is empty", i)
		}
	}
}
