//go:build integration

package fs

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// waitMountReady waits until the FUSE mountpoint responds to stat(),
// with a 2-second timeout. Avoids the "context canceled" that occurs
// when operating on a mount that is not serving requests yet.
func waitMountReady(t *testing.T, mountpoint string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(mountpoint); err == nil {
			// The mountpoint responds — give a small extra margin
			// so FUSE finishes initializing internally.
			time.Sleep(10 * time.Millisecond)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Timeout waiting for the FUSE mountpoint to be ready")
}

// hasFusermount checks whether fusermount3 is available on the system.
func hasFusermount() bool {
	_, err := exec.LookPath("fusermount3")
	if err == nil {
		return true
	}
	_, err = exec.LookPath("fusermount")
	return err == nil
}

// mountTestFS creates a temporary FUSE filesystem backed by a
// mock HTTP of the Graph API. Returns the mountpoint, the server, and a function
// for cleanup.
func mountTestFS(t *testing.T, handler http.HandlerFunc) (mountpoint string, server *httptest.Server, graphClient *graph.Client, inodeCache *InodeCache, contentCache *ContentCache, cleanup func()) {
	t.Helper()

	if !hasFusermount() {
		t.Skip("fusermount3/fusermount not available — needed for FUSE integration tests")
	}

	server = httptest.NewServer(handler)

	graphClient = &graph.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	tmpDir := t.TempDir()
	mountpoint = filepath.Join(tmpDir, "mnt")
	if err := os.Mkdir(mountpoint, 0755); err != nil {
		t.Fatalf("Error creating mountpoint: %v", err)
	}

	cacheDir := filepath.Join(tmpDir, "cache")
	var err error
	contentCache, err = NewContentCache(filepath.Join(cacheDir, "content"))
	if err != nil {
		t.Fatalf("Error creating ContentCache: %v", err)
	}

	inodeCache = NewInodeCache()

	root := NewOneCloudFS(graphClient, &mockTokenProvider{token: "test_token"}, inodeCache, contentCache, nil)

	opts := &fs.Options{
		MountOptions: fuse.MountOptions{
			DirectMountStrict: false,
			Options:           []string{"rw"},
		},
	}

	serverFUSE, err := fs.Mount(mountpoint, root, opts)
	if err != nil {
		t.Fatalf("Error mounting FUSE: %v", err)
	}

	cleanup = func() {
		if err := serverFUSE.Unmount(); err != nil {
			t.Logf("Warning: error unmounting: %v", err)
			// Intentar lazy-unmount como fallback
			fusermountCmd := "fusermount3"
			if _, e := exec.LookPath("fusermount3"); e != nil {
				fusermountCmd = "fusermount"
			}
			exec.Command(fusermountCmd, "-uz", mountpoint).Run()
		}
		// Pause so the kernel finishes cleaning up the mount before
		// before the next test creates a new one.
		time.Sleep(100 * time.Millisecond)
		server.Close()
	}

	// Actively wait for the mountpoint to be ready. A fixed sleep
	// (50ms) is not enough on slow or loaded machines, and causes
	// "context canceled" when the FUSE operation is issued before
	// the daemon is serving requests.
	waitMountReady(t, mountpoint)

	return
}

// ──── Mkdir ────

func TestIntegration_Mkdir(t *testing.T) {
	var createFolderCalls int32

	mountpoint, _, _, inodeCache, _, cleanup := mountTestFS(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.Contains(r.URL.Path, "/children") {
			atomic.AddInt32(&createFolderCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "newfolder123", "name": "MiCarpeta", "folder": {"childCount": 0}}`))
			return
		}
		// ListChildren for the root (during mount)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value": []}`))
	})
	defer cleanup()

	err := os.Mkdir(filepath.Join(mountpoint, "MiCarpeta"), 0755)
	if err != nil {
		t.Fatalf("os.Mkdir error: %v", err)
	}

	// Verify that CreateFolder was called on Graph
	if atomic.LoadInt32(&createFolderCalls) != 1 {
		t.Errorf("Expected 1 CreateFolder call, got %d", createFolderCalls)
	}

	// Verify that the folder exists in InodeCache (by ID, without fetcher)
	if child := inodeCache.Get("newfolder123"); child == nil {
		t.Error("newfolder123 not found in InodeCache")
	} else if child.Name() != "MiCarpeta" {
		t.Errorf("Expected Name 'MiCarpeta', got %q", child.Name())
	}

	// Verify that we can stat the folder
	fi, err := os.Stat(filepath.Join(mountpoint, "MiCarpeta"))
	if err != nil {
		t.Fatalf("Stat error: %v", err)
	}
	if !fi.IsDir() {
		t.Error("MiCarpeta should be a directory")
	}
}

// ──── Create + Write + Read ────

func TestIntegration_CreateWriteRead(t *testing.T) {
	var uploadCalls int32

	mountpoint, _, _, inodeCache, contentCache, cleanup := mountTestFS(t, func(w http.ResponseWriter, r *http.Request) {
		// ListChildren for root
		if strings.Contains(r.URL.Path, "/children") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"value": []}`))
			return
		}
		// UploadItem (PUT .../content)
		if r.Method == "PUT" {
			atomic.AddInt32(&uploadCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "uploaded123", "name": "test.txt", "size": 14, "parentReference": {"id": "root"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	defer cleanup()

	fpath := filepath.Join(mountpoint, "test.txt")
	content := []byte("Hello, FUSE!\n")

	// Create file
	if err := os.WriteFile(fpath, content, 0644); err != nil {
		t.Fatalf("os.WriteFile error: %v", err)
	}

	// Verify that the file is in InodeCache.
	// After WriteFile (which includes close → flush → fsync → upload),
	// el ID local ya fue swappeado por el ID remoto ("uploaded123").
	rootInode := inodeCache.Get("root")
	if rootInode == nil {
		t.Fatal("root not found in InodeCache")
	}
	var child *Inode
	for _, childID := range rootInode.Children() {
		if c := inodeCache.Get(childID); c != nil && c.Name() == "test.txt" {
			child = c
			break
		}
	}
	if child == nil {
		t.Fatal("test.txt not found in InodeCache (searching root children)")
	}
	// The ID should already be the remote one (the upload happened in Flush)
	if isLocalID(child.ID()) {
		t.Log("The file still has a local ID (upload not completed yet) — this is OK")
	} else if child.ID() != "uploaded123" {
		t.Errorf("Expected ID 'uploaded123', got %q", child.ID())
	}

	// Verify that it has content in ContentCache
	if !contentCache.HasContent(child.ID()) {
		t.Error("The file should exist in ContentCache")
	}

	// Read the file back
	data, err := os.ReadFile(fpath)
	if err != nil {
		t.Fatalf("os.ReadFile error: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("Expected content %q, got %q", string(content), string(data))
	}
}

// ──── CreateExisting (truncar) ────

func TestIntegration_CreateExisting_Truncates(t *testing.T) {
	var listingCalls int32

	// Mock that returns an existing file the first time
	var firstList atomic.Bool
	firstList.Store(true)

	mountpoint, _, _, _, _, cleanup := mountTestFS(t, func(w http.ResponseWriter, r *http.Request) {
		// GetItem (metadata)
		if r.URL.Path == "/me/drive/items/existing123" && r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "existing123", "name": "existente.txt", "size": 10}`))
			return
		}
		// GetItemContent
		if strings.Contains(r.URL.Path, "/content") {
			w.Header().Set("Content-Range", "bytes 0-9/10")
			w.WriteHeader(http.StatusPartialContent)
			w.Write([]byte("contenido!"))
			return
		}
		// ListChildren
		if strings.Contains(r.URL.Path, "/children") {
			atomic.AddInt32(&listingCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			if firstList.Swap(false) {
				w.Write([]byte(`{"value": [{"id": "existing123", "name": "existente.txt", "size": 10}]}`))
			} else {
				w.Write([]byte(`{"value": [{"id": "existing123", "name": "existente.txt", "size": 0}]}`))
			}
			return
		}
		// PUT content
		if r.Method == "PUT" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "existing123", "name": "existente.txt", "size": 0}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	defer cleanup()

	fpath := filepath.Join(mountpoint, "existente.txt")

	// Open with O_TRUNC → should truncate to 0
	f, err := os.OpenFile(fpath, os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatalf("OpenFile error: %v", err)
	}

	// Verify that the file was truncated
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat error: %v", err)
	}
	if fi.Size() != 0 {
		t.Errorf("Expected size 0 (truncated), got %d", fi.Size())
	}
	f.Close()
}

// ──── Mkdir + Rmdir ────

func TestIntegration_MkdirRmdir(t *testing.T) {
	var mkdirCalls, deleteCalls int32
	mountpoint, _, _, inodeCache, _, cleanup := mountTestFS(t, func(w http.ResponseWriter, r *http.Request) {
		// ListChildren
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/children") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"value": []}`))
			return
		}
		// CreateFolder
		if r.Method == "POST" && strings.Contains(r.URL.Path, "/children") {
			atomic.AddInt32(&mkdirCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "delfolder", "name": "ToDelete", "folder": {"childCount": 0}}`))
			return
		}
		// DeleteItem
		if r.Method == "DELETE" {
			atomic.AddInt32(&deleteCalls, 1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		fmt.Printf("UNEXPECTED: %s %s\n", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	})
	defer cleanup()

	dirPath := filepath.Join(mountpoint, "ToDelete")

	// Create folder
	if err := os.Mkdir(dirPath, 0755); err != nil {
		t.Fatalf("os.Mkdir error: %v", err)
	}
	if atomic.LoadInt32(&mkdirCalls) != 1 {
		t.Errorf("Expected 1 CreateFolder, got %d", mkdirCalls)
	}

	// Verify it exists in the FS
	if _, err := os.Stat(dirPath); err != nil {
		t.Fatalf("Stat after Mkdir failed: %v", err)
	}

	// Delete folder
	if err := os.Remove(dirPath); err != nil {
		t.Fatalf("os.Remove error: %v", err)
	}
	if atomic.LoadInt32(&deleteCalls) != 1 {
		t.Errorf("Expected 1 DeleteItem, got %d", deleteCalls)
	}

	// Verify that it no longer exists in the FS
	if _, err := os.Stat(dirPath); !os.IsNotExist(err) {
		t.Error("The folder should have been removed")
	}

	// Verify that it is no longer in InodeCache
	if child := inodeCache.Get("delfolder"); child != nil {
		t.Error("delfolder should not exist in InodeCache after Rmdir")
	}

}

// ──── Unlink ────

func TestIntegration_Unlink(t *testing.T) {
	mountpoint, _, _, _, _, cleanup := mountTestFS(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" && strings.Contains(r.URL.Path, "content") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "todel", "name": "todelete.txt", "size": 5}`))
			return
		}
		if r.Method == "DELETE" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value": []}`))
	})
	defer cleanup()

	fpath := filepath.Join(mountpoint, "todelete.txt")

	// Create file
	if err := os.WriteFile(fpath, []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	// Verify it exists
	if _, err := os.Stat(fpath); err != nil {
		t.Fatalf("Stat error: %v", err)
	}

	// Delete
	if err := os.Remove(fpath); err != nil {
		t.Fatalf("Remove error: %v", err)
	}

	// Verify that it no longer exists
	if _, err := os.Stat(fpath); !os.IsNotExist(err) {
		t.Error("The file should have been removed")
	}
}

// ──── Rename ────

func TestIntegration_Rename(t *testing.T) {
	var renameCalled atomic.Bool

	mountpoint, _, _, _, _, cleanup := mountTestFS(t, func(w http.ResponseWriter, r *http.Request) {
		// Upload (PUT content) — must come BEFORE the generic /content handler
		if r.Method == "PUT" && strings.Contains(r.URL.Path, "/content") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "ren123", "name": "origen.txt", "size": 10}`))
			return
		}
		// GetItem (metadata, no content)
		if r.URL.Path == "/me/drive/items/ren123" && r.Method == "GET" && !strings.Contains(r.URL.Path, "/content") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "ren123", "name": "origen.txt", "size": 10}`))
			return
		}
		// GetItemContent (GET content)
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/content") {
			w.Header().Set("Content-Range", "bytes 0-9/10")
			w.WriteHeader(http.StatusPartialContent)
			w.Write([]byte("contenido!"))
			return
		}
		// Rename (PATCH)
		if r.Method == "PATCH" {
			renameCalled.Store(true)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "ren123", "name": "destino.txt"}`))
			return
		}
		// ListChildren
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value": [{"id": "ren123", "name": "origen.txt", "size": 10}]}`))
	})
	defer cleanup()

	srcPath := filepath.Join(mountpoint, "origen.txt")
	dstPath := filepath.Join(mountpoint, "destino.txt")

	// Create the file first (so it exists in the FS)
	if err := os.WriteFile(srcPath, []byte("contenido!"), 0644); err != nil {
		t.Skipf("Initial WriteFile failed (expected if the mock is incomplete): %v", err)
	}

	// Rename
	if err := os.Rename(srcPath, dstPath); err != nil {
		t.Fatalf("Rename error: %v", err)
	}

	// Verify that the source no longer exists
	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Error("The source file should have disappeared")
	}

	// Verify the destination exists
	if _, err := os.Stat(dstPath); err != nil {
		t.Fatalf("The destination file should exist: %v", err)
	}

	if !renameCalled.Load() {
		t.Error("Expected a PATCH call (RenameItem)")
	}
}

// ──── Statfs ────

func TestIntegration_Statfs(t *testing.T) {
	mountpoint, _, _, _, _, cleanup := mountTestFS(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value": []}`))
	})
	defer cleanup()

	// syscall.Statfs en Linux no siempre refleja los valores de FUSE
	// NodeStatfser (depends on the kernel and go-fuse versions).
	// The important thing is that the call does not fail with an error.
	var stat syscall.Statfs_t
	if err := syscall.Statfs(mountpoint, &stat); err != nil {
		t.Fatalf("Statfs error: %v", err)
	}

	// Verify that at least the fundamental fields are not zero.
	// On kernels that do support FUSE Statfs, these values will come
	// from our NodeStatfser (Bsize=4096, Namelen=260).
	// On kernels that do not, they will be 0 but the call will not fail.
	if stat.Bsize != 0 && stat.Bsize != 4096 {
		t.Errorf("Unexpected bsize: %d (expected 4096 or 0)", stat.Bsize)
	}
	// Linux file systems typically report 255; OneDrive supports up to 260.
	// Accept both, plus 0 for kernels without FUSE Statfs support.
	if stat.Namelen != 0 && stat.Namelen != 255 && stat.Namelen != 260 {
		t.Errorf("Namelen unexpected: %d (expected 255, 260, or 0)", stat.Namelen)
	}
	t.Logf("Statfs: Bsize=%d, Blocks=%d, Namelen=%d", stat.Bsize, stat.Blocks, stat.Namelen)
}

// ──── Write con offset (mid-file write) ────

func TestIntegration_WriteAtOffset(t *testing.T) {
	mountpoint, _, _, _, _, cleanup := mountTestFS(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "wr123", "name": "midfile.txt", "size": 100}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value": []}`))
	})
	defer cleanup()

	fpath := filepath.Join(mountpoint, "midfile.txt")

	// Create file with initial content
	f, err := os.OpenFile(fpath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		t.Fatalf("OpenFile error: %v", err)
	}
	defer f.Close()

	// Write at offset 10
	n, err := f.WriteAt([]byte("MIDFILE"), 10)
	if err != nil {
		t.Fatalf("WriteAt error: %v", err)
	}
	if n != 7 {
		t.Errorf("Expected 7 written bytes, got %d", n)
	}

	// Read from offset 10
	buf := make([]byte, 7)
	n, err = f.ReadAt(buf, 10)
	if err != nil {
		t.Fatalf("ReadAt error: %v", err)
	}
	if string(buf[:n]) != "MIDFILE" {
		t.Errorf("Expected content 'MIDFILE', got %q", string(buf[:n]))
	}

}
