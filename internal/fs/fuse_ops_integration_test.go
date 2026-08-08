package fs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// =============================================================================
// FUSE operations integration tests with httptest mock Graph API
// =============================================================================

// noopFetcher is a ChildrenFetcher that returns empty results. Used when
// the cache is pre-populated and we want to avoid nil fetcher panics.
func noopFetcher(_ context.Context, _ string) ([]graph.DriveItem, error) {
	return nil, nil
}

// testTP is a simple TokenProvider for tests.
type testTP struct{ token string }

func (t *testTP) GetAccessToken(_ context.Context) (string, error) { return t.token, nil }

// setupFuseTest creates a OneCloudFS with an httptest-backed Graph client,
// inode cache, and content cache. Returns the server, the root, and the caches.
func setupFuseTest(t *testing.T) (*httptest.Server, *OneCloudFS, *InodeCache, *ContentCache) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "children") {
			// ListDriveRoot or ListChildren — return empty value array
			w.Write([]byte(`{"value": []}`))
			return
		}
		if strings.Contains(r.URL.Path, "content") {
			http.NotFound(w, r)
			return
		}
		// Default: DriveItem
		json.NewEncoder(w).Encode(graph.DriveItem{
			ID:   "root",
			Name: "root",
		})
	}))

	gc := graph.NewClient(
		graph.WithBaseURL(server.URL),
		graph.WithHTTPClient(server.Client()),
	)

	ic := NewInodeCache()
	cc, err := NewContentCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewContentCache: %v", err)
	}

	root := NewOneCloudFS(gc, &testTP{"token"}, ic, cc, nil)
	return server, root, ic, cc
}

// =============================================================================
// Getattr
// =============================================================================

func TestOneCloudFS_Getattr_Integration(t *testing.T) {
	server, root, _, _ := setupFuseTest(t)
	defer server.Close()

	var out fuse.AttrOut
	errno := root.Getattr(context.Background(), nil, &out)
	if errno != 0 {
		t.Fatalf("Getattr failed: %v", errno)
	}
	if out.Mode&syscall.S_IFDIR == 0 {
		t.Error("root should be a directory")
	}
}

// =============================================================================
// Statfs
// =============================================================================

func TestOneCloudFS_Statfs(t *testing.T) {
	server, root, _, _ := setupFuseTest(t)
	defer server.Close()

	var out fuse.StatfsOut
	errno := root.Statfs(context.Background(), &out)
	if errno != 0 {
		t.Fatalf("Statfs failed: %v", errno)
	}
	if out.Bsize == 0 {
		t.Error("Bsize should not be 0")
	}
	if out.NameLen == 0 {
		t.Error("NameLen should not be 0")
	}
}

func TestDriveItemNode_Statfs_Integration(t *testing.T) {
	server, _, ic, cc := setupFuseTest(t)
	defer server.Close()

	// Create a DriveItemNode for a folder
	inode := NewInodeDriveItem(&graph.DriveItem{
		ID:     "folder-1",
		Name:   "Documents",
		Folder: &graph.Folder{ChildCount: 0},
	})

	node := &DriveItemNode{
		inode: inode,
		nodeDeps: nodeDeps{
			graphClient: graph.NewClient(
				graph.WithBaseURL(server.URL),
				graph.WithHTTPClient(server.Client()),
			),
			tokenProvider: &testTP{"token"},
			inodeCache:    ic,
			contentCache:  cc,
		},
	}

	var out fuse.StatfsOut
	errno := node.Statfs(context.Background(), &out)
	if errno != 0 {
		t.Fatalf("Statfs failed: %v", errno)
	}
	if out.Blocks == 0 {
		t.Error("Blocks should not be 0")
	}
}

// =============================================================================
// Mkdir via doMkdir
// =============================================================================

func TestDoMkdir_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CreateFolder response
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(graph.DriveItem{
			ID:     "new-folder-1",
			Name:   "NewFolder",
			Folder: &graph.Folder{ChildCount: 0},
		})
	}))
	defer server.Close()

	gc := graph.NewClient(
		graph.WithBaseURL(server.URL),
		graph.WithHTTPClient(server.Client()),
	)
	ic := NewInodeCache()

	deps := nodeDeps{
		graphClient:   gc,
		tokenProvider: &testTP{"token"},
		inodeCache:    ic,
	}

	var out fuse.EntryOut
	childInode, errno := deps.doMkdir(context.Background(), "root", "NewFolder", nil, &out)
	if errno != 0 {
		t.Fatalf("doMkdir failed: %v", errno)
	}
	if childInode.Name() != "NewFolder" {
		t.Errorf("expected NewFolder, got %s", childInode.Name())
	}
}

func TestDoMkdir_RestrictedName(t *testing.T) {
	deps := nodeDeps{inodeCache: NewInodeCache()}

	var out fuse.EntryOut
	_, errno := deps.doMkdir(context.Background(), "root", "CON", nil, &out)
	if errno != syscall.EINVAL {
		t.Errorf("expected EINVAL for CON, got %v", errno)
	}
}

// =============================================================================
// Create via doCreate
// =============================================================================

func TestDoCreate_NewFile(t *testing.T) {
	ic := NewInodeCache()
	cc, err := NewContentCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewContentCache: %v", err)
	}

	// Pre-populate root inode so GetChildren works without fetcher
	rootInode := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{ChildCount: 0}})
	rootInode.SetChildren([]string{})
	ic.Insert(rootInode)

	deps := nodeDeps{
		inodeCache:   ic,
		contentCache: cc,
	}

	var out fuse.EntryOut
	childNode, errno := deps.doCreate(context.Background(), "root", "newfile.txt", syscall.O_CREAT, 0644, nil, &out)
	if errno != 0 {
		t.Fatalf("doCreate failed: %v", errno)
	}
	if childNode.inode.Name() != "newfile.txt" {
		t.Errorf("expected newfile.txt, got %s", childNode.inode.Name())
	}
	if !childNode.inode.HasChanges() {
		t.Error("newly created file should have changes")
	}
}

func TestDoCreate_RestrictedName(t *testing.T) {
	cc, _ := NewContentCache(t.TempDir())
	deps := nodeDeps{inodeCache: NewInodeCache(), contentCache: cc}

	var out fuse.EntryOut
	_, errno := deps.doCreate(context.Background(), "root", "NUL", syscall.O_CREAT, 0644, nil, &out)
	if errno != syscall.EINVAL {
		t.Errorf("expected EINVAL for NUL, got %v", errno)
	}
}

func TestDoCreate_ExistingFileTruncates(t *testing.T) {
	ic := NewInodeCache()
	cc, _ := NewContentCache(t.TempDir())

	// Pre-populate root inode
	rootInode := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{ChildCount: 0}})
	rootInode.SetChildren([]string{})
	ic.Insert(rootInode)

	// First create
	deps := nodeDeps{inodeCache: ic, contentCache: cc}
	var out fuse.EntryOut
	_, errno := deps.doCreate(context.Background(), "root", "existing.txt", syscall.O_CREAT, 0644, nil, &out)
	if errno != 0 {
		t.Fatalf("first create failed: %v", errno)
	}

	// Second create with same name → should truncate
	childNode2, errno := deps.doCreate(context.Background(), "root", "existing.txt", syscall.O_CREAT, 0644, nil, &out)
	if errno != 0 {
		t.Fatalf("second create failed: %v", errno)
	}
	if childNode2.inode.Size() != 0 {
		t.Errorf("truncated file should have size 0, got %d", childNode2.inode.Size())
	}
}

// =============================================================================
// Rename via doRename
// =============================================================================

func TestDoRename_NotFound(t *testing.T) {
	ic := NewInodeCache()
	rootInode := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{ChildCount: 0}})
	rootInode.SetChildren([]string{})
	ic.Insert(rootInode)

	deps := nodeDeps{inodeCache: ic}

	errno := deps.doRename(context.Background(), "root", "ghost.txt", "root", "new.txt", noopFetcher)
	if errno != syscall.ENOENT {
		t.Errorf("expected ENOENT, got %v", errno)
	}
}

func TestDoRename_RestrictedName(t *testing.T) {
	ic := NewInodeCache()
	rootInode := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{ChildCount: 1}})
	rootInode.SetChildren([]string{})
	ic.Insert(rootInode)
	child := NewInodeDriveItem(&graph.DriveItem{ID: "f1", Name: "ok.txt"})
	ic.InsertChild("root", "ok.txt", child)

	deps := nodeDeps{inodeCache: ic}
	errno := deps.doRename(context.Background(), "root", "ok.txt", "root", "AUX", noopFetcher)
	if errno != syscall.EINVAL {
		t.Errorf("expected EINVAL for AUX, got %v", errno)
	}
}

// =============================================================================
// isNameRestricted
// =============================================================================

func TestIsNameRestricted_Extended(t *testing.T) {
	restricted := []string{"CON", "AUX", "PRN", "NUL", "desktop.ini", ".lock", "file<name", "a:b", "LPT1", "COM3", "_vti_"}
	for _, name := range restricted {
		if !isNameRestricted(name) {
			t.Errorf("expected %q to be restricted", name)
		}
	}
	allowed := []string{"normal.txt", "file-name.pdf", "my_document", "123"}
	for _, name := range allowed {
		if isNameRestricted(name) {
			t.Errorf("expected %q to be allowed", name)
		}
	}
}

// =============================================================================
// resolveNewParentID (edge case already tested in coverage_test.go)
// =============================================================================

func TestResolveNewParentID_DriveItemNode(t *testing.T) {
	inode := &Inode{}
	inode.DriveItem.ID = "folder-xyz"
	node := &DriveItemNode{inode: inode}
	if got := resolveNewParentID(node); got != "folder-xyz" {
		t.Errorf("expected folder-xyz, got %q", got)
	}
}

// =============================================================================
// fillEntryOut
// =============================================================================

func TestFillEntryOut(t *testing.T) {
	item := &graph.DriveItem{
		ID:   "item-1",
		Name: "test.txt",
		Size: 500,
	}

	var out fuse.EntryOut
	fillEntryOut(&out, item)

	if out.Attr.Size != 500 {
		t.Errorf("expected size 500, got %d", out.Attr.Size)
	}
}

// =============================================================================
// DriveItemNode Getattr
// =============================================================================

func TestDriveItemNode_Getattr(t *testing.T) {
	inode := NewInodeDriveItem(&graph.DriveItem{
		ID:   "file-1",
		Name: "doc.txt",
		Size: 42,
	})

	node := &DriveItemNode{inode: inode}

	var out fuse.AttrOut
	errno := node.Getattr(context.Background(), nil, &out)
	if errno != 0 {
		t.Fatalf("Getattr failed: %v", errno)
	}
	if out.Size != 42 {
		t.Errorf("expected size 42, got %d", out.Size)
	}
}

// =============================================================================
// DriveItemNode Mkdir (via fuseMkdir)
// =============================================================================

func TestDriveItemNode_Mkdir_NotDir(t *testing.T) {
	inode := NewInodeDriveItem(&graph.DriveItem{
		ID:   "file-1",
		Name: "doc.txt",
		Size: 10,
	})
	node := &DriveItemNode{inode: inode}

	var out fuse.EntryOut
	_, errno := node.Mkdir(context.Background(), "sub", 0755, &out)
	if errno != syscall.ENOTDIR {
		t.Errorf("expected ENOTDIR for file, got %v", errno)
	}
}

func TestDriveItemNode_Create_NotDir(t *testing.T) {
	inode := NewInodeDriveItem(&graph.DriveItem{
		ID:   "file-1",
		Name: "doc.txt",
		Size: 10,
	})
	node := &DriveItemNode{inode: inode}

	var out fuse.EntryOut
	_, _, _, errno := node.Create(context.Background(), "new.txt", syscall.O_CREAT, 0644, &out)
	if errno != syscall.ENOTDIR {
		t.Errorf("expected ENOTDIR for file, got %v", errno)
	}
}

// =============================================================================
// OneCloudFS FUSE wrappers (delegate to nodeDeps)
// =============================================================================

func TestOneCloudFS_FuseWrappers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "children") {
			w.Write([]byte(`{"value": []}`))
			return
		}
		json.NewEncoder(w).Encode(graph.DriveItem{
			ID:     "root",
			Name:   "root",
			Folder: &graph.Folder{ChildCount: 0},
		})
	}))
	defer server.Close()

	gc := graph.NewClient(
		graph.WithBaseURL(server.URL),
		graph.WithHTTPClient(server.Client()),
	)
	ic := NewInodeCache()
	cc, _ := NewContentCache(t.TempDir())

	root := NewOneCloudFS(gc, &testTP{"token"}, ic, cc, nil)

	// GetFolderID
	if id := root.GetFolderID(); id != "root" {
		t.Errorf("expected 'root', got %q", id)
	}

	// Readdir (empty root) - just check it doesn't error
	stream, errno := root.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed: %v", errno)
	}
	if stream == nil {
		t.Fatal("stream should not be nil")
	}
	// Don't call Next() on potentially empty stream — go-fuse panics
	// on double consumption of exhausted dirArray. Just verify no error.
}

// =============================================================================
// DriveItemNode: Readdir, Lookup, Rmdir, Unlink on non-dirs
// =============================================================================

func TestDriveItemNode_Readdir_NotDir(t *testing.T) {
	inode := NewInodeDriveItem(&graph.DriveItem{ID: "f1", Name: "file.txt"})
	node := &DriveItemNode{inode: inode}

	_, errno := node.Readdir(context.Background())
	if errno != syscall.ENOTDIR {
		t.Errorf("expected ENOTDIR, got %v", errno)
	}
}

func TestDriveItemNode_Lookup_NotDir(t *testing.T) {
	inode := NewInodeDriveItem(&graph.DriveItem{ID: "f1", Name: "file.txt"})
	node := &DriveItemNode{inode: inode}

	var out fuse.EntryOut
	_, errno := node.Lookup(context.Background(), "child", &out)
	if errno != syscall.ENOTDIR {
		t.Errorf("expected ENOTDIR, got %v", errno)
	}
}

func TestDriveItemNode_Rmdir_NotDir(t *testing.T) {
	inode := NewInodeDriveItem(&graph.DriveItem{ID: "f1", Name: "file.txt"})
	node := &DriveItemNode{inode: inode}

	errno := node.Rmdir(context.Background(), "sub")
	if errno != syscall.ENOTDIR {
		t.Errorf("expected ENOTDIR, got %v", errno)
	}
}

func TestDriveItemNode_Unlink_NotDir(t *testing.T) {
	inode := NewInodeDriveItem(&graph.DriveItem{ID: "f1", Name: "file.txt"})
	node := &DriveItemNode{inode: inode}

	errno := node.Unlink(context.Background(), "sub")
	if errno != syscall.ENOTDIR {
		t.Errorf("expected ENOTDIR, got %v", errno)
	}
}

func TestDriveItemNode_Rename_NotDir(t *testing.T) {
	inode := NewInodeDriveItem(&graph.DriveItem{ID: "f1", Name: "file.txt"})
	node := &DriveItemNode{inode: inode}

	errno := node.Rename(context.Background(), "old", &OneCloudFS{}, "new", 0)
	if errno != syscall.ENOTDIR {
		t.Errorf("expected ENOTDIR, got %v", errno)
	}
}

// =============================================================================
// DriveItemNode GetFolderID
// =============================================================================

func TestDriveItemNode_GetFolderID(t *testing.T) {
	inode := NewInodeDriveItem(&graph.DriveItem{ID: "folder-abc", Name: "docs", Folder: &graph.Folder{}})
	node := &DriveItemNode{inode: inode}

	if id := node.GetFolderID(); id != "folder-abc" {
		t.Errorf("expected folder-abc, got %q", id)
	}
}

// =============================================================================
// DriveItemNode Open on directory
// =============================================================================

func TestDriveItemNode_Open_Dir(t *testing.T) {
	inode := NewInodeDriveItem(&graph.DriveItem{
		ID:     "dir-1",
		Name:   "docs",
		Folder: &graph.Folder{},
	})
	node := &DriveItemNode{inode: inode}

	_, _, errno := node.Open(context.Background(), syscall.O_RDONLY)
	if errno != syscall.EISDIR {
		t.Errorf("expected EISDIR, got %v", errno)
	}
}
