package fs

import (
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func TestCacheDirInUse_NoDatabase(t *testing.T) {
	// No cache dir / no DB file yet → nothing can be holding it.
	busy, err := CacheDirInUse(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("CacheDirInUse failed: %v", err)
	}
	if busy {
		t.Error("expected not busy when the cache directory does not exist")
	}
}

func TestCacheDirInUse_Free(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "inodes.db")

	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		t.Fatalf("creating db failed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("closing db failed: %v", err)
	}

	busy, err := CacheDirInUse(tmpDir)
	if err != nil {
		t.Fatalf("CacheDirInUse failed: %v", err)
	}
	if busy {
		t.Error("expected not busy when no holder is open")
	}
}

func TestCacheDirInUse_Locked(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "inodes.db")

	// Simulate a running mount: hold the exclusive BoltDB lock open.
	holder, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		t.Fatalf("creating holder db failed: %v", err)
	}
	defer holder.Close()

	busy, err := CacheDirInUse(tmpDir)
	if err != nil {
		t.Fatalf("CacheDirInUse failed: %v", err)
	}
	if !busy {
		t.Error("expected busy while another instance holds the BoltDB lock")
	}
}
