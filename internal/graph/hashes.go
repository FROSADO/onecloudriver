package graph

import (
	"encoding/base64"
	"io"
	"strings"

	"github.com/frosado/onecloudriver/internal/graph/quickxorhash"
)

// SumQuickXORHash computes the quickXorHash of data and returns its base64
// representation, matching the format the Microsoft Graph API uses for the
// file.hashes.quickXorHash field.
func SumQuickXORHash(data []byte) string {
	sum := quickxorhash.Sum(data)
	return base64.StdEncoding.EncodeToString(sum[:])
}

// QuickXORHashStream hashes the contents of r and returns the base64
// representation of the quickXorHash, matching the format the Microsoft Graph
// API uses for the file.hashes.quickXorHash field.
func QuickXORHashStream(r io.Reader) (string, error) {
	h := quickxorhash.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

// VerifyChecksum reports whether checksum (a locally computed base64
// quickXorHash) matches the hash the server stored for this item. The
// comparison is case-insensitive and returns false when either side is empty
// or when the item has no file metadata (folders never carry a quickXorHash).
func (item *DriveItem) VerifyChecksum(checksum string) bool {
	if item == nil || item.File == nil || checksum == "" || item.File.Hashes.QuickXorHash == "" {
		return false
	}
	return strings.EqualFold(item.File.Hashes.QuickXorHash, checksum)
}
