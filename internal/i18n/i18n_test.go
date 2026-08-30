package i18n

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// idPattern matches every L("...")/Ld("...") call in the source tree.
// The id must start with a letter: namespaced ids look like
// "cmd.list.empty.root", and this also skips doc comments that literally
// mention L("...").
var idPattern = regexp.MustCompile(`\bL(?:d)?\("([a-z][a-z0-9.]*)"`)

func TestParseLocale(t *testing.T) {
	tests := []struct {
		name   string
		locale string
		want   string
	}{
		{name: "empty", locale: "", want: "en"},
		{name: "C", locale: "C", want: "en"},
		{name: "POSIX", locale: "POSIX", want: "en"},
		{name: "spanish full", locale: "es_ES.UTF-8", want: "es"},
		{name: "spanish bare", locale: "es", want: "es"},
		{name: "spanish with modifier", locale: "es_ES@euro", want: "es"},
		{name: "english US", locale: "en_US.UTF-8", want: "en"},
		{name: "french", locale: "fr_FR.UTF-8", want: "fr"},
		{name: "german", locale: "de_DE", want: "de"},
		{name: "whitespace", locale: "  es_ES.UTF-8  ", want: "es"},
		{name: "garbage", locale: "xyz123", want: "en"},
		{name: "c utf8", locale: "C.UTF-8", want: "en"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseLocale(tt.locale); got != tt.want {
				t.Errorf("ParseLocale(%q) = %q, want %q", tt.locale, got, tt.want)
			}
		})
	}
}

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "nothing set", env: nil, want: "en"},
		{name: "only LANG", env: map[string]string{"LANG": "es_ES.UTF-8"}, want: "es"},
		{name: "LC_ALL wins", env: map[string]string{"LC_ALL": "fr_FR.UTF-8", "LANG": "es_ES.UTF-8"}, want: "fr"},
		{name: "LC_MESSAGES beats LANG", env: map[string]string{"LC_MESSAGES": "de_DE", "LANG": "es_ES"}, want: "de"},
		{name: "C locale falls through to LANG", env: map[string]string{"LC_ALL": "C.UTF-8", "LANG": "es_ES.UTF-8"}, want: "es"},
		{name: "english env", env: map[string]string{"LANG": "en_US.UTF-8"}, want: "en"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
				os.Unsetenv(k)
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if got := DetectLanguage(); got != tt.want {
				t.Errorf("DetectLanguage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLocalize_EnglishAndSpanish(t *testing.T) {
	t.Run("spanish", func(t *testing.T) {
		Init("es")
		if got := L("cmd.list.empty.root"); got != "La carpeta raíz está vacía." {
			t.Errorf("L(es) = %q", got)
		}
	})

	t.Run("english", func(t *testing.T) {
		Init("en")
		if got := L("cmd.list.empty.root"); got != "The root folder is empty." {
			t.Errorf("L(en) = %q", got)
		}
	})

	t.Run("unknown language falls back to default", func(t *testing.T) {
		Init("fr") // sin catálogo: go-i18n cae al idioma por defecto del bundle (en)
		if got := L("cmd.list.empty.root"); got != "The root folder is empty." {
			t.Errorf("L(fr fallback) = %q", got)
		}
	})

	t.Run("template data", func(t *testing.T) {
		Init("es")
		got := Ld("err.account.notfound", map[string]any{"Name": "x@y.com"})
		if got != "la cuenta 'x@y.com' no existe" {
			t.Errorf("Ld(es) = %q", got)
		}
		Init("en")
		got = Ld("err.account.notfound", map[string]any{"Name": "x@y.com"})
		if got != "account 'x@y.com' does not exist" {
			t.Errorf("Ld(en) = %q", got)
		}
	})

	t.Run("uninitialized returns the id", func(t *testing.T) {
		l = nil // simula un Init() que nunca se llamó
		if got := L("cmd.list.empty.root"); got != "cmd.list.empty.root" {
			t.Errorf("L(no init) = %q, want the id itself", got)
		}
		if got := Ld("err.account.notfound", map[string]any{"Name": "x"}); got != "err.account.notfound" {
			t.Errorf("Ld(no init) = %q, want the id itself", got)
		}
		Init("en") // restaurar para el resto de tests
	})
}

// TestCatalogCompleteness guarantees that MustLocalize can never panic on a
// missing id: every id referenced in the source exists in en.json, and the
// en/es key sets are identical.
func TestCatalogCompleteness(t *testing.T) {
	used := collectMessageIDs(t)
	enIDs := catalogKeys(t, "en.json")
	esIDs := catalogKeys(t, "es.json")

	for _, id := range used {
		if _, ok := enIDs[id]; !ok {
			t.Errorf("message id %q is used in code but missing from locales/en.json", id)
		}
	}
	for id := range enIDs {
		if _, ok := esIDs[id]; !ok {
			t.Errorf("message id %q is in en.json but missing from es.json", id)
		}
	}
	for id := range esIDs {
		if _, ok := enIDs[id]; !ok {
			t.Errorf("message id %q is in es.json but missing from en.json", id)
		}
	}
}

// collectMessageIDs greps cmd/ and internal/ for L("...")/Ld("...") calls.
func collectMessageIDs(t *testing.T) []string {
	t.Helper()
	var ids []string
	root := filepath.Join("..", "..")
	for _, dir := range []string{"cmd", "internal"} {
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range idPattern.FindAllSubmatch(data, -1) {
				ids = append(ids, string(m[1]))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	return ids
}

// catalogKeys returns the set of top-level message ids in a locale file.
func catalogKeys(t *testing.T, name string) map[string]bool {
	t.Helper()
	data, err := localeFS.ReadFile("locales/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var catalog map[string]json.RawMessage
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	keys := make(map[string]bool, len(catalog))
	for id := range catalog {
		keys[id] = true
	}
	return keys
}
