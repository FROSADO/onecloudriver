package fs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// ──── Write ────

func TestDriveItemNode_Write_Success(t *testing.T) {
	tmpDir := t.TempDir()
	contentCache, _ := NewContentCache(tmpDir)
	contentCache.Insert("file123", []byte("0123456789"))

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "file123", Name: "test.txt", Size: 10}),
		nodeDeps: nodeDeps{
			contentCache: contentCache,
		},
	}

	n, errno := node.Write(context.Background(), node, []byte("ABC"), 2)

	if errno != 0 {
		t.Fatalf("Write error: %d", errno)
	}
	if n != 3 {
		t.Errorf("Bytes escritos esperados 3, obtenidos %d", n)
	}

	data, _ := contentCache.Open("file123")
	buf := make([]byte, 10)
	data.ReadAt(buf, 0)
	if string(buf) != "01ABC56789" {
		t.Errorf("Contenido esperado '01ABC56789', obtenido %q", string(buf))
	}

	if !node.inode.HasChanges() {
		t.Error("Write should mark hasChanges=true")
	}
}

func TestDriveItemNode_Write_ExtendsSize(t *testing.T) {
	tmpDir := t.TempDir()
	contentCache, _ := NewContentCache(tmpDir)
	contentCache.Insert("file123", []byte("abc"))

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "file123", Name: "test.txt", Size: 3}),
		nodeDeps: nodeDeps{
			contentCache: contentCache,
		},
	}

	node.Write(context.Background(), node, []byte("defgh"), 3)

	if node.inode.Size() != 8 {
		t.Errorf("Size esperado 8, obtenido %d", node.inode.Size())
	}
}

// ──── Setattr (truncate) ────

func TestDriveItemNode_Setattr_Truncate(t *testing.T) {
	tmpDir := t.TempDir()
	contentCache, _ := NewContentCache(tmpDir)
	contentCache.Insert("file123", []byte("0123456789"))

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "file123", Name: "test.txt", Size: 10}),
		nodeDeps: nodeDeps{
			contentCache: contentCache,
		},
	}

	var out fuse.AttrOut
	setIn := &fuse.SetAttrIn{}
	setIn.Valid = fuse.FATTR_SIZE
	setIn.Size = 5

	errno := node.Setattr(context.Background(), nil, setIn, &out)

	if errno != 0 {
		t.Fatalf("Setattr error: %d", errno)
	}
	if out.Size != 5 {
		t.Errorf("Size esperado 5, obtenido %d", out.Size)
	}
	if !node.inode.HasChanges() {
		t.Error("Truncate should mark hasChanges=true")
	}

	data, _ := contentCache.Open("file123")
	buf := make([]byte, 10)
	data.ReadAt(buf, 0)
	if string(buf[:5]) != "01234" {
		t.Errorf("Contenido truncado esperado '01234', obtenido %q", string(buf[:5]))
	}
}

func TestDriveItemNode_Setattr_Chmod(t *testing.T) {
	tmpDir := t.TempDir()
	contentCache, _ := NewContentCache(tmpDir)

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "file123", Name: "test.txt", Size: 0}),
		nodeDeps: nodeDeps{
			contentCache: contentCache,
		},
	}

	var out fuse.AttrOut
	setIn := &fuse.SetAttrIn{}
	setIn.Valid = fuse.FATTR_MODE
	setIn.Mode = 0600

	errno := node.Setattr(context.Background(), nil, setIn, &out)
	if errno != 0 {
		t.Fatalf("Setattr(chmod) error: %d", errno)
	}

	expectedMode := uint32(syscall.S_IFREG | 0600)
	if out.Mode != expectedMode {
		t.Errorf("Mode esperado %o, obtenido %o", expectedMode, out.Mode)
	}
	if node.inode.Mode() != expectedMode {
		t.Errorf("Inode.Mode() esperado %o, obtenido %o", expectedMode, node.inode.Mode())
	}
}

func TestDriveItemNode_Setattr_Chmod_Folder(t *testing.T) {
	tmpDir := t.TempDir()
	contentCache, _ := NewContentCache(tmpDir)

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "dir1", Name: "mydir", Folder: &graph.Folder{}}),
		nodeDeps: nodeDeps{
			contentCache: contentCache,
		},
	}

	var out fuse.AttrOut
	setIn := &fuse.SetAttrIn{}
	setIn.Valid = fuse.FATTR_MODE
	setIn.Mode = 0700

	errno := node.Setattr(context.Background(), nil, setIn, &out)
	if errno != 0 {
		t.Fatalf("Setattr(chmod) error: %d", errno)
	}

	expectedMode := uint32(syscall.S_IFDIR | 0700)
	if out.Mode != expectedMode {
		t.Errorf("Mode esperado %o, obtenido %o", expectedMode, out.Mode)
	}
}

func TestDriveItemNode_Setattr_Utimens(t *testing.T) {
	tmpDir := t.TempDir()
	contentCache, _ := NewContentCache(tmpDir)

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "file123", Name: "test.txt", Size: 0}),
		nodeDeps: nodeDeps{
			contentCache: contentCache,
		},
	}

	newTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)

	var out fuse.AttrOut
	setIn := &fuse.SetAttrIn{}
	setIn.Valid = fuse.FATTR_MTIME
	setIn.Mtime = uint64(newTime.Unix())

	errno := node.Setattr(context.Background(), nil, setIn, &out)
	if errno != 0 {
		t.Fatalf("Setattr(utimens) error: %d", errno)
	}

	if out.Mtime != setIn.Mtime {
		t.Errorf("Mtime esperado %d, obtenido %d", setIn.Mtime, out.Mtime)
	}
}

// ──── Create (requiere bridge FUSE para NewInode) ────

func TestDriveItemNode_Create_NewFile(t *testing.T) {
	t.Skip("Requires mounted FUSE bridge (NewInode) — integration test")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value": []}`))
	}))
	defer server.Close()

	graphClient := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	inodeCache := NewInodeCache()
	tmpDir := t.TempDir()
	contentCache, _ := NewContentCache(tmpDir)

	parentInode := NewInodeDriveItem(&graph.DriveItem{ID: "folder1", Name: "Docs", Folder: &graph.Folder{}})
	node := &DriveItemNode{
		inode: parentInode,
		nodeDeps: nodeDeps{
			graphClient:   graphClient,
			tokenProvider: &mockTokenProvider{token: "t"},
			inodeCache:    inodeCache,
			contentCache:  contentCache,
		},
	}

	var out fuse.EntryOut
	_, fh, flags, errno := node.Create(context.Background(), "nuevo.txt", syscall.O_WRONLY, 0644, &out)

	if errno != 0 {
		t.Fatalf("Create error: %d", errno)
	}
	if fh == nil {
		t.Fatal("Se esperaba un FileHandle")
	}
	if flags != 0 {
		t.Errorf("Flags esperados 0, obtenidos %d", flags)
	}
	if out.Mode&syscall.S_IFREG == 0 {
		t.Error("Mode should include S_IFREG")
	}

	childNode := fh.(*DriveItemNode)
	if !isLocalID(childNode.inode.ID()) {
		t.Error("The created file should have a local ID")
	}
	if !contentCache.HasContent(childNode.inode.ID()) {
		t.Error("The created file should exist in ContentCache")
	}
}

// ──── Flush ────

func TestDriveItemNode_Flush_ClosesFD(t *testing.T) {
	tmpDir := t.TempDir()
	contentCache, _ := NewContentCache(tmpDir)
	contentCache.Insert("file123", []byte("hello"))

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "file123", Name: "test.txt", Size: 5}),
		nodeDeps: nodeDeps{
			contentCache: contentCache,
		},
	}

	// Abrir primero para que haya un FD
	contentCache.Open("file123")
	if !contentCache.IsOpen("file123") {
		t.Fatal("Precondition: the file should be open")
	}

	errno := node.Flush(context.Background(), node)

	if errno != 0 {
		t.Errorf("Flush error: %d", errno)
	}
	if contentCache.IsOpen("file123") {
		t.Error("Flush should close the FD")
	}
}

// ──── Mkdir (requiere bridge FUSE para NewInode) ────

func TestDriveItemNode_Mkdir_Success(t *testing.T) {
	t.Skip("Requires mounted FUSE bridge (NewInode) — integration test")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "newfolder", "name": "Nueva Carpeta", "folder": {"childCount": 0}}`))
	}))
	defer server.Close()

	graphClient := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	inodeCache := NewInodeCache()
	tmpDir := t.TempDir()
	contentCache, _ := NewContentCache(tmpDir)

	parentInode := NewInodeDriveItem(&graph.DriveItem{ID: "folder1", Name: "Docs", Folder: &graph.Folder{}})
	inodeCache.Insert(parentInode)

	node := &DriveItemNode{
		inode: parentInode,
		nodeDeps: nodeDeps{
			graphClient:   graphClient,
			tokenProvider: &mockTokenProvider{token: "t"},
			inodeCache:    inodeCache,
			contentCache:  contentCache,
		},
	}

	var out fuse.EntryOut
	_, errno := node.Mkdir(context.Background(), "Nueva Carpeta", 0755, &out)

	if errno != 0 {
		t.Fatalf("Mkdir error: %d", errno)
	}
	if out.Mode&syscall.S_IFDIR == 0 {
		t.Error("Mode should include S_IFDIR")
	}

	// Verify that the inode was inserted into the cache
	children, _ := inodeCache.GetChildren(context.Background(), "folder1", nil)
	if _, exists := children["Nueva Carpeta"]; !exists {
		t.Error("The created folder should exist in InodeCache")
	}
}

// ──── Rmdir ────

func TestDriveItemNode_Rmdir_Success(t *testing.T) {
	var deletedID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capturar el ID del item a eliminar
		deletedID = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	graphClient := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	inodeCache := NewInodeCache()

	parentInode := NewInodeDriveItem(&graph.DriveItem{ID: "folder1", Name: "Docs", Folder: &graph.Folder{}})
	childInode := NewInodeDriveItem(&graph.DriveItem{ID: "subfolder1", Name: "SubCarpeta", Folder: &graph.Folder{}, Parent: &graph.DriveItemParent{ID: "folder1"}})
	// Do NOT call SetChildren → children=nil → HasChildren=false → "empty" folder
	inodeCache.Insert(parentInode)
	inodeCache.Insert(childInode)
	parentInode.SetChildren([]string{"subfolder1"})

	node := &DriveItemNode{
		inode: parentInode,
		nodeDeps: nodeDeps{
			graphClient:   graphClient,
			tokenProvider: &mockTokenProvider{token: "t"},
			inodeCache:    inodeCache,
		},
	}

	errno := node.Rmdir(context.Background(), "SubCarpeta")

	if errno != 0 {
		t.Fatalf("Rmdir error: %d", errno)
	}
	if deletedID == "" {
		t.Error("Se esperaba una llamada DELETE a Graph")
	}
}

func TestDriveItemNode_Rmdir_NotEmpty(t *testing.T) {
	inodeCache := NewInodeCache()

	parentInode := NewInodeDriveItem(&graph.DriveItem{ID: "folder1", Name: "Docs", Folder: &graph.Folder{}})
	childInode := NewInodeDriveItem(&graph.DriveItem{ID: "subfolder1", Name: "SubCarpeta", Folder: &graph.Folder{}, Parent: &graph.DriveItemParent{ID: "folder1"}})
	childInode.SetChildren([]string{"child1"}) // not empty
	inodeCache.Insert(parentInode)
	inodeCache.Insert(childInode)
	parentInode.SetChildren([]string{"subfolder1"})

	node := &DriveItemNode{
		inode: parentInode,
		nodeDeps: nodeDeps{
			inodeCache: inodeCache,
		},
	}

	errno := node.Rmdir(context.Background(), "SubCarpeta")
	if errno != syscall.ENOTEMPTY {
		t.Errorf("Se esperaba ENOTEMPTY, obtenido %d", errno)
	}
}

// ──── Unlink ────

func TestDriveItemNode_Unlink_Success(t *testing.T) {
	var deletedID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deletedID = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	graphClient := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	inodeCache := NewInodeCache()

	parentInode := NewInodeDriveItem(&graph.DriveItem{ID: "folder1", Name: "Docs", Folder: &graph.Folder{}})
	childInode := NewInodeDriveItem(&graph.DriveItem{ID: "file123", Name: "archivo.txt", Parent: &graph.DriveItemParent{ID: "folder1"}})
	inodeCache.Insert(parentInode)
	inodeCache.Insert(childInode)
	parentInode.SetChildren([]string{"file123"})

	tmpDir := t.TempDir()
	contentCache, _ := NewContentCache(tmpDir)

	node := &DriveItemNode{
		inode: parentInode,
		nodeDeps: nodeDeps{
			graphClient:   graphClient,
			tokenProvider: &mockTokenProvider{token: "t"},
			inodeCache:    inodeCache,
			contentCache:  contentCache,
		},
	}

	errno := node.Unlink(context.Background(), "archivo.txt")

	if errno != 0 {
		t.Fatalf("Unlink error: %d", errno)
	}
	if deletedID == "" {
		t.Error("Se esperaba una llamada DELETE a Graph")
	}
}

// ──── Rename ────

func TestDriveItemNode_Rename_Success(t *testing.T) {
	var patchCalled, moveCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" {
			patchCalled = true
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "file123", "name": "renombrado.txt"}`))
		}
	}))
	defer server.Close()

	graphClient := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	inodeCache := NewInodeCache()

	parentInode := NewInodeDriveItem(&graph.DriveItem{ID: "folder1", Name: "Docs", Folder: &graph.Folder{}})
	childInode := NewInodeDriveItem(&graph.DriveItem{ID: "file123", Name: "archivo.txt", Parent: &graph.DriveItemParent{ID: "folder1"}})
	inodeCache.Insert(parentInode)
	inodeCache.Insert(childInode)
	parentInode.SetChildren([]string{"file123"})

	node := &DriveItemNode{
		inode: parentInode,
		nodeDeps: nodeDeps{
			graphClient:   graphClient,
			tokenProvider: &mockTokenProvider{token: "t"},
			inodeCache:    inodeCache,
		},
	}

	errno := node.Rename(context.Background(), "archivo.txt", node, "renombrado.txt", 0)

	if errno != 0 {
		t.Fatalf("Rename error: %d", errno)
	}
	if !patchCalled {
		t.Error("Se esperaba una llamada PATCH a Graph")
	}
	if moveCalled {
		t.Error("No se esperaba MoveItem (mismo padre)")
	}
	if childInode.Name() != "renombrado.txt" {
		t.Errorf("Nombre esperado 'renombrado.txt', obtenido %q", childInode.Name())
	}
}
