package main

import (
	"testing"

	"github.com/frosado/onecloudriver/internal/i18n"
	"github.com/spf13/cobra"
)

// restoreEnglish re-initializes the locale and re-applies the English
// Short/Long and flag usages, so later tests that assert English text are not
// affected by the Spanish mutations of these tests.
func restoreEnglish() {
	i18n.Init("en")
	localizeCommandTree(rootCmd)
	localizeFlags(rootCmd)
}

func TestResolveLanguage(t *testing.T) {
	t.Run("empty flag falls back to detection", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.Flags().String("lang", "", "")
		// TestMain forces LC_ALL/LANG=C, so detection returns en.
		if got := resolveLanguage(cmd); got != "en" {
			t.Errorf("resolveLanguage() = %q, want en", got)
		}
	})

	t.Run("flag normalized to base tag", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.Flags().String("lang", "", "")
		_ = cmd.Flags().Set("lang", "es_ES.UTF-8")
		if got := resolveLanguage(cmd); got != "es" {
			t.Errorf("resolveLanguage() = %q, want es", got)
		}
	})
}

func TestLocalizeCommandTree(t *testing.T) {
	defer restoreEnglish()

	i18n.Init("es")
	localizeCommandTree(rootCmd)

	if got := rootCmd.Short; got != "Sistema de archivos nativo para OneDrive en Linux" {
		t.Errorf("rootCmd.Short = %q, want Spanish", got)
	}

	list := findSubcommand(rootCmd, "list")
	if got := list.Short; got != "Listar el contenido de una carpeta de OneDrive (raíz por defecto)" {
		t.Errorf("list.Short = %q, want Spanish", got)
	}
	if got := list.Long; got != "Lista los archivos y carpetas de una carpeta de OneDrive.\nSin opciones lista la carpeta raíz. Usa --id o --path (no ambos) para\napuntar a una carpeta arbitraria:\n\n  onecloudriver list --account user@mail.com --path /Documents\n  onecloudriver list --account user@mail.com --id 01BYE5RZ..." {
		t.Errorf("list.Long not localized:\n%s", got)
	}
}

func TestLocalizeFlags(t *testing.T) {
	defer restoreEnglish()

	i18n.Init("es")
	localizeFlags(rootCmd)

	list := findSubcommand(rootCmd, "list")
	if got := list.Flags().Lookup("id").Usage; got != "ID de la carpeta a listar (por defecto: raíz)" {
		t.Errorf("list --id usage = %q, want Spanish", got)
	}
	if got := list.Flags().Lookup("path").Usage; got != "Ruta de la carpeta a listar (por defecto: raíz, p. ej.: /Documents)" {
		t.Errorf("list --path usage = %q, want Spanish", got)
	}

	mount := findSubcommand(rootCmd, "mount")
	if got := mount.Flags().Lookup("cache-dir").Usage; got != "Directorio de caché raíz SOLO para este montaje; no se guarda en la configuración de la cuenta (por defecto: ~/.cache/onecloudriver/<cuenta>)" {
		t.Errorf("mount --cache-dir usage = %q, want Spanish", got)
	}
}
