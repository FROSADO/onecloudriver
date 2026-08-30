package i18n

import (
	"os"
	"strings"

	"golang.org/x/text/language"
)

// ParseLocale normalizes a POSIX locale ("es_ES.UTF-8") to a BCP-47 base
// language ("es") and returns it. It is a pure function (no environment or
// global state), so it can be table-tested without touching real env vars.
//
// Handles:
//   - empty, "C" and "POSIX" (undefined locale) → "en" (default)
//   - encoding and modifier suffixes: "es_ES.UTF-8@euro" → "es"
//   - underscore separators: "es_ES" → "es-ES" → base "es"
//   - unparseable input → "en"
func ParseLocale(locale string) string {
	locale = strings.TrimSpace(locale)
	if locale == "" || locale == "C" || locale == "POSIX" {
		return "en"
	}

	// "es_ES.UTF-8@euro" -> quitar el modificador @... y la codificación .UTF-8
	locale = strings.SplitN(locale, "@", 2)[0]
	locale = strings.SplitN(locale, ".", 2)[0]
	locale = strings.ReplaceAll(locale, "_", "-")

	tag, err := language.Parse(locale)
	if err != nil {
		return "en"
	}
	base, _ := tag.Base() // solo el idioma base: "es-ES" -> "es"
	return base.String()
}

// DetectLanguage returns the user's language from the POSIX environment, in
// priority order: LC_ALL > LC_MESSAGES > LANG. A locale that parses to "en"
// (e.g. "C.UTF-8") does not stop the search, so a later variable with a
// real language still wins. Falls back to "en" when nothing is set.
func DetectLanguage() string {
	for _, env := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(env); v != "" {
			if lang := ParseLocale(v); lang != "en" {
				return lang
			}
		}
	}
	return "en"
}
