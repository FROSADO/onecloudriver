package fs

import (
	"syscall"
	"testing"

	"github.com/frosado/onecloudriver/internal/graph"
)

// ──── hasChanges ────

func TestInode_HasChanges_DefaultFalse(t *testing.T) {
	item := &graph.DriveItem{ID: "f1", Name: "file.txt"}
	inode := NewInodeDriveItem(item)

	if inode.HasChanges() {
		t.Error("A newly created Inode should not have changes")
	}
}

func TestInode_SetHasChanges(t *testing.T) {
	item := &graph.DriveItem{ID: "f1", Name: "file.txt"}
	inode := NewInodeDriveItem(item)

	inode.SetHasChanges(true)
	if !inode.HasChanges() {
		t.Error("HasChanges should be true after SetHasChanges(true)")
	}

	inode.SetHasChanges(false)
	if inode.HasChanges() {
		t.Error("HasChanges should be false after SetHasChanges(false)")
	}
}

// ──── NewInodeLocal ────

func TestInode_NewInodeLocal_File(t *testing.T) {
	parent := &Inode{DriveItem: graph.DriveItem{ID: "parent1", Name: "Documents"}}
	child := NewInodeLocal("nuevo.txt", syscall.S_IFREG|0644, parent)

	if child == nil {
		t.Fatal("NewInodeLocal returned nil")
	}
	if !isLocalID(child.ID()) {
		t.Errorf("ID should be local, got %q", child.ID())
	}
	if child.Name() != "nuevo.txt" {
		t.Errorf("Expected Name 'nuevo.txt', got %q", child.Name())
	}
	if child.IsDir() {
		t.Error("A file should not be a directory")
	}
	if child.ParentID() != "parent1" {
		t.Errorf("Expected parentID 'parent1', got %q", child.ParentID())
	}
	if !child.HasChanges() {
		t.Error("A new local file should start with hasChanges=true")
	}
}

func TestInode_NewInodeLocal_Folder(t *testing.T) {
	parent := &Inode{DriveItem: graph.DriveItem{ID: "parent1", Name: "Documents"}}
	child := NewInodeLocal("Nueva Carpeta", syscall.S_IFDIR|0755, parent)

	if child == nil {
		t.Fatal("NewInodeLocal returned nil")
	}
	if !child.IsDir() {
		t.Error("Should be a directory")
	}
	if child.HasChanges() {
		t.Error("A folder should not start with hasChanges=true")
	}
}

func TestInode_NewInodeLocal_NoParent(t *testing.T) {
	child := NewInodeLocal("raiz_file.txt", syscall.S_IFREG|0644, nil)

	if child == nil {
		t.Fatal("NewInodeLocal returned nil")
	}
	if child.ParentID() != "" {
		t.Errorf("Without a parent, ParentID should be '', got %q", child.ParentID())
	}
}

// ──── isLocalID ────

func TestInode_isLocalID_True(t *testing.T) {
	if !isLocalID("local-abc123") {
		t.Error("isLocalID should return true for IDs with the 'local-' prefix")
	}
}

func TestInode_isLocalID_False(t *testing.T) {
	if isLocalID("01BYE5RZ6QN3VXWN") {
		t.Error("isLocalID should return false for remote IDs")
	}
	if isLocalID("") {
		t.Error("isLocalID should return false for an empty string")
	}
	if isLocalID("local") {
		t.Error("isLocalID should return false for 'local' without a dash")
	}
}

func TestInode_newLocalID_Unique(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := newLocalID()
		if !isLocalID(id) {
			t.Errorf("newLocalID()=%q is not a valid local ID", id)
		}
		if ids[id] {
			t.Errorf("ID duplicado: %q", id)
		}
		ids[id] = true
	}
}
