package graph

import (
	"strings"
	"testing"
)

func TestSumQuickXORHash(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{
			name: "empty input",
			in:   nil,
			want: "AAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		},
		{
			name: "single byte",
			in:   []byte{0x4A}, // 'J'
			want: "SgAAAAAAAAAAAAAAAQAAAAAAAAA=",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SumQuickXORHash(tt.in); got != tt.want {
				t.Errorf("SumQuickXORHash(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestQuickXORHashStream(t *testing.T) {
	content := []byte("hello world")
	got, err := QuickXORHashStream(strings.NewReader(string(content)))
	if err != nil {
		t.Fatalf("QuickXORHashStream: %v", err)
	}
	if want := SumQuickXORHash(content); got != want {
		t.Errorf("QuickXORHashStream = %q, want %q (SumQuickXORHash)", got, want)
	}
}

func TestDriveItem_VerifyChecksum(t *testing.T) {
	tests := []struct {
		name     string
		item     *DriveItem
		checksum string
		want     bool
	}{
		{
			name:     "case-insensitive match",
			item:     &DriveItem{File: &File{Hashes: Hashes{QuickXorHash: "ABCdef=="}}},
			checksum: "abcdef==",
			want:     true,
		},
		{
			name:     "mismatch",
			item:     &DriveItem{File: &File{Hashes: Hashes{QuickXorHash: "ABCdef=="}}},
			checksum: "ZZZ==",
			want:     false,
		},
		{
			name:     "empty local checksum",
			item:     &DriveItem{File: &File{Hashes: Hashes{QuickXorHash: "ABCdef=="}}},
			checksum: "",
			want:     false,
		},
		{
			name:     "empty server hash",
			item:     &DriveItem{File: &File{Hashes: Hashes{QuickXorHash: ""}}},
			checksum: "ABCdef==",
			want:     false,
		},
		{
			name:     "nil file metadata",
			item:     &DriveItem{File: nil},
			checksum: "ABCdef==",
			want:     false,
		},
		{
			name:     "nil item",
			item:     nil,
			checksum: "ABCdef==",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.item.VerifyChecksum(tt.checksum); got != tt.want {
				t.Errorf("VerifyChecksum(%q) = %v, want %v", tt.checksum, got, tt.want)
			}
		})
	}
}
