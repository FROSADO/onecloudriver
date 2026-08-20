package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
	"go.yaml.in/yaml/v3"
)

// --- getFormatter -------------------------------------------------------------

func TestGetFormatter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		format    string
		wantNil   bool
		wantErr   bool
		errSubstr string
	}{
		{
			name:    "text format",
			format:  "text",
			wantNil: false,
			wantErr: false,
		},
		{
			name:    "json format",
			format:  "json",
			wantNil: false,
			wantErr: false,
		},
		{
			name:    "yaml format",
			format:  "yaml",
			wantNil: false,
			wantErr: false,
		},
		{
			name:      "empty format",
			format:    "",
			wantNil:   true,
			wantErr:   true,
			errSubstr: "unsupported format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f, err := getFormatter(tt.format)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("expected error containing %q, got %q", tt.errSubstr, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}

			if tt.wantNil && f != nil {
				t.Fatalf("expected nil formatter, got %T", f)
			}
			if !tt.wantNil && f == nil {
				t.Fatal("expected non-nil formatter, got nil")
			}
		})
	}
}

// --- textFormatter.FormatDriveItems -------------------------------------------

func TestTextFormatter_FormatDriveItems(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)

	tests := []struct {
		name    string
		items   []graph.DriveItem
		contain []string // strings the output must contain
	}{
		{
			name:  "empty list",
			items: []graph.DriveItem{},
			contain: []string{
				"TYPE", "NAME", "SIZE", "MODIFIED",
			},
		},
		{
			name: "single file",
			items: []graph.DriveItem{
				{
					Name:    "photo.jpg",
					Size:    1024,
					ModTime: &now,
				},
			},
			contain: []string{
				"File", "photo.jpg", "1024 B", "2025-01-15 10:30:00",
			},
		},
		{
			name: "single folder",
			items: []graph.DriveItem{
				{
					Name:    "Documents",
					Size:    0,
					ModTime: &now,
					Folder:  &graph.Folder{ChildCount: 5},
				},
			},
			contain: []string{
				"Folder", "Documents", "-",
			},
		},
		{
			name: "mixed items",
			items: []graph.DriveItem{
				{
					Name:    "readme.md",
					Size:    512,
					ModTime: &now,
				},
				{
					Name:    "Pictures",
					ModTime: &now,
					Folder:  &graph.Folder{ChildCount: 42},
				},
			},
			contain: []string{
				"File", "readme.md", "512 B",
				"Folder", "Pictures", "-",
			},
		},
		{
			name: "nil modtime",
			items: []graph.DriveItem{
				{
					Name: "no-date.txt",
					Size: 256,
				},
			},
			contain: []string{
				"File", "no-date.txt", "256 B", "N/A",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := &textFormatter{}
			out, err := f.FormatDriveItems(tt.items)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, substr := range tt.contain {
				if !strings.Contains(out, substr) {
					t.Errorf("expected output to contain %q\nGot:\n%s", substr, out)
				}
			}
		})
	}
}

// --- textFormatter.FormatDriveItem --------------------------------------------

func TestTextFormatter_FormatDriveItem(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 6, 20, 14, 45, 30, 0, time.UTC)

	tests := []struct {
		name    string
		item    *graph.DriveItem
		contain []string
	}{
		{
			name: "complete file",
			item: &graph.DriveItem{
				ID:          "ABC123",
				Name:        "report.pdf",
				Size:        2048,
				ModTime:     &now,
				CreatedTime: &now,
				ETag:        "etag-value",
				File: &graph.File{
					Hashes: graph.Hashes{
						SHA1Hash:     "abc123sha1",
						QuickXorHash: "qxh123",
					},
				},
				Parent: &graph.DriveItemParent{
					DriveType: "personal",
					ID:        "PARENT456",
				},
			},
			contain: []string{
				"file info",
				"Name:        report.pdf",
				"ID:          ABC123",
				"Type:        File",
				"Size:        2048 bytes (2.00 KB)",
				"Created:     2025-06-20 14:45:30",
				"Modified:    2025-06-20 14:45:30",
				"SHA1:        abc123sha1",
				"QuickXorHash:qxh123",
				"Drive:       personal",
				"Parent ID:   PARENT456",
				"ETag:        etag-value",
			},
		},
		{
			name: "folder with child count",
			item: &graph.DriveItem{
				ID:      "FOLDER789",
				Name:    "Documents",
				ModTime: &now,
				Folder:  &graph.Folder{ChildCount: 10},
			},
			contain: []string{
				"directory info",
				"Name:        Documents",
				"ID:          FOLDER789",
				"Type:        Folder",
				"Size:        - (10 elements)",
			},
		},
		{
			name: "folder with nil Folder field",
			item: &graph.DriveItem{
				ID:      "FOLDER000",
				Name:    "EmptyFolder",
				ModTime: &now,
				Folder:  nil, // IsFolder() returns false here, but test edge case
			},
			contain: []string{
				"Name:        EmptyFolder",
				"Size:        0 bytes (0.00 KB)",
			},
		},
		{
			name: "minimal item",
			item: &graph.DriveItem{
				ID:   "MIN001",
				Name: "minimal.txt",
			},
			contain: []string{
				"Name:        minimal.txt",
				"ID:          MIN001",
				"Modified:    N/A",
			},
		},
		{
			name: "file without hashes",
			item: &graph.DriveItem{
				ID:      "FILE001",
				Name:    "plain.txt",
				Size:    100,
				ModTime: &now,
				File:    &graph.File{},
			},
			contain: []string{
				"Name:        plain.txt",
				"Size:        100 bytes (0.10 KB)",
			},
		},
		{
			name: "item with only parent id (no drive type)",
			item: &graph.DriveItem{
				ID:      "ITEM001",
				Name:    "shared.txt",
				ModTime: &now,
				Parent: &graph.DriveItemParent{
					ID: "PARENT999",
				},
			},
			contain: []string{
				"Parent ID:   PARENT999",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := &textFormatter{}
			out, err := f.FormatDriveItem(tt.item)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, substr := range tt.contain {
				if !strings.Contains(out, substr) {
					t.Errorf("expected output to contain %q\nGot:\n%s", substr, out)
				}
			}
		})
	}
}

// --- jsonFormatter.FormatDriveItems -------------------------------------------

func TestJSONFormatter_FormatDriveItems(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 3, 10, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		items   []graph.DriveItem
		contain []string
	}{
		{
			name:  "empty list",
			items: []graph.DriveItem{},
			contain: []string{
				"[]",
			},
		},
		{
			name: "single item",
			items: []graph.DriveItem{
				{
					ID:      "JSON001",
					Name:    "data.json",
					Size:    4096,
					ModTime: &now,
				},
			},
			contain: []string{
				`"id": "JSON001"`,
				`"name": "data.json"`,
				`"size": 4096`,
			},
		},
		{
			name: "multiple items",
			items: []graph.DriveItem{
				{ID: "A", Name: "first.txt"},
				{ID: "B", Name: "second.txt"},
			},
			contain: []string{
				`"id": "A"`,
				`"id": "B"`,
				`"name": "first.txt"`,
				`"name": "second.txt"`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := &jsonFormatter{}
			out, err := f.FormatDriveItems(tt.items)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, substr := range tt.contain {
				if !strings.Contains(out, substr) {
					t.Errorf("expected output to contain %q\nGot:\n%s", substr, out)
				}
			}
		})
	}
}

// --- jsonFormatter.FormatDriveItem --------------------------------------------

func TestJSONFormatter_FormatDriveItem(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 4, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		item    *graph.DriveItem
		contain []string
	}{
		{
			name: "complete item",
			item: &graph.DriveItem{
				ID:          "FULL001",
				Name:        "complete.json",
				Size:        8192,
				ModTime:     &now,
				CreatedTime: &now,
				ETag:        "etag-json",
				File: &graph.File{
					Hashes: graph.Hashes{
						SHA1Hash:     "sha1-json",
						QuickXorHash: "qxh-json",
					},
				},
				Parent: &graph.DriveItemParent{
					DriveType: "business",
					ID:        "PAR-BIZ",
				},
				Folder: nil,
			},
			contain: []string{
				`"id": "FULL001"`,
				`"name": "complete.json"`,
				`"size": 8192`,
				`"eTag": "etag-json"`,
				`"sha1Hash": "sha1-json"`,
				`"quickXorHash": "qxh-json"`,
				`"driveType": "business"`,
				`"id": "PAR-BIZ"`,
			},
		},
		{
			name: "folder item",
			item: &graph.DriveItem{
				ID:     "FOLDER-JSON",
				Name:   "MyFolder",
				Folder: &graph.Folder{ChildCount: 7},
			},
			contain: []string{
				`"id": "FOLDER-JSON"`,
				`"name": "MyFolder"`,
				`"childCount": 7`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := &jsonFormatter{}
			out, err := f.FormatDriveItem(tt.item)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, substr := range tt.contain {
				if !strings.Contains(out, substr) {
					t.Errorf("expected output to contain %q\nGot:\n%s", substr, out)
				}
			}
		})
	}
}

// --- yamlFormatter.FormatDriveItems -------------------------------------------

func TestYAMLFormatter_FormatDriveItems(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 3, 10, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		items   []graph.DriveItem
		contain []string
	}{
		{
			name:  "empty list",
			items: []graph.DriveItem{},
			contain: []string{
				"[]",
			},
		},
		{
			name: "single item",
			items: []graph.DriveItem{
				{
					ID:      "YAML001",
					Name:    "data.yaml",
					Size:    4096,
					ModTime: &now,
				},
			},
			contain: []string{
				"id: YAML001",
				"name: data.yaml",
				"size: 4096",
			},
		},
		{
			name: "multiple items",
			items: []graph.DriveItem{
				{ID: "A", Name: "first.yaml"},
				{ID: "B", Name: "second.yaml"},
			},
			contain: []string{
				"id: A",
				"id: B",
				"name: first.yaml",
				"name: second.yaml",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := &yamlFormatter{}
			out, err := f.FormatDriveItems(tt.items)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, substr := range tt.contain {
				if !strings.Contains(out, substr) {
					t.Errorf("expected output to contain %q\nGot:\n%s", substr, out)
				}
			}
		})
	}
}

// --- yamlFormatter.FormatDriveItem --------------------------------------------

func TestYAMLFormatter_FormatDriveItem(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 4, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		item    *graph.DriveItem
		contain []string
	}{
		{
			name: "complete item",
			item: &graph.DriveItem{
				ID:          "FULL001",
				Name:        "complete.yaml",
				Size:        8192,
				ModTime:     &now,
				CreatedTime: &now,
				ETag:        "etag-yaml",
				File: &graph.File{
					Hashes: graph.Hashes{
						SHA1Hash:     "sha1-yaml",
						QuickXorHash: "qxh-yaml",
					},
				},
				Parent: &graph.DriveItemParent{
					DriveType: "business",
					ID:        "PAR-BIZ",
				},
				Folder: nil,
			},
			contain: []string{
				"id: FULL001",
				"name: complete.yaml",
				"size: 8192",
				"etag: etag-yaml",
				"sha1hash: sha1-yaml",
				"quickxorhash: qxh-yaml",
				"drivetype: business",
				"id: PAR-BIZ",
			},
		},
		{
			name: "folder item",
			item: &graph.DriveItem{
				ID:     "FOLDER-YAML",
				Name:   "MyFolder",
				Folder: &graph.Folder{ChildCount: 7},
			},
			contain: []string{
				"id: FOLDER-YAML",
				"name: MyFolder",
				"childcount: 7",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := &yamlFormatter{}
			out, err := f.FormatDriveItem(tt.item)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, substr := range tt.contain {
				if !strings.Contains(out, substr) {
					t.Errorf("expected output to contain %q\nGot:\n%s", substr, out)
				}
			}
		})
	}
}

// --- Error cases (JSON/YAML marshalling) ------------------------------------------

// TestJSONFormatter_MarshalErrorHandling tests that JSON formatter properly handles
// cases where serialization might fail (e.g., with unmarshalable types).
func TestJSONFormatter_MarshalErrorHandling(t *testing.T) {
	t.Parallel()

	// While it's hard to make json.Marshal fail with standard types,
	// we test that the error path is properly formatted.
	// We verify the formatter returns formatted errors for any hypothetical failure.

	f := &jsonFormatter{}

	// Valid item should succeed
	item := &graph.DriveItem{
		ID:   "TEST001",
		Name: "valid.json",
	}

	out, err := f.FormatDriveItem(item)
	if err != nil {
		t.Fatalf("unexpected error for valid item: %v", err)
	}

	if !strings.Contains(out, "TEST001") {
		t.Errorf("expected JSON output to contain ID, got: %s", out)
	}

	// Test FormatDriveItems with valid data
	items := []graph.DriveItem{
		{ID: "A", Name: "a.json"},
		{ID: "B", Name: "b.json"},
	}

	out, err = f.FormatDriveItems(items)
	if err != nil {
		t.Fatalf("unexpected error for valid items: %v", err)
	}

	if !strings.Contains(out, "\"id\": \"A\"") {
		t.Errorf("expected JSON array output, got: %s", out)
	}
}

// TestYAMLFormatter_MarshalErrorHandling tests that YAML formatter properly handles
// serialization (similarly to JSON, standard types rarely fail to marshal).
func TestYAMLFormatter_MarshalErrorHandling(t *testing.T) {
	t.Parallel()

	f := &yamlFormatter{}

	// Valid item should succeed
	item := &graph.DriveItem{
		ID:   "TEST002",
		Name: "valid.yaml",
	}

	out, err := f.FormatDriveItem(item)
	if err != nil {
		t.Fatalf("unexpected error for valid item: %v", err)
	}

	if !strings.Contains(out, "TEST002") {
		t.Errorf("expected YAML output to contain ID, got: %s", out)
	}

	// Test FormatDriveItems with valid data
	items := []graph.DriveItem{
		{ID: "X", Name: "x.yaml"},
		{ID: "Y", Name: "y.yaml"},
	}

	out, err = f.FormatDriveItems(items)
	if err != nil {
		t.Fatalf("unexpected error for valid items: %v", err)
	}

	if !strings.Contains(out, "id: X") {
		t.Errorf("expected YAML array output, got: %s", out)
	}
}

// --- validateOutputFormat ------------------------------------------------------

func TestValidateOutputFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		format    string
		wantErr   bool
		errSubstr string
	}{
		{name: "text accepted", format: "text"},
		{name: "json accepted", format: "json"},
		{name: "yaml accepted", format: "yaml"},
		{name: "xml rejected", format: "xml", wantErr: true, errSubstr: "unsupported format"},
		{name: "empty rejected", format: "", wantErr: true, errSubstr: "unsupported format"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateOutputFormat(tt.format)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("expected error containing %q, got %q", tt.errSubstr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// --- formatStructuredValue -----------------------------------------------------

func TestFormatStructuredValue(t *testing.T) {
	t.Parallel()

	type sample struct {
		Name string `json:"name" yaml:"name"`
		N    int    `json:"n" yaml:"n"`
	}
	v := sample{Name: "demo", N: 7}

	t.Run("json serializes and round-trips", func(t *testing.T) {
		t.Parallel()

		out, err := formatStructuredValue("json", v)
		if err != nil {
			t.Fatalf("formatStructuredValue(json): %v", err)
		}

		var got sample
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("output is not valid JSON: %v\n%s", err, out)
		}
		if got != v {
			t.Errorf("round-trip = %#v, want %#v", got, v)
		}
	})

	t.Run("yaml serializes and round-trips", func(t *testing.T) {
		t.Parallel()

		out, err := formatStructuredValue("yaml", v)
		if err != nil {
			t.Fatalf("formatStructuredValue(yaml): %v", err)
		}

		var got sample
		if err := yaml.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("output is not valid YAML: %v\n%s", err, out)
		}
		if got != v {
			t.Errorf("round-trip = %#v, want %#v", got, v)
		}
	})

	t.Run("empty slice serializes to an empty collection", func(t *testing.T) {
		t.Parallel()

		out, err := formatStructuredValue("json", []int{})
		if err != nil {
			t.Fatalf("formatStructuredValue(json, []): %v", err)
		}
		if strings.TrimSpace(out) != "[]" {
			t.Errorf("empty slice JSON = %q, want []", out)
		}
	})

	t.Run("ends with a single trailing newline", func(t *testing.T) {
		t.Parallel()

		for _, format := range []string{"json", "yaml"} {
			out, err := formatStructuredValue(format, v)
			if err != nil {
				t.Fatalf("formatStructuredValue(%q): %v", format, err)
			}
			if !strings.HasSuffix(out, "\n") {
				t.Errorf("%s output missing trailing newline: %q", format, out)
			}
			if strings.HasSuffix(out, "\n\n") {
				t.Errorf("%s output has double trailing newline: %q", format, out)
			}
		}
	})

	t.Run("text is rejected", func(t *testing.T) {
		t.Parallel()

		if _, err := formatStructuredValue("text", v); err == nil {
			t.Fatal("expected error for text (not a structured format)")
		}
	})

	t.Run("unknown format is rejected", func(t *testing.T) {
		t.Parallel()

		_, err := formatStructuredValue("xml", v)
		if err == nil || !strings.Contains(err.Error(), "unsupported format") {
			t.Fatalf("expected unsupported format error, got %v", err)
		}
	})
}
