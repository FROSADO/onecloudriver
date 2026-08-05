package fs

import (
	"syscall"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
)

// ──── NewInodeDriveItem ────

func TestInode_NewInodeDriveItem_Nil(t *testing.T) {
	inode := NewInodeDriveItem(nil)
	if inode != nil {
		t.Error("NewInodeDriveItem(nil) should return nil")
	}
}

func TestInode_NewInodeDriveItem_File(t *testing.T) {
	modTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	item := &graph.DriveItem{
		ID:      "file123",
		Name:    "documento.pdf",
		Size:    2048000,
		ModTime: &modTime,
	}

	inode := NewInodeDriveItem(item)

	if inode.ID() != "file123" {
		t.Errorf("Expected ID 'file123', got %q", inode.ID())
	}
	if inode.Name() != "documento.pdf" {
		t.Errorf("Expected Name 'documento.pdf', got %q", inode.Name())
	}
	if inode.IsDir() {
		t.Error("A file should not be a directory")
	}
	if inode.Size() != 2048000 {
		t.Errorf("Expected size 2048000, got %d", inode.Size())
	}
	if inode.NLink() != 1 {
		t.Errorf("Expected NLink 1 for file, got %d", inode.NLink())
	}
}

func TestInode_NewInodeDriveItem_Folder(t *testing.T) {
	item := &graph.DriveItem{
		ID:     "folder123",
		Name:   "Documentos",
		Folder: &graph.Folder{ChildCount: 5},
	}

	inode := NewInodeDriveItem(item)

	if !inode.IsDir() {
		t.Error("A folder should be a directory")
	}
	if inode.Size() != 4096 {
		t.Errorf("Expected size 4096 for folder, got %d", inode.Size())
	}
	if inode.NLink() != 2 {
		t.Errorf("Expected NLink 2 for folder without subdirs, got %d", inode.NLink())
	}
}

// ──── Mode ────

func TestInode_Mode_File(t *testing.T) {
	item := &graph.DriveItem{ID: "f1", Name: "file.txt"}
	inode := NewInodeDriveItem(item)

	mode := inode.Mode()
	if mode&syscall.S_IFREG == 0 {
		t.Error("Mode should include S_IFREG")
	}
	if mode&0777 != 0644 {
		t.Errorf("Expected permissions 0644, got %o", mode&0777)
	}
}

func TestInode_Mode_Folder(t *testing.T) {
	item := &graph.DriveItem{ID: "d1", Name: "dir", Folder: &graph.Folder{}}
	inode := NewInodeDriveItem(item)

	mode := inode.Mode()
	if mode&syscall.S_IFDIR == 0 {
		t.Error("Mode should include S_IFDIR")
	}
	if mode&0777 != 0755 {
		t.Errorf("Expected permissions 0755, got %o", mode&0777)
	}
}

// ──── SetMode ────

func TestInode_SetMode_Custom(t *testing.T) {
	item := &graph.DriveItem{ID: "f1", Name: "file.txt"}
	inode := NewInodeDriveItem(item)

	inode.SetMode(syscall.S_IFREG | 0600)
	mode := inode.Mode()
	if mode != (syscall.S_IFREG | 0600) {
		t.Errorf("Expected mode S_IFREG|0600, got %o", mode)
	}
}

func TestInode_SetMode_ResetToDefault(t *testing.T) {
	item := &graph.DriveItem{ID: "f1", Name: "file.txt"}
	inode := NewInodeDriveItem(item)

	inode.SetMode(syscall.S_IFREG | 0600)
	inode.SetMode(0) // reset
	mode := inode.Mode()
	if mode != (syscall.S_IFREG | 0644) {
		t.Errorf("Expected default mode S_IFREG|0644, got %o", mode)
	}
}

func TestInode_SetMode_Folder(t *testing.T) {
	item := &graph.DriveItem{ID: "d1", Name: "dir", Folder: &graph.Folder{}}
	inode := NewInodeDriveItem(item)

	inode.SetMode(syscall.S_IFDIR | 0700)
	mode := inode.Mode()
	if mode != (syscall.S_IFDIR | 0700) {
		t.Errorf("Expected mode S_IFDIR|0700, got %o", mode)
	}
}

// ──── Children / HasChildren / SetChildren ────

func TestInode_Children_Uninitialized(t *testing.T) {
	item := &graph.DriveItem{ID: "d1", Name: "dir", Folder: &graph.Folder{}}
	inode := NewInodeDriveItem(item)

	if inode.Children() != nil {
		t.Error("Children should be nil when not initialized")
	}
	if inode.HasChildren() {
		t.Error("HasChildren should be false when children is nil and ChildCount=0")
	}
	if inode.IsChildrenFetched() {
		t.Error("IsChildrenFetched should be false when children is nil")
	}
}

func TestInode_SetChildren_Empty(t *testing.T) {
	item := &graph.DriveItem{ID: "d1", Name: "dir", Folder: &graph.Folder{}}
	inode := NewInodeDriveItem(item)

	inode.SetChildren([]string{})

	if inode.Children() == nil {
		t.Error("Children should not be nil after SetChildren([])")
	}
	if !inode.IsChildrenFetched() {
		t.Error("IsChildrenFetched should be true after initializing (even if empty)")
	}
	if inode.HasChildren() {
		t.Error("HasChildren should be false with empty children and ChildCount=0")
	}
	if len(inode.Children()) != 0 {
		t.Errorf("Expected empty Children, got %d elements", len(inode.Children()))
	}
}

func TestInode_SetChildren_WithIDs(t *testing.T) {
	item := &graph.DriveItem{ID: "d1", Name: "dir", Folder: &graph.Folder{}}
	inode := NewInodeDriveItem(item)

	ids := []string{"child1", "child2", "child3"}
	inode.SetChildren(ids)

	if !inode.HasChildren() {
		t.Error("HasChildren should be true with non-empty children")
	}
	children := inode.Children()
	if len(children) != 3 {
		t.Errorf("Expected 3 children, got %d", len(children))
	}
}

func TestInode_HasChildren_FromChildCount(t *testing.T) {
	item := &graph.DriveItem{
		ID: "d1", Name: "dir",
		Folder: &graph.Folder{ChildCount: 5},
	}
	inode := NewInodeDriveItem(item)

	// Aunque children es nil (nunca consultado), ChildCount=5 → HasChildren=true
	if !inode.HasChildren() {
		t.Error("HasChildren should be true when ChildCount > 0")
	}
	if inode.IsChildrenFetched() {
		t.Error("IsChildrenFetched should be false (children=nil)")
	}
}

// ──── NLink con subdir ────

func TestInode_NLink_WithSubdir(t *testing.T) {
	item := &graph.DriveItem{ID: "d1", Name: "dir", Folder: &graph.Folder{ChildCount: 3}}
	inode := NewInodeDriveItem(item)
	inode.SetSubdir(3)

	if inode.NLink() != 5 {
		t.Errorf("Expected NLink 5 (2+3), got %d", inode.NLink())
	}
}

// ──── ParentID / Path ────

func TestInode_ParentID_NoParent(t *testing.T) {
	item := &graph.DriveItem{ID: "root", Name: "root"}
	inode := NewInodeDriveItem(item)

	if inode.ParentID() != "" {
		t.Errorf("Expected ParentID '', got %q", inode.ParentID())
	}
}

func TestInode_ParentID_WithParent(t *testing.T) {
	item := &graph.DriveItem{
		ID:   "child1",
		Name: "subfolder",
		Parent: &graph.DriveItemParent{
			ID:   "parent123",
			Path: "/drive/root:/Documents",
		},
	}
	inode := NewInodeDriveItem(item)

	if inode.ParentID() != "parent123" {
		t.Errorf("Expected ParentID 'parent123', got %q", inode.ParentID())
	}
}

func TestInode_Path_Root(t *testing.T) {
	item := &graph.DriveItem{ID: "root", Name: "root"}
	inode := NewInodeDriveItem(item)

	if inode.Path() != "/" {
		t.Errorf("Expected Path '/', got %q", inode.Path())
	}
}

// ──── makeAttr ────

func TestInode_makeAttr_File(t *testing.T) {
	modTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	item := &graph.DriveItem{
		ID:      "file1",
		Name:    "doc.txt",
		Size:    500,
		ModTime: &modTime,
	}
	inode := NewInodeDriveItem(item)

	attr := inode.makeAttr()

	if attr.Size != 500 {
		t.Errorf("Expected size 500, got %d", attr.Size)
	}
	if attr.Nlink != 1 {
		t.Errorf("Expected nlink 1, got %d", attr.Nlink)
	}
	expectedMtime := uint64(modTime.Unix()) //#nosec G115 -- test timestamp, always >= 0
	if attr.Mtime != expectedMtime {
		t.Errorf("Expected mtime %d, got %d", expectedMtime, attr.Mtime)
	}
	if attr.Mode&syscall.S_IFREG == 0 {
		t.Error("makeAttr should produce S_IFREG for files")
	}
}

func TestInode_makeAttr_Folder(t *testing.T) {
	item := &graph.DriveItem{
		ID:     "dir1",
		Name:   "Documents",
		Folder: &graph.Folder{ChildCount: 2},
	}
	inode := NewInodeDriveItem(item)
	inode.SetSubdir(2)

	attr := inode.makeAttr()

	if attr.Size != 4096 {
		t.Errorf("Expected size 4096 for folder, got %d", attr.Size)
	}
	if attr.Nlink != 4 {
		t.Errorf("Expected Nlink 4 (2+2), got %d", attr.Nlink)
	}
	if attr.Mode&syscall.S_IFDIR == 0 {
		t.Error("makeAttr should produce S_IFDIR for folders")
	}
}

// ──── JSON serialization ────

func TestInode_AsJSON_RoundTrip(t *testing.T) {
	modTime := time.Date(2024, 3, 10, 8, 0, 0, 0, time.UTC)
	item := &graph.DriveItem{
		ID:      "item-abc",
		Name:    "test.txt",
		Size:    1024,
		ModTime: &modTime,
		Parent:  &graph.DriveItemParent{ID: "parent-xyz"},
	}
	inode := NewInodeDriveItem(item)
	inode.SetChildren([]string{"child-a", "child-b"})

	// Serializar
	jsonData := inode.AsJSON()
	if len(jsonData) == 0 {
		t.Fatal("AsJSON returned empty data")
	}

	// Deserializar
	restored, err := NewInodeJSON(jsonData)
	if err != nil {
		t.Fatalf("NewInodeJSON error: %v", err)
	}

	// Verify fields
	if restored.ID() != "item-abc" {
		t.Errorf("Expected ID 'item-abc', got %q", restored.ID())
	}
	if restored.Name() != "test.txt" {
		t.Errorf("Expected name 'test.txt', got %q", restored.Name())
	}
	if restored.Size() != 1024 {
		t.Errorf("Expected size 1024, got %d", restored.Size())
	}
	if restored.ParentID() != "parent-xyz" {
		t.Errorf("Expected ParentID 'parent-xyz', got %q", restored.ParentID())
	}

	children := restored.Children()
	if len(children) != 2 {
		t.Errorf("Expected 2 children, got %d", len(children))
	}
}

func TestInode_NewInodeJSON_Invalid(t *testing.T) {
	_, err := NewInodeJSON([]byte("esto no es json"))
	if err == nil {
		t.Error("An error was expected with invalid JSON")
	}
}

func TestInode_AsJSON_NoChildren(t *testing.T) {
	item := &graph.DriveItem{ID: "f1", Name: "file.txt"}
	inode := NewInodeDriveItem(item)

	jsonData := inode.AsJSON()
	restored, err := NewInodeJSON(jsonData)
	if err != nil {
		t.Fatalf("NewInodeJSON error: %v", err)
	}
	if restored.HasChildren() {
		t.Error("An Inode without children should not have HasChildren=true after roundtrip")
	}
}

// ──── Thread safety ────

func TestInode_ConcurrentReads(_ *testing.T) {
	item := &graph.DriveItem{
		ID:   "concurrent",
		Name: "shared.txt",
		Size: 100,
	}
	inode := NewInodeDriveItem(item)

	const goroutines = 50
	done := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = inode.ID()
				_ = inode.Name()
				_ = inode.IsDir()
				_ = inode.Mode()
				_ = inode.Size()
				_ = inode.NLink()
			}
			done <- struct{}{}
		}()
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}
}

func TestInode_ConcurrentReadWrite(_ *testing.T) {
	item := &graph.DriveItem{ID: "d1", Name: "dir", Folder: &graph.Folder{}}
	inode := NewInodeDriveItem(item)

	const goroutines = 20
	done := make(chan struct{})

	// Readers
	for i := 0; i < goroutines/2; i++ {
		go func() {
			for j := 0; j < 50; j++ {
				_ = inode.Children()
				_ = inode.HasChildren()
			}
			done <- struct{}{}
		}()
	}

	// Writers
	for i := 0; i < goroutines/2; i++ {
		go func(_ int) {
			for j := 0; j < 50; j++ {
				inode.SetChildren([]string{"child"})
				inode.SetSubdir(uint32(j % 5)) //#nosec G115 -- j % 5 is always in [0,5)
			}
			done <- struct{}{}
		}(i)
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}
}

// ──── DriveItemPtr ────

func TestInode_DriveItemPtr(t *testing.T) {
	item := &graph.DriveItem{ID: "x", Name: "y"}
	inode := NewInodeDriveItem(item)

	ptr := inode.DriveItemPtr()
	if ptr.ID != "x" {
		t.Errorf("Expected DriveItemPtr().ID 'x', got %q", ptr.ID)
	}
}
