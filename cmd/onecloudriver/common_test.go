package main

import (
	"strings"
	"testing"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/spf13/cobra"
)

func TestBuildResource(t *testing.T) {
	tests := []struct {
		name      string
		itemID    string
		itemPath  string
		label     string
		wantErr   string // expected substring of the error, empty if no error expected
		wantIsID  bool
		wantIsPth bool
		wantVal   string
	}{
		{
			name:     "only id",
			itemID:   "01BYE5RZ",
			wantIsID: true,
			wantVal:  "01BYE5RZ",
		},
		{
			name:      "only path",
			itemPath:  "/Documents/photo.jpg",
			wantIsPth: true,
			wantVal:   "/Documents/photo.jpg",
		},
		{
			name:    "neither with label",
			label:   " for the source",
			wantErr: "you must specify exactly one of --id or --path for the source",
		},
		{
			name:     "both",
			itemID:   "01BYE5RZ",
			itemPath: "/Documents/photo.jpg",
			label:    " for the destination folder",
			wantErr:  "you must specify exactly one of --id or --path for the destination folder",
		},
		{
			name:    "neither without label",
			wantErr: "you must specify exactly one of --id or --path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := buildResource(tt.itemID, tt.itemPath, tt.label)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if r == nil {
				t.Fatal("expected a non-nil Resource")
			}

			switch v := r.(type) {
			case graph.ItemID:
				if !tt.wantIsID {
					t.Errorf("expected ItemPath resource, got ItemID(%q)", string(v))
				}
				if string(v) != tt.wantVal {
					t.Errorf("expected ItemID value %q, got %q", tt.wantVal, string(v))
				}
			case graph.ItemPath:
				if !tt.wantIsPth {
					t.Errorf("expected ItemID resource, got ItemPath(%q)", string(v))
				}
				if string(v) != tt.wantVal {
					t.Errorf("expected ItemPath value %q, got %q", tt.wantVal, string(v))
				}
			default:
				t.Fatalf("unexpected resource type %T", r)
			}
		})
	}
}

func TestResolveAccountNameDefault(t *testing.T) {

	setupManager(t, "test@outlook.com")
	resolvedName, err := resolveAccountName(&cobra.Command{}, manager)
	if err != nil {
		t.Fatal("Unexpected error resolving default account")
	}
	if resolvedName != "test@outlook.com" {
		t.Fatalf("Expected resolved account name to be 'test@outlook.com', got '%s'", resolvedName)
	}
	acc, err := resolveAccount(&cobra.Command{}, manager)
	if err != nil {
		t.Fatal("Unexpected error resolving default account")
	}
	if acc.Name != "test@outlook.com" {
		t.Fatalf("Expected resolved account name to be 'test@outlook.com', got '%s'", acc.Name)
	}

}
