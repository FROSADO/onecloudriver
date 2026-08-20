package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/frosado/onecloudriver/internal/graph"
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

	fmt.Fprintf(&b, "\n%-7s | %-30s | %-10s | %s\n", "TYPE", "NAME", "SIZE", "MODIFIED")
	b.WriteString(strings.Repeat("-", 70))
	b.WriteString("\n")

	for _, item := range items {
		typ := "File"
		size := fmt.Sprintf("%d B", item.Size)
		if item.IsFolder() {
			typ = "Folder"
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
	fmt.Fprintf(&b, "  %s info\n", map[bool]string{true: "directory", false: "file"}[item.IsFolder()])
	b.WriteString(strings.Repeat("=", 60))
	b.WriteString("\n")
	fmt.Fprintf(&b, "  Name:        %s\n", item.Name)
	fmt.Fprintf(&b, "  ID:          %s\n", item.ID)

	typ := "File"
	size := fmt.Sprintf("%d bytes (%.2f KB)", item.Size, float64(item.Size)/1024)
	if item.IsFolder() {
		typ = "Folder"
		if item.Folder != nil {
			size = fmt.Sprintf("- (%d elements)", item.Folder.ChildCount)
		} else {
			size = "-"
		}
	}
	fmt.Fprintf(&b, "  Type:        %s\n", typ)
	fmt.Fprintf(&b, "  Size:        %s\n", size)

	if item.CreatedTime != nil {
		fmt.Fprintf(&b, "  Created:     %s\n", item.CreatedTime.Format("2006-01-02 15:04:05"))
	}

	fmt.Fprintf(&b, "  Modified:    %s\n", item.ModTimeString())

	if item.File != nil {
		if item.File.Hashes.SHA1Hash != "" {
			fmt.Fprintf(&b, "  SHA1:        %s\n", item.File.Hashes.SHA1Hash)
		}
		if item.File.Hashes.QuickXorHash != "" {
			fmt.Fprintf(&b, "  QuickXorHash:%s\n", item.File.Hashes.QuickXorHash)
		}
	}

	if item.Parent != nil {
		if item.Parent.DriveType != "" {
			fmt.Fprintf(&b, "  Drive:       %s\n", item.Parent.DriveType)
		}
		if item.Parent.ID != "" {
			fmt.Fprintf(&b, "  Parent ID:   %s\n", item.Parent.ID)
		}
	}

	if item.ETag != "" {
		fmt.Fprintf(&b, "  ETag:        %s\n", item.ETag)
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
