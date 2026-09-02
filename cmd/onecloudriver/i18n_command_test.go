package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/frosado/onecloudriver/internal/i18n"
)

// TestI18n_CommandOutputSwitchesLocale verifies that a command's user-facing
// output follows the active locale (issue #30). The same command (list on an
// empty root) renders the empty message in Spanish under i18n.Init("es") and
// in English under i18n.Init("en"), exercising the L()/Ld() wiring end-to-end
// at the command layer rather than only the catalog lookup.
func TestI18n_CommandOutputSwitchesLocale(t *testing.T) {
	// TestMain pins "en"; restore it so later tests are unaffected.
	defer i18n.Init("en")

	cmd, server := setupGraphCommand(t, "list", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jsonChildren(nil))
	}))
	defer server.Close()
	setCommandFlags(t, cmd, map[string]string{"account": "test@outlook.com"})

	t.Run("spanish", func(t *testing.T) {
		i18n.Init("es")
		out := captureStdoutForTest(t, func() error { return cmd.RunE(cmd, nil) })
		if !strings.Contains(out, "La carpeta raíz está vacía.") {
			t.Errorf("spanish output = %q, want it to contain 'La carpeta raíz está vacía.'", out)
		}
	})

	t.Run("english", func(t *testing.T) {
		i18n.Init("en")
		out := captureStdoutForTest(t, func() error { return cmd.RunE(cmd, nil) })
		if !strings.Contains(out, "The root folder is empty.") {
			t.Errorf("english output = %q, want it to contain 'The root folder is empty.'", out)
		}
	})
}
