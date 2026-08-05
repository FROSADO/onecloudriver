package fs

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// ──── MoveID ────

func TestInodeCache_MoveID(t *testing.T) {
	cache := NewInodeCache()

	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "docs", Folder: &graph.Folder{},
	})
	parent.SetChildren([]string{"oldID"})
	cache.Insert(parent)

	child := NewInodeDriveItem(&graph.DriveItem{
		ID: "oldID", Name: "file.txt",
		Parent: &graph.DriveItemParent{ID: "parent1"},
	})
	cache.Insert(child)

	cache.MoveID("oldID", "newID")

	// oldID must no longer exist
	if cache.Get("oldID") != nil {
		t.Error("oldID should have been removed")
	}
	// newID must exist
	moved := cache.Get("newID")
	if moved == nil {
		t.Fatal("newID should exist")
	}
	if moved.Name() != "file.txt" {
		t.Errorf("Expected Name 'file.txt', got %q", moved.Name())
	}
	// 🔧 The internal ID must be updated to newID (regression: it previously
	// con el ID viejo, rompiendo ItemsByParent/fallback offline)
	if moved.ID() != "newID" {
		t.Errorf("Expected internal ID 'newID', got %q", moved.ID())
	}
	// The parent must have newID in its children
	updatedParent := cache.Get("parent1")
	children := updatedParent.Children()
	if len(children) != 1 || children[0] != "newID" {
		t.Errorf("Expected children ['newID'], got %v", children)
	}
}

func TestInodeCache_MoveID_Nonexistent(_ *testing.T) {
	cache := NewInodeCache()
	cache.MoveID("nonexistent", "newID") // must not panic
}

// ──── SetOffline / IsOffline ────

func TestInodeCache_SetOffline(t *testing.T) {
	cache := NewInodeCache()

	if cache.IsOffline() {
		t.Error("IsOffline should be false initially")
	}
	cache.SetOffline(true)
	if !cache.IsOffline() {
		t.Error("IsOffline should be true after SetOffline(true)")
	}
	cache.SetOffline(false)
	if cache.IsOffline() {
		t.Error("IsOffline should be false after SetOffline(false)")
	}
}

func TestInodeCache_SetOffline_Idempotent(t *testing.T) {
	cache := NewInodeCache()
	cache.SetOffline(true)
	cache.SetOffline(true) // must not duplicate the log
	if !cache.IsOffline() {
		t.Error("IsOffline should still be true")
	}
}

// ──── BaseTTL / MaxEntries ────

func TestInodeCache_BaseTTL(t *testing.T) {
	cache := NewInodeCache()

	if cache.BaseTTL() != 60*time.Second {
		t.Errorf("Expected default base TTL 60s, got %v", cache.BaseTTL())
	}
	cache.SetBaseTTL(120 * time.Second)
	if cache.BaseTTL() != 120*time.Second {
		t.Errorf("Expected base TTL 120s, got %v", cache.BaseTTL())
	}
}

func TestInodeCache_MaxEntries(t *testing.T) {
	cache := NewInodeCache()

	if cache.MaxEntries() != 2000 {
		t.Errorf("Expected default MaxEntries 2000, got %d", cache.MaxEntries())
	}
	cache.SetMaxEntries(500)
	if cache.MaxEntries() != 500 {
		t.Errorf("Expected MaxEntries 500, got %d", cache.MaxEntries())
	}
}

// ──── fillEntryOut ────

func TestFillEntryOut_File(t *testing.T) {
	modTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	item := &graph.DriveItem{
		ID:      "file1",
		Name:    "doc.pdf",
		Size:    4096,
		ModTime: &modTime,
	}

	var out fuse.EntryOut
	fillEntryOut(&out, item)

	if out.Mode&syscall.S_IFREG == 0 {
		t.Error("Mode should include S_IFREG")
	}
	if out.Mode&0777 != 0644 {
		t.Errorf("Expected permissions 0644, got %o", out.Mode&0777)
	}
	if out.Nlink != 1 {
		t.Errorf("Expected nlink 1, got %d", out.Nlink)
	}
	if out.Size != 4096 {
		t.Errorf("Expected size 4096, got %d", out.Size)
	}
	expectedMtime := uint64(modTime.Unix()) //#nosec G115 -- test timestamp, always >= 0
	if out.Mtime != expectedMtime {
		t.Errorf("Expected mtime %d, got %d", expectedMtime, out.Mtime)
	}
}

func TestFillEntryOut_Folder(t *testing.T) {
	item := &graph.DriveItem{
		ID:     "folder1",
		Name:   "Documents",
		Folder: &graph.Folder{ChildCount: 5},
	}

	var out fuse.EntryOut
	fillEntryOut(&out, item)

	if out.Mode&syscall.S_IFDIR == 0 {
		t.Error("Mode should include S_IFDIR")
	}
	if out.Mode&0777 != 0755 {
		t.Errorf("Expected permissions 0755, got %o", out.Mode&0777)
	}
	if out.Nlink != 2 {
		t.Errorf("Expected nlink 2, got %d", out.Nlink)
	}
	if out.Size != 4096 {
		t.Errorf("Expected size 4096, got %d", out.Size)
	}
}

func TestFillEntryOut_NilModTime(t *testing.T) {
	item := &graph.DriveItem{
		ID:      "file1",
		Name:    "no_date.txt",
		Size:    100,
		ModTime: nil,
	}

	var out fuse.EntryOut
	fillEntryOut(&out, item)

	// With nil ModTime, it must use time.Now()
	if out.Mtime == 0 {
		t.Error("Mtime should not be 0 when ModTime is nil")
	}
}

// ──── Subdir ────

func TestInode_Subdir(t *testing.T) {
	inode := NewInodeDriveItem(&graph.DriveItem{
		ID: "folder1", Name: "docs", Folder: &graph.Folder{},
	})

	if inode.Subdir() != 0 {
		t.Errorf("Expected initial subdir 0, got %d", inode.Subdir())
	}

	inode.SetSubdir(3)
	if inode.Subdir() != 3 {
		t.Errorf("Expected subdir 3, got %d", inode.Subdir())
	}

	// NLink must reflect the subdir
	if inode.NLink() != 5 { // 2 + 3
		t.Errorf("Expected NLink 5, got %d", inode.NLink())
	}
}

// ──── isNetworkError ────

func TestIsNetworkError_Nil(t *testing.T) {
	if isNetworkError(nil) {
		t.Error("isNetworkError(nil) should be false")
	}
}

func TestIsNetworkError_RegularError(t *testing.T) {
	if isNetworkError(errors.New("something went wrong")) {
		t.Error("isNetworkError for a regular error should be false")
	}
}

func TestIsNetworkError_Timeout(t *testing.T) {
	err := &net.DNSError{Name: "example.com", Err: "timeout", IsTimeout: true}
	if !isNetworkError(err) {
		t.Error("isNetworkError for a DNSError with timeout should be true")
	}
}

func TestIsNetworkError_NetOpError(t *testing.T) {
	// net.OpError.Temporary() returns false for connection refused, so it
	// is a NetworkError ONLY if the errno syscall.ECONNREFUSED is present.
	err := &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
	if !isNetworkError(err) {
		t.Error("isNetworkError for connection refused (ECONNREFUSED) should be true")
	}
}

// TestIsNetworkError_ConnectionRefused_RealScenario reproduce el error REAL que
// produced a broken proxy / cut network in the offline end-to-end test:
//
//	url.Error (http.Client) → net.OpError → os.SyscallError → syscall.ECONNREFUSED
//
// Este era el bug: isNetworkError solo miraba Timeout()/Temporary(), y ambos
// return false for ECONNREFUSED → the offline fallback never activated and
// navigating a stale subfolder returned "Error in Lookup" (EIO).
func TestIsNetworkError_ConnectionRefused_RealScenario(t *testing.T) {
	inner := &net.OpError{
		Op:   "dial",
		Net:  "tcp",
		Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1},
		Err:  os.NewSyscallError("connect", syscall.ECONNREFUSED),
	}
	urlErr := &url.Error{Op: "Get", URL: "https://graph.microsoft.com/v1.0/me/drive/root/delta", Err: inner}
	wrapped := fmt.Errorf("error de red al consultar Graph: %w", urlErr)
	if !isNetworkError(wrapped) {
		t.Error("isNetworkError for a broken proxy (wrapped ECONNREFUSED) should be true")
	}
}

// TestIsNetworkError_OtherConnErrnos: ECONNRESET, EHOSTUNREACH, ENETUNREACH,
// ETIMEDOUT must also be recognized (cut network, unreachable server, etc.).
func TestIsNetworkError_OtherConnErrnos(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"ECONNRESET", &net.OpError{Op: "read", Err: syscall.ECONNRESET}},
		{"EHOSTUNREACH", &net.OpError{Op: "dial", Err: syscall.EHOSTUNREACH}},
		{"ENETUNREACH", &net.OpError{Op: "dial", Err: syscall.ENETUNREACH}},
		{"ETIMEDOUT", &net.OpError{Op: "dial", Err: syscall.ETIMEDOUT}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !isNetworkError(tc.err) {
				t.Errorf("isNetworkError for %s should be true", tc.name)
			}
		})
	}
}

// TestIsNetworkError_NonErrnoOpError: an OpError without a connection errno (e.g.
// application error) must NOT be recognized as a network error.
func TestIsNetworkError_NonErrnoOpError(t *testing.T) {
	err := &net.OpError{Op: "dial", Err: errors.New("connection refused")}
	if isNetworkError(err) {
		t.Error("isNetworkError for an OpError without a connection errno should be false")
	}
}

func TestIsNetworkError_Temporary(t *testing.T) {
	err := &net.DNSError{Name: "example.com", Err: "temporary failure", IsTemporary: true}
	if !isNetworkError(err) {
		t.Error("isNetworkError for a temporary DNSError should be true")
	}
}

// TestIsNetworkError_Wrapped regression: the Graph layer wraps errors with
// %w ("network error querying Graph: %w"). Without errors.As, a wrapped error
// it was never recognized and offline mode failed in production.
func TestIsNetworkError_Wrapped(t *testing.T) {
	inner := &net.DNSError{Name: "example.com", Err: "timeout", IsTimeout: true}
	wrapped := fmt.Errorf("error de red al consultar Graph: %w", inner)
	if !isNetworkError(wrapped) {
		t.Error("isNetworkError for an error wrapped with %w should be true")
	}

	// Double wrapping (typical of intermediate layers)
	wrapped2 := fmt.Errorf("ListDriveRoot: %w", wrapped)
	if !isNetworkError(wrapped2) {
		t.Error("isNetworkError for double wrapping should be true")
	}
}

// TestIsNetworkError_Wrapped_NonNetwork: a regular wrapped error must NOT
// be recognized as a network error.
func TestIsNetworkError_Wrapped_NonNetwork(t *testing.T) {
	wrapped := fmt.Errorf("HTTP 500 interno: %w", errors.New("server error"))
	if isNetworkError(wrapped) {
		t.Error("isNetworkError for a wrapped HTTP error should be false")
	}
}

// ──── InodeCache: SetOffline with fetchChildren (integration test) ────
// Note: TestOneCloudFS_FetchChildren_NetworkError requires mounting a server
// HTTP that rejects connections, which is covered in integration tests.
