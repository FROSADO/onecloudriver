package fs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// ──── Mkdir (requiere bridge FUSE para NewInode) ────

func TestOneCloudFS_Mkdir_Success(t *testing.T) {
	t.Skip("Requires mounted FUSE bridge (NewInode) — integration test")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "newfolder", "name": "Nueva Carpeta", "folder": {"childCount": 0}}`))
	}))
	defer server.Close()

	graphClient := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	inodeCache := NewInodeCache()

	root := &OneCloudFS{
		nodeDeps: nodeDeps{
			graphClient:   graphClient,
			tokenProvider: &mockTokenProvider{token: "t"},
			inodeCache:    inodeCache,
		},
	}

	var out fuse.EntryOut
	_, errno := root.Mkdir(context.Background(), "Nueva Carpeta", 0755, &out)

	if errno != 0 {
		t.Fatalf("Mkdir error: %d", errno)
	}
	if out.Mode&syscall.S_IFDIR == 0 {
		t.Error("Mode should include S_IFDIR")
	}

	children, _ := inodeCache.GetChildren(context.Background(), "root", nil)
	if _, exists := children["Nueva Carpeta"]; !exists {
		t.Error("The created folder should exist in InodeCache")
	}
}

// ──── Rmdir ────

func TestOneCloudFS_Rmdir_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	graphClient := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	inodeCache := NewInodeCache()

	childInode := NewInodeDriveItem(&graph.DriveItem{
		ID: "subfolder1", Name: "SubCarpeta", Folder: &graph.Folder{},
		Parent: &graph.DriveItemParent{ID: "root"},
	})
	// children = nil → HasChildren=false → "empty" folder
	inodeCache.Insert(childInode)

	rootInode := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	rootInode.SetChildren([]string{"subfolder1"})
	inodeCache.Insert(rootInode)

	root := &OneCloudFS{
		nodeDeps: nodeDeps{
			graphClient:   graphClient,
			tokenProvider: &mockTokenProvider{token: "t"},
			inodeCache:    inodeCache,
		},
	}

	errno := root.Rmdir(context.Background(), "SubCarpeta")
	if errno != 0 {
		t.Fatalf("Rmdir error: %d", errno)
	}
}

func TestOneCloudFS_Rmdir_NotEmpty(t *testing.T) {
	inodeCache := NewInodeCache()

	childInode := NewInodeDriveItem(&graph.DriveItem{
		ID: "subfolder1", Name: "SubCarpeta", Folder: &graph.Folder{},
		Parent: &graph.DriveItemParent{ID: "root"},
	})
	childInode.SetChildren([]string{"child1"}) // not empty
	inodeCache.Insert(childInode)

	rootInode := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	rootInode.SetChildren([]string{"subfolder1"})
	inodeCache.Insert(rootInode)

	root := &OneCloudFS{nodeDeps: nodeDeps{inodeCache: inodeCache}}
	errno := root.Rmdir(context.Background(), "SubCarpeta")
	if errno != syscall.ENOTEMPTY {
		t.Errorf("Se esperaba ENOTEMPTY, obtenido %d", errno)
	}
}

// ──── Unlink ────

func TestOneCloudFS_Unlink_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	graphClient := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	inodeCache := NewInodeCache()
	tmpDir := t.TempDir()
	contentCache, _ := NewContentCache(tmpDir)

	childInode := NewInodeDriveItem(&graph.DriveItem{
		ID: "file1", Name: "archivo.txt", Parent: &graph.DriveItemParent{ID: "root"},
	})
	inodeCache.Insert(childInode)

	rootInode := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	rootInode.SetChildren([]string{"file1"})
	inodeCache.Insert(rootInode)

	root := &OneCloudFS{
		nodeDeps: nodeDeps{
			graphClient:   graphClient,
			tokenProvider: &mockTokenProvider{token: "t"},
			inodeCache:    inodeCache,
			contentCache:  contentCache,
		},
	}

	errno := root.Unlink(context.Background(), "archivo.txt")
	if errno != 0 {
		t.Fatalf("Unlink error: %d", errno)
	}
}

// ──── Create (requiere bridge FUSE para NewInode) ────

func TestOneCloudFS_Create_NewFile(t *testing.T) {
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

	rootInode := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	rootInode.SetChildren([]string{})
	inodeCache.Insert(rootInode)

	root := &OneCloudFS{
		nodeDeps: nodeDeps{
			graphClient:   graphClient,
			tokenProvider: &mockTokenProvider{token: "t"},
			inodeCache:    inodeCache,
			contentCache:  contentCache,
		},
	}

	var out fuse.EntryOut
	_, fh, flags, errno := root.Create(context.Background(), "nuevo.txt", syscall.O_WRONLY, 0644, &out)

	if errno != 0 {
		t.Fatalf("Create error: %d", errno)
	}
	if fh == nil {
		t.Fatal("Se esperaba un FileHandle")
	}
	if flags != 0 {
		t.Errorf("Flags esperados 0, obtenidos %d", flags)
	}

	childNode := fh.(*DriveItemNode)
	if !isLocalID(childNode.inode.ID()) {
		t.Error("The created file should have a local ID")
	}
}

func TestOneCloudFS_Create_ExistingFile(t *testing.T) {
	t.Skip("Requires mounted FUSE bridge (NewInode) — integration test")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value": [{"id": "file1", "name": "existente.txt", "size": 100}]}`))
	}))
	defer server.Close()

	graphClient := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	inodeCache := NewInodeCache()
	tmpDir := t.TempDir()
	contentCache, _ := NewContentCache(tmpDir)
	contentCache.Insert("file1", []byte("contenido viejo"))

	rootInode := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	rootInode.SetChildren([]string{"file1"})
	inodeCache.Insert(rootInode)

	childInode := NewInodeDriveItem(&graph.DriveItem{ID: "file1", Name: "existente.txt", Parent: &graph.DriveItemParent{ID: "root"}})
	inodeCache.Insert(childInode)

	root := &OneCloudFS{
		nodeDeps: nodeDeps{
			graphClient:   graphClient,
			tokenProvider: &mockTokenProvider{token: "t"},
			inodeCache:    inodeCache,
			contentCache:  contentCache,
		},
	}

	var out fuse.EntryOut
	_, fh, _, errno := root.Create(context.Background(), "existente.txt", syscall.O_WRONLY, 0644, &out)

	if errno != 0 {
		t.Fatalf("Create error: %d", errno)
	}

	childNode := fh.(*DriveItemNode)
	if childNode.inode.Size() != 0 {
		t.Errorf("The existing file should be truncated to 0, Size=%d", childNode.inode.Size())
	}
	if !childNode.inode.HasChanges() {
		t.Error("The truncated file should be marked as dirty")
	}
}
