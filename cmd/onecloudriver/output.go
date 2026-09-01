package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/frosado/onecloudriver/internal/i18n"
	"go.yaml.in/yaml/v3"
)

// Formatter converts Graph data into a textual representation for output.
//
// Each implementation handles a different format (text, JSON, YAML, etc.).
// To add a new format, simply create a new implementation and register it
// in the formatters map.
type Formatter interface {
	FormatDriveItems(items []graph.DriveItem) (string, error)
	FormatDriveItem(item *graph.DriveItem) (string, error)
}

// formatters registers the available format implementations.
// To add a new format (e.g.: YAML), simply implement the Formatter interface
// and register it here.
var formatters = map[string]Formatter{
	"text": &textFormatter{},
	"json": &jsonFormatter{},
	"yaml": &yamlFormatter{},
}

// getFormatter returns the Formatter for the given name.
// Returns an error if the format is not supported.
func getFormatter(name string) (Formatter, error) {
	if err := validateOutputFormat(name); err != nil {
		return nil, err
	}
	return formatters[name], nil
}

// validateOutputFormat reports whether name is a supported output format. It
// is the single source of truth for the canonical "unsupported format" error,
// shared by the DriveItem formatters and the generic service serializer.
func validateOutputFormat(name string) error {
	switch name {
	case "text", "json", "yaml":
		return nil
	default:
		return fmt.Errorf("unsupported format: %q (valid: text, json, yaml)", name)
	}
}

// formatStructuredValue serializes an arbitrary value as JSON or YAML. It is
// intentionally separate from the Formatter interface (which is tied to
// graph.DriveItem): the serialization is identical for every value type, only
// the text rendering differs per command. "text" is not valid here — callers
// must render text themselves.
//
// The returned document always ends with a single trailing newline (json emits
// none and yaml emits one, so both are normalized for clean stdout output).
func formatStructuredValue(format string, v any) (string, error) {
	var out string
	switch format {
	case "json":
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return "", fmt.Errorf("error serializing to JSON: %w", err)
		}
		out = string(b)
	case "yaml":
		b, err := yaml.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("error serializing to YAML: %w", err)
		}
		out = string(b)
	default:
		if format == "text" {
			return "", fmt.Errorf("text output must be rendered by the caller, not formatStructuredValue")
		}
		return "", validateOutputFormat(format)
	}

	return strings.TrimRight(out, "\n") + "\n", nil
}

// --- textFormatter -----------------------------------------------------------

// textFormatter implements Formatter for tabulated plain text output.
type textFormatter struct{}

// FormatDriveItems formats a list of DriveItems as a text table.
func (f *textFormatter) FormatDriveItems(items []graph.DriveItem) (string, error) {
	var b strings.Builder

	fmt.Fprintf(&b, "\n%-7s | %-30s | %-10s | %s\n",
		i18n.L("fmt.list.header.type"), i18n.L("fmt.list.header.name"), i18n.L("fmt.list.header.size"), i18n.L("fmt.list.header.modified"))
	b.WriteString(strings.Repeat("-", 70))
	b.WriteString("\n")

	for _, item := range items {
		typ := i18n.L("fmt.item.file")
		size := fmt.Sprintf("%d B", item.Size)
		if item.IsFolder() {
			typ = i18n.L("fmt.item.folder")
			size = "-"
		}

		fmt.Fprintf(&b, "%-7s | %-30s | %-10s | %s\n", typ, item.Name, size, item.ModTimeString())
	}

	return b.String(), nil
}

// FormatDriveItem formats a single DriveItem as a detailed text block.
func (f *textFormatter) FormatDriveItem(item *graph.DriveItem) (string, error) {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(strings.Repeat("=", 60))
	b.WriteString("\n")
	// The info-block subtitle uses the lowercase kind ("file info"), while
	// the Type field reuses the capital word; ToLower localizes both.
	kind := strings.ToLower(i18n.L("fmt.item.file"))
	if item.IsFolder() {
		kind = strings.ToLower(i18n.L("fmt.item.directory"))
	}
	fmt.Fprintf(&b, "  %s info\n", kind)
	b.WriteString(strings.Repeat("=", 60))
	b.WriteString("\n")
	fmt.Fprintf(&b, "  %s:        %s\n", i18n.L("fmt.info.label.name"), item.Name)
	fmt.Fprintf(&b, "  %s:          %s\n", i18n.L("fmt.info.label.id"), item.ID)

	typ := i18n.L("fmt.item.file")
	size := fmt.Sprintf("%d bytes (%.2f KB)", item.Size, float64(item.Size)/1024)
	if item.IsFolder() {
		typ = i18n.L("fmt.item.folder")
		if item.Folder != nil {
			size = i18n.Ld("fmt.item.elements", map[string]any{"Count": item.Folder.ChildCount})
		} else {
			size = "-"
		}
	}
	fmt.Fprintf(&b, "  %s:        %s\n", i18n.L("fmt.info.label.type"), typ)
	fmt.Fprintf(&b, "  %s:        %s\n", i18n.L("fmt.info.label.size"), size)

	if item.CreatedTime != nil {
		fmt.Fprintf(&b, "  %s:     %s\n", i18n.L("fmt.info.label.created"), item.CreatedTime.Format("2006-01-02 15:04:05"))
	}

	fmt.Fprintf(&b, "  %s:    %s\n", i18n.L("fmt.info.label.modified"), item.ModTimeString())

	if item.File != nil {
		if item.File.Hashes.SHA1Hash != "" {
			fmt.Fprintf(&b, "  %s:        %s\n", i18n.L("fmt.info.label.sha1"), item.File.Hashes.SHA1Hash)
		}
		if item.File.Hashes.QuickXorHash != "" {
			fmt.Fprintf(&b, "  %s:%s\n", i18n.L("fmt.info.label.quickxorhash"), item.File.Hashes.QuickXorHash)
		}
	}

	if item.Parent != nil {
		if item.Parent.DriveType != "" {
			fmt.Fprintf(&b, "  %s:       %s\n", i18n.L("fmt.info.label.drive"), item.Parent.DriveType)
		}
		if item.Parent.ID != "" {
			fmt.Fprintf(&b, "  %s:   %s\n", i18n.L("fmt.info.label.parentid"), item.Parent.ID)
		}
	}

	if item.ETag != "" {
		fmt.Fprintf(&b, "  %s:        %s\n", i18n.L("fmt.info.label.etag"), item.ETag)
	}

	b.WriteString(strings.Repeat("=", 60))
	b.WriteString("\n")

	return b.String(), nil
}

// --- jsonFormatter -----------------------------------------------------------

// jsonFormatter implements Formatter for indented JSON output.
type jsonFormatter struct{}

// FormatDriveItems serializes a list of DriveItems to indented JSON.
func (f *jsonFormatter) FormatDriveItems(items []graph.DriveItem) (string, error) {
	b, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return "", fmt.Errorf("error serializing to JSON: %w", err)
	}
	return string(b), nil
}

// FormatDriveItem serializes a single DriveItem to indented JSON.
func (f *jsonFormatter) FormatDriveItem(item *graph.DriveItem) (string, error) {
	b, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return "", fmt.Errorf("error serializing to JSON: %w", err)
	}
	return string(b), nil
}

// --- yamlFormatter -----------------------------------------------------------

// yamlFormatter implements Formatter for YAML output.
type yamlFormatter struct{}

// FormatDriveItems serializes a list of DriveItems to YAML.
func (f *yamlFormatter) FormatDriveItems(items []graph.DriveItem) (string, error) {
	b, err := yaml.Marshal(items)
	if err != nil {
		return "", fmt.Errorf("error serializing to YAML: %w", err)
	}
	return string(b), nil
}

func (f *yamlFormatter) FormatDriveItem(item *graph.DriveItem) (string, error) {
	b, err := yaml.Marshal(item)
	if err != nil {
		return "", fmt.Errorf("error serializing to YAML: %w", err)
	}
	return string(b), nil
}
