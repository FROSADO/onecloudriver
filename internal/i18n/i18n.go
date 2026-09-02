// Package i18n provides locale detection and a message catalog for the
// onecloudriver CLI. All user-facing strings go through L()/Ld() with a
// stable dotted id ("cmd.list.empty.root"), so the full catalog is greppable:
// `grep -rn 'L("' cmd/ internal/` lists every translatable message.
//
// English is the bundle default (fallback); Spanish is the first translation
// target, mirroring the bilingual docs (docs/*.es.md).
package i18n

import (
	"embed"
	"encoding/json"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

//go:embed locales/*.json
var localeFS embed.FS

var (
	bundle *i18n.Bundle
	l      *i18n.Localizer
)

// init builds the bundle once and loads every embedded catalog. The default
// language is English, so any requested language without a catalog (or an
// unparseable one) falls back to English.
func init() {
	bundle = i18n.NewBundle(language.English)
	bundle.RegisterUnmarshalFunc("json", json.Unmarshal)

	entries, err := localeFS.ReadDir("locales")
	if err != nil {
		panic("i18n: cannot read embedded locales: " + err.Error())
	}
	for _, e := range entries {
		data, err := localeFS.ReadFile("locales/" + e.Name())
		if err != nil {
			panic("i18n: cannot read embedded locale " + e.Name() + ": " + err.Error())
		}
		if _, err := bundle.ParseMessageFileBytes(data, e.Name()); err != nil {
			panic("i18n: cannot parse locale " + e.Name() + ": " + err.Error())
		}
	}
}

// Init fixes the language for the current process. lang is a BCP-47 tag
// ("en", "es", "es-ES"...); languages without a catalog fall back to the
// bundle default (English). Call it once at startup, before any command runs.
func Init(lang string) {
	l = i18n.NewLocalizer(bundle, lang)
}

// L returns the translated message for id. If Init was never called it
// returns the id itself (debug-friendly), so callers never panic.
func L(id string) string {
	if l == nil {
		return id
	}
	return l.MustLocalize(&i18n.LocalizeConfig{MessageID: id})
}

// Ld is L with template data: the message can reference {{.Field}} placeholders
// (see the JSON catalogs). Example: Ld("err.account.notfound", map[string]any{"Name": n}).
func Ld(id string, data map[string]any) string {
	if l == nil {
		return id
	}
	return l.MustLocalize(&i18n.LocalizeConfig{MessageID: id, TemplateData: data})
}
