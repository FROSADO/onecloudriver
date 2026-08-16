package fs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// TestDriveItemNode_Getattr_File tests Getattr for a file
func TestDriveItemNode_Getattr_File(t *testing.T) {
	modTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	item := &graph.DriveItem{
		ID:      "file123",
		Name:    "documento.pdf",
		Size:    1024000,
		ModTime: &modTime,
	}

	node := &DriveItemNode{
		inode: NewInodeDriveItem(item),
	}

	var out fuse.AttrOut
	errno := node.Getattr(context.Background(), nil, &out)

	if errno != 0 {
		t.Errorf("Expected errno 0, got %d", errno)
	}
	if out.Mode&syscall.S_IFREG == 0 {
		t.Error("Should be a regular file (S_IFREG)")
	}
	if out.Mode&syscall.S_IFDIR != 0 {
		t.Error("Should not be a directory")
	}
	if out.Mode&0777 != 0644 {
		t.Errorf("Expected permissions 0644, got %o", out.Mode&0777)
	}
	if out.Size != 1024000 {
		t.Errorf("Expected size 1024000, got %d", out.Size)
	}
	expectedMtime := uint64(modTime.Unix()) //#nosec G115 -- test timestamp, always >= 0
	if out.Mtime != expectedMtime {
		t.Errorf("Expected mtime %d, got %d", expectedMtime, out.Mtime)
	}
}

// TestDriveItemNode_Getattr_Folder tests Getattr for a folder
func TestDriveItemNode_Getattr_Folder(t *testing.T) {
	item := &graph.DriveItem{
		ID:     "folder123",
		Name:   "Documentos",
		Folder: &graph.Folder{ChildCount: 5},
	}

	node := &DriveItemNode{
		inode: NewInodeDriveItem(item),
	}

	var out fuse.AttrOut
	errno := node.Getattr(context.Background(), nil, &out)

	if errno != 0 {
		t.Errorf("Expected errno 0, got %d", errno)
	}
	if out.Mode&syscall.S_IFDIR == 0 {
		t.Error("Should be a directory (S_IFDIR)")
	}
	if out.Mode&syscall.S_IFREG != 0 {
		t.Error("Should not be a regular file")
	}
	if out.Mode&0777 != 0755 {
		t.Errorf("Expected permissions 0755, got %o", out.Mode&0777)
	}
	if out.Nlink != 2 {
		t.Errorf("Expected nlink 2, got %d", out.Nlink)
	}
}

// TestDriveItemNode_Getattr_NilModTime tests that it does not panic with a nil ModTime
func TestDriveItemNode_Getattr_NilModTime(t *testing.T) {
	item := &graph.DriveItem{
		ID:      "file123",
		Name:    "archivo.txt",
		Size:    100,
		ModTime: nil,
	}

	node := &DriveItemNode{
		inode: NewInodeDriveItem(item),
	}

	var out fuse.AttrOut
	errno := node.Getattr(context.Background(), nil, &out)

	if errno != 0 {
		t.Errorf("Expected errno 0, got %d", errno)
	}
	// ModTime nil -> ModTimeUnix() returns 0 (epoch = "unknown time")
	if out.Mtime != 0 {
		t.Logf("Mtime = %d (expected 0 when ModTime is nil)", out.Mtime)
	}
}

// TestDriveItemNode_Readdir_CacheMiss tests Readdir when the cache is empty
func TestDriveItemNode_Readdir_CacheMiss(t *testing.T) {
	var callCount int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"value": [
				{"id": "file1", "name": "archivo.txt", "size": 100},
				{"id": "folder1", "name": "Carpeta", "folder": {"childCount": 2}}
			]
		}`))
	}))
	defer server.Close()

	graphClient := &graph.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	inodeCache := NewInodeCache()
	tokenProvider := &mockTokenProvider{token: "test_token"}

	item := &graph.DriveItem{
		ID:     "folder123",
		Name:   "Documentos",
		Folder: &graph.Folder{ChildCount: 2},
	}

	node := &DriveItemNode{
		inode: NewInodeDriveItem(item),
		nodeDeps: nodeDeps{
			graphClient:   graphClient,
			tokenProvider: tokenProvider,
			inodeCache:    inodeCache,
		},
	}

	stream, errno := node.Readdir(context.Background())

	if errno != 0 {
		t.Fatalf("Expected errno 0, got %d", errno)
	}

	entries := make([]fuse.DirEntry, 0, 4)
	for stream.HasNext() {
		entry, errno := stream.Next()
		if errno != 0 {
			t.Fatalf("Error reading stream: %d", errno)
		}
		entries = append(entries, entry)
	}

	if len(entries) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(entries))
	}
	if callCount != 1 {
		t.Errorf("Expected 1 Graph call, got %d", callCount)
	}
}

// TestDriveItemNode_Readdir_CacheHit tests Readdir when the cache has data
func TestDriveItemNode_Readdir_CacheHit(t *testing.T) {
	var callCount int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value": [{"id": "file1", "name": "archivo.txt", "size": 100}]}`))
	}))
	defer server.Close()

	graphClient := &graph.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	inodeCache := NewInodeCache()
	tokenProvider := &mockTokenProvider{token: "test_token"}

	item := &graph.DriveItem{
		ID:     "folder123",
		Name:   "Documentos",
		Folder: &graph.Folder{ChildCount: 1},
	}

	node := &DriveItemNode{
		inode: NewInodeDriveItem(item),
		nodeDeps: nodeDeps{
			graphClient:   graphClient,
			tokenProvider: tokenProvider,
			inodeCache:    inodeCache,
		},
	}

	// First call: fills the cache
	_, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("First call failed: %d", errno)
	}

	// Second call: should use the cache
	_, errno = node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Second call failed: %d", errno)
	}

	if callCount != 1 {
		t.Errorf("Expected 1 call to Graph (cache hit), got %d", callCount)
	}
}

// TestDriveItemNode_Lookup_Success tests Lookup when the file exists
func TestDriveItemNode_Lookup_Success(t *testing.T) {
	t.Skip("Requires mounted FUSE bridge (NewInode) — integration test")
}

// TestDriveItemNode_Readdir_OfflineFallback_Subfolder reproduce el bug del
// test offline real: antes, DriveItemNode.fetchChildren llamaba directamente
// a fetchChildrenFromGraph sin el fallback offline (solo OneCloudFS.fetchChildren
// had it). When navigating to a SUBFOLDER (e.g. onedriver_tests/paging) with the
// network down, Readdir returned EIO even though the metadata was cached.
// Ahora ambos fetchers delegan en nodeDeps.fetchChildrenWithOffline.
func TestDriveItemNode_Readdir_OfflineFallback_Subfolder(t *testing.T) {
	// HTTP client with broken proxy → real network error (connection refused)
	transport := &http.Transport{
		Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: "127.0.0.1:1"}),
	}
	graphClient := &graph.Client{
		BaseURL:    "https://graph.microsoft.com/v1.0",
		HTTPClient: &http.Client{Transport: transport},
	}
	tokenProvider := &mockTokenProvider{token: "test_token"}
	inodeCache := NewInodeCache()

	// Subfolder with cached metadata (like the 250 paging files),
	// with stale children (restored from a previous session)
	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "subfolder1", Name: "paging", Folder: &graph.Folder{ChildCount: 250},
		Parent: &graph.DriveItemParent{ID: "root"},
	})
	parent.SetChildren([]string{"f0", "f1"})
	parent.Lock()
	parent.childrenCachedAt = time.Now().Add(-2 * time.Hour) // stale
	parent.childrenLastAccess = time.Now().Add(-2 * time.Hour)
	parent.Unlock()
	inodeCache.Insert(parent)
	inodeCache.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "f0", Name: "0.txt", Size: 10, Parent: &graph.DriveItemParent{ID: "subfolder1"},
	}))
	inodeCache.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "f1", Name: "1.txt", Size: 20, Parent: &graph.DriveItemParent{ID: "subfolder1"},
	}))

	node := &DriveItemNode{
		inode: parent,
		nodeDeps: nodeDeps{
			graphClient:   graphClient,
			tokenProvider: tokenProvider,
			inodeCache:    inodeCache,
		},
	}

	// Readdir with the network down → must serve the cached metadata
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Offline Readdir in a subfolder should serve the cache, errno: %d", errno)
	}

	entries := make([]fuse.DirEntry, 0, 4)
	for stream.HasNext() {
		entry, errno := stream.Next()
		if errno != 0 {
			t.Fatalf("Error reading stream: %d", errno)
		}
		entries = append(entries, entry)
	}
	if len(entries) != 2 {
		t.Errorf("Expected 2 cached entries, got %d", len(entries))
	}
	if !inodeCache.IsOffline() {
		t.Error("Offline mode should have been activated")
	}
}

// TestDriveItemNode_Lookup_NotFound tests Lookup when the file does not exist
func TestDriveItemNode_Lookup_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value": []}`))
	}))
	defer server.Close()

	graphClient := &graph.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	inodeCache := NewInodeCache()
	tokenProvider := &mockTokenProvider{token: "test_token"}

	item := &graph.DriveItem{
		ID:     "folder123",
		Name:   "Documentos",
		Folder: &graph.Folder{ChildCount: 0},
	}

	node := &DriveItemNode{
		inode: NewInodeDriveItem(item),
		nodeDeps: nodeDeps{
			graphClient:   graphClient,
			tokenProvider: tokenProvider,
			inodeCache:    inodeCache,
		},
	}

	var out fuse.EntryOut
	_, errno := node.Lookup(context.Background(), "no_existe.txt", &out)

	if errno != syscall.ENOENT {
		t.Errorf("Expected errno ENOENT, got %d", errno)
	}
}

// TestDriveItemNode_Open_ReadOnly tests Open with read-only flags
func TestDriveItemNode_Open_ReadOnly(t *testing.T) {
	item := &graph.DriveItem{
		ID:   "file123",
		Name: "archivo.txt",
		Size: 100,
	}

	tmpDir := t.TempDir()
	contentCache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("Error creando ContentCache: %v", err)
	}
	if err := contentCache.Insert(item.ID, []byte("contenido de prueba")); err != nil {
		t.Fatalf("Error inserting content: %v", err)
	}

	node := &DriveItemNode{
		inode: NewInodeDriveItem(item),
		nodeDeps: nodeDeps{
			contentCache: contentCache,
		},
	}

	fh, flags, errno := node.Open(context.Background(), syscall.O_RDONLY)

	if errno != 0 {
		t.Errorf("Expected errno 0, got %d", errno)
	}
	if fh == nil {
		t.Error("Expected a FileHandle, got nil")
	}
	if flags != fuse.FOPEN_KEEP_CACHE {
		t.Errorf("Expected flags FOPEN_KEEP_CACHE (%d), got %d", fuse.FOPEN_KEEP_CACHE, flags)
	}
}

// TestDriveItemNode_Open_VerifyHashMatch verifies that cached content whose
// hash matches the server quickXorHash is served without re-downloading.
func TestDriveItemNode_Open_VerifyHashMatch(t *testing.T) {
	content := []byte("contenido de prueba")
	contentHash := graph.SumQuickXORHash(content)

	tmpDir := t.TempDir()
	contentCache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("Error creando ContentCache: %v", err)
	}
	if err := contentCache.Insert("file123", content); err != nil {
		t.Fatalf("Error inserting content: %v", err)
	}

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{
			ID:   "file123",
			Name: "archivo.txt",
			Size: uint64(len(content)),
			File: &graph.File{Hashes: graph.Hashes{QuickXorHash: contentHash}},
		}),
		nodeDeps: nodeDeps{
			contentCache: contentCache,
		},
	}

	fh, flags, errno := node.Open(context.Background(), syscall.O_RDONLY)
	if errno != 0 {
		t.Fatalf("Expected errno 0, got %d", errno)
	}
	if fh == nil {
		t.Error("Expected a FileHandle, got nil")
	}
	if flags != fuse.FOPEN_KEEP_CACHE {
		t.Errorf("Expected flags FOPEN_KEEP_CACHE, got %d", flags)
	}
	if !contentCache.HasContent("file123") {
		t.Error("Cached content should be preserved (hash matches)")
	}
}

// TestDriveItemNode_Open_VerifyHashMismatch_Redownloads verifies that cached
// content whose hash does not match the server quickXorHash is invalidated
// and re-downloaded on Open (issue #32).
func TestDriveItemNode_Open_VerifyHashMismatch_Redownloads(t *testing.T) {
	freshContent := []byte("contenido fresco descargado")
	freshHash := graph.SumQuickXORHash(freshContent)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/me/drive/items/remote_file":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"remote_file","name":"remoto.txt","size":` + itoa(len(freshContent)) + `,"file":{"hashes":{"quickXorHash":"` + freshHash + `"}}}`))
		case "/me/drive/items/remote_file/content":
			if r.Header.Get("Range") != "" {
				w.Header().Set("Content-Range", "bytes 0-"+itoa(len(freshContent)-1)+"/"+itoa(len(freshContent)))
				w.Header().Set("Content-Length", itoa(len(freshContent)))
				w.WriteHeader(http.StatusPartialContent)
			}
			w.Write(freshContent)
		}
	}))
	defer server.Close()

	graphClient := &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	tmpDir := t.TempDir()
	contentCache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("Error creando ContentCache: %v", err)
	}
	// Stale content whose hash does NOT match the server.
	if err := contentCache.Insert("remote_file", []byte("contenido obsoleto")); err != nil {
		t.Fatalf("Error inserting stale content: %v", err)
	}

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{
			ID:   "remote_file",
			Name: "remoto.txt",
			Size: uint64(len(freshContent)),
			File: &graph.File{Hashes: graph.Hashes{QuickXorHash: freshHash}},
		}),
		nodeDeps: nodeDeps{
			graphClient:   graphClient,
			tokenProvider: tokenProvider,
			contentCache:  contentCache,
		},
	}
	defer contentCache.Close("remote_file")

	fh, _, errno := node.Open(context.Background(), syscall.O_RDONLY)
	if errno != 0 {
		t.Fatalf("Expected errno 0, got %d", errno)
	}
	if fh == nil {
		t.Fatal("Expected a FileHandle, got nil")
	}

	diskData, err := os.ReadFile(contentCache.contentPath("remote_file"))
	if err != nil {
		t.Fatalf("Error reading cached content: %v", err)
	}
	if string(diskData) != string(freshContent) {
		t.Errorf("Expected re-downloaded content %q, got %q", string(freshContent), string(diskData))
	}
}

// TestDriveItemNode_Open_WriteAllowed tests that Open accepts writing (O_WRONLY)
func TestDriveItemNode_Open_WriteAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	contentCache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("Error creando ContentCache: %v", err)
	}
	if err := contentCache.Insert("file123", []byte("contenido")); err != nil {
		t.Fatalf("Error inserting content: %v", err)
	}

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "file123", Name: "archivo.txt", Size: 100}),
		nodeDeps: nodeDeps{
			contentCache: contentCache,
		},
	}

	_, _, errno := node.Open(context.Background(), syscall.O_WRONLY)

	if errno != 0 {
		t.Errorf("Expected errno 0 (write allowed), got %d", errno)
	}
}

// TestDriveItemNode_Open_ReadWrite tests Open with O_RDWR
func TestDriveItemNode_Open_ReadWrite(t *testing.T) {
	tmpDir := t.TempDir()
	contentCache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("Error creando ContentCache: %v", err)
	}
	if err := contentCache.Insert("file123", []byte("contenido")); err != nil {
		t.Fatalf("Error inserting content: %v", err)
	}

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "file123", Name: "archivo.txt", Size: 100}),
		nodeDeps: nodeDeps{
			contentCache: contentCache,
		},
	}

	_, _, errno := node.Open(context.Background(), syscall.O_RDWR)

	if errno != 0 {
		t.Errorf("Expected errno 0 (read-write allowed), got %d", errno)
	}
}

// TestDriveItemNode_Open_FolderDenied tests that Open rejects folders
func TestDriveItemNode_Open_FolderDenied(t *testing.T) {
	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{
			ID: "folder123", Name: "Carpeta", Folder: &graph.Folder{ChildCount: 0},
		}),
	}

	_, _, errno := node.Open(context.Background(), syscall.O_RDONLY)

	if errno != syscall.EISDIR {
		t.Errorf("Expected errno EISDIR, got %d", errno)
	}
}

// TestDriveItemNode_Read_Success tests Read with different offsets
func TestDriveItemNode_Read_Success(t *testing.T) {
	content := []byte("Hola mundo!")

	tmpDir := t.TempDir()
	contentCache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("Error creando ContentCache: %v", err)
	}
	if err := contentCache.Insert("file123", content); err != nil {
		t.Fatalf("Error inserting content: %v", err)
	}

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "file123", Name: "archivo.txt", Size: 11}),
		nodeDeps: nodeDeps{
			contentCache: contentCache,
		},
	}

	dest := make([]byte, 100)
	result, errno := node.Read(context.Background(), node, dest, 0)

	if errno != 0 {
		t.Fatalf("Expected errno 0, got %d", errno)
	}

	data, _ := result.Bytes(dest)
	if string(data) != "Hola mundo!" {
		t.Errorf("Expected content 'Hola mundo!', got '%s'", string(data))
	}

	result, errno = node.Read(context.Background(), node, dest, 5)
	if errno != 0 {
		t.Fatalf("Expected errno 0, got %d", errno)
	}

	data, _ = result.Bytes(dest)
	if string(data) != "mundo!" {
		t.Errorf("Expected content 'mundo!', got '%s'", string(data))
	}
}

// TestDriveItemNode_Open_DownloadsFromHTTP tests that Open downloads from OneDrive
func TestDriveItemNode_Open_DownloadsFromHTTP(t *testing.T) {
	content := []byte("contenido remoto desde HTTP")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/me/drive/items/remote_file":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "remote_file", "name": "remoto.txt", "size": ` + itoa(len(content)) + `}`))
		case "/me/drive/items/remote_file/content":
			if r.Header.Get("Range") != "" {
				w.Header().Set("Content-Range", "bytes 0-"+itoa(len(content)-1)+"/"+itoa(len(content)))
				w.Header().Set("Content-Length", itoa(len(content)))
				w.WriteHeader(http.StatusPartialContent)
			}
			w.Write(content)
		}
	}))
	defer server.Close()

	graphClient := &graph.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	tmpDir := t.TempDir()
	contentCache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("Error creando ContentCache: %v", err)
	}
	defer contentCache.Close("remote_file")

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "remote_file", Name: "remoto.txt", Size: uint64(len(content))}),
		nodeDeps: nodeDeps{
			graphClient:   graphClient,
			tokenProvider: tokenProvider,
			contentCache:  contentCache,
		},
	}

	fh, flags, errno := node.Open(context.Background(), syscall.O_RDONLY)
	if errno != 0 {
		t.Fatalf("Unexpected Open error: %d", errno)
	}
	if fh == nil {
		t.Fatal("Expected a non-nil FileHandle")
	}
	if flags != fuse.FOPEN_KEEP_CACHE {
		t.Errorf("Expected flags FOPEN_KEEP_CACHE (%d), got %d", fuse.FOPEN_KEEP_CACHE, flags)
	}

	if !contentCache.HasContent("remote_file") {
		t.Error("HasContent should be true after downloading")
	}

	diskData, err := os.ReadFile(contentCache.contentPath("remote_file"))
	if err != nil {
		t.Fatalf("Error reading cached content: %v", err)
	}
	if string(diskData) != string(content) {
		t.Errorf("Expected content %q, got %q", string(content), string(diskData))
	}
}

// ──── Open: paths de escritura ────

func TestDriveItemNode_Open_WithContentOnDisk_NoDownload(t *testing.T) {
	tmpDir := t.TempDir()
	contentCache, _ := NewContentCache(tmpDir)
	contentCache.Insert("file123", []byte("contenido local"))

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "file123", Name: "local.txt", Size: 15}),
		nodeDeps: nodeDeps{
			contentCache: contentCache,
		},
	}

	fh, flags, errno := node.Open(context.Background(), syscall.O_RDONLY)
	if errno != 0 {
		t.Fatalf("Open with on-disk content error: %d", errno)
	}
	if fh == nil {
		t.Fatal("FileHandle should not be nil")
	}
	if flags != fuse.FOPEN_KEEP_CACHE {
		t.Errorf("Expected flags FOPEN_KEEP_CACHE, got %d", flags)
	}
}

func TestDriveItemNode_Open_ReadWrite_WithContentOnDisk(t *testing.T) {
	tmpDir := t.TempDir()
	contentCache, _ := NewContentCache(tmpDir)
	contentCache.Insert("file_rw", []byte("contenido RDWR local"))

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "file_rw", Name: "rw.txt", Size: 18}),
		nodeDeps: nodeDeps{
			contentCache: contentCache,
		},
	}

	fh, _, errno := node.Open(context.Background(), syscall.O_RDWR)
	if errno != 0 {
		t.Fatalf("Open O_RDWR with on-disk content error: %d", errno)
	}
	if fh == nil {
		t.Fatal("FileHandle should not be nil")
	}
}

func TestDriveItemNode_Open_TruncateFlag(t *testing.T) {
	tmpDir := t.TempDir()
	contentCache, _ := NewContentCache(tmpDir)
	contentCache.Insert("file123", []byte("content that will be truncated"))

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "file123", Name: "trunc.txt", Size: 27}),
		nodeDeps: nodeDeps{
			contentCache: contentCache,
		},
	}

	fh, _, errno := node.Open(context.Background(), syscall.O_WRONLY|syscall.O_TRUNC)
	if errno != 0 {
		t.Fatalf("Open O_TRUNC error: %d", errno)
	}
	if fh == nil {
		t.Fatal("FileHandle should not be nil")
	}
	if node.inode.Size() != 0 {
		t.Errorf("Size should be 0 after truncate, Size=%d", node.inode.Size())
	}
	if !node.inode.HasChanges() {
		t.Error("Truncate should mark hasChanges=true")
	}
	// Verify that the file on disk was truncated
	if contentCache.Size("file123") != 0 {
		t.Error("The file on disk should be truncated to 0")
	}
}

func TestDriveItemNode_Open_LocalID_SkipsDownload(t *testing.T) {
	tmpDir := t.TempDir()
	contentCache, _ := NewContentCache(tmpDir)

	localID := newLocalID()
	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: localID, Name: "local.txt"}),
		nodeDeps: nodeDeps{
			contentCache: contentCache,
		},
	}

	// Open read-only without previous content -> should not try to download (it is local)
	fh, _, errno := node.Open(context.Background(), syscall.O_RDONLY)
	if errno != 0 {
		t.Fatalf("Open local ID solo lectura error: %d", errno)
	}
	if fh == nil {
		t.Fatal("FileHandle should not be nil")
	}
}

// ──── Fsync ────

// The scenario behind issue #87: a brand-new file is flushed while still
// empty (FUSE can process the flush before the first write lands), then the
// write arrives and the file is flushed again. The upload must be enqueued by
// the second flush, and the dirty flag must survive the empty flush so the
// write that follows is not lost.
func TestDriveItemNode_Fsync_EmptyFlushThenWrite_EnqueuesUpload(t *testing.T) {
	tmpDir := t.TempDir()
	contentCache, _ := NewContentCache(tmpDir)
	um := NewUploadManager(nil, nil, NewInodeCache(), contentCache, 0, 0)
	um.Start()
	defer um.Stop()

	parent := NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}})
	node := &DriveItemNode{
		inode: NewInodeLocal("test.txt", 0644, parent),
		nodeDeps: nodeDeps{
			contentCache:  contentCache,
			inodeCache:    NewInodeCache(),
			uploadManager: um,
		},
	}
	if _, err := contentCache.Open(node.inode.ID()); err != nil {
		t.Fatal(err)
	}

	// Flush 1: the file is still empty (nothing written yet). The inode was
	// born dirty (new local file), so this flush must not lose the file.
	if errno := node.Fsync(context.Background(), node, 0); errno != 0 {
		t.Fatalf("Fsync (empty) error: %d", errno)
	}

	// The write lands after the empty flush.
	if _, errno := node.Write(context.Background(), node, []byte("contenido real"), 0); errno != 0 {
		t.Fatalf("Write error: %d", errno)
	}

	// Flush 2: content exists now → the session must be enqueued and the
	// dirty flag cleared.
	if errno := node.Fsync(context.Background(), node, 0); errno != 0 {
		t.Fatalf("Fsync (after write) error: %d", errno)
	}
	if node.inode.HasChanges() {
		t.Error("Fsync after a successful enqueue should clear hasChanges")
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		um.mu.Lock()
		_, exists := um.sessions[node.inode.ID()]
		um.mu.Unlock()
		if exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the upload session was never enqueued after the second flush")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDriveItemNode_Fsync_NoChanges(t *testing.T) {
	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "file123", Name: "clean.txt", Size: 100}),
	}

	errno := node.Fsync(context.Background(), node, 0)
	if errno != 0 {
		t.Errorf("Fsync without changes should return 0, got %d", errno)
	}
}

func TestDriveItemNode_Fsync_HasChanges_NoUploadManager(t *testing.T) {
	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "file123", Name: "dirty.txt", Size: 100}),
	}
	node.inode.SetHasChanges(true)

	errno := node.Fsync(context.Background(), node, 0)
	if errno != 0 {
		t.Errorf("Fsync without UploadManager should return 0, got %d", errno)
	}
	if node.inode.HasChanges() {
		t.Error("Fsync should clear hasChanges")
	}
}

func TestDriveItemNode_Fsync_HasChanges_WithUploadManager(t *testing.T) {
	tmpDir := t.TempDir()
	contentCache, _ := NewContentCache(tmpDir)
	contentCache.Insert("file123", []byte("data para subir"))

	um := NewUploadManager(nil, nil, NewInodeCache(), contentCache, 0, 0)
	um.Start()
	defer um.Stop()

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{
			ID: "file123", Name: "dirty.txt", Size: 15,
			Parent: &graph.DriveItemParent{ID: "root"},
		}),
		nodeDeps: nodeDeps{
			contentCache:  contentCache,
			inodeCache:    NewInodeCache(),
			uploadManager: um,
		},
	}
	node.inode.SetHasChanges(true)

	// Fsync must enqueue without blocking (the uploadLoop drains the channel)
	errno := node.Fsync(context.Background(), node, 0)
	if errno != 0 {
		t.Errorf("Fsync with UploadManager should return 0, got %d", errno)
	}
	if node.inode.HasChanges() {
		t.Error("Fsync should clear hasChanges")
	}

	// Wait for uploadLoop to process the queued session
	time.Sleep(100 * time.Millisecond)

	// Verify that the session was queued
	um.mu.Lock()
	sessionCount := len(um.sessions)
	um.mu.Unlock()
	if sessionCount != 1 {
		t.Errorf("Expected 1 queued session, got %d", sessionCount)
	}
}

func TestDriveItemNode_Flush_FsyncAndClose(t *testing.T) {
	tmpDir := t.TempDir()
	contentCache, _ := NewContentCache(tmpDir)
	contentCache.Insert("file123", []byte("hello"))
	contentCache.Open("file123") // abrir FD primero

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "file123", Name: "test.txt", Size: 5}),
		nodeDeps: nodeDeps{
			contentCache: contentCache,
		},
	}
	node.inode.SetHasChanges(true)

	errno := node.Flush(context.Background(), node)
	if errno != 0 {
		t.Errorf("Flush error: %d", errno)
	}

	if node.inode.HasChanges() {
		t.Error("Flush→Fsync should clear hasChanges")
	}
	if contentCache.IsOpen("file123") {
		t.Error("Flush should close the FD")
	}
}

// TestDriveItemNode_Read_OffsetBeyondEOF tests Read with an offset greater than the size
func TestDriveItemNode_Read_OffsetBeyondEOF(t *testing.T) {
	content := []byte("0123456789")

	tmpDir := t.TempDir()
	contentCache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("Error creando ContentCache: %v", err)
	}
	if err := contentCache.Insert("file123", content); err != nil {
		t.Fatalf("Error inserting content: %v", err)
	}

	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "file123", Name: "archivo.txt", Size: 10}),
		nodeDeps: nodeDeps{
			contentCache: contentCache,
		},
	}

	dest := make([]byte, 100)
	result, errno := node.Read(context.Background(), node, dest, 100)

	if errno != 0 {
		t.Fatalf("Expected errno 0, got %d", errno)
	}

	data, _ := result.Bytes(dest)
	if len(data) != 0 {
		t.Errorf("Expected 0 bytes, got %d", len(data))
	}
}

// ──── Statfs ────

func TestDriveItemNode_Statfs(t *testing.T) {
	node := &DriveItemNode{
		inode: NewInodeDriveItem(&graph.DriveItem{ID: "root", Name: "root", Folder: &graph.Folder{}}),
	}

	var out fuse.StatfsOut
	errno := node.Statfs(context.Background(), &out)

	if errno != 0 {
		t.Fatalf("Statfs should return 0, got %d", errno)
	}
	if out.Bsize != 4096 {
		t.Errorf("Expected bsize 4096, got %d", out.Bsize)
	}
	if out.Blocks == 0 {
		t.Error("Blocks should not be 0")
	}
	if out.NameLen != 260 {
		t.Errorf("Expected NameLen 260, got %d", out.NameLen)
	}
}

// itoa convierte int a string sin importar fmt.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
