package fs

import (
	"encoding/base64"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/frosado/onecloudriver/internal/graph/quickxorhash"
	"github.com/rs/zerolog/log"
)

// ContentCache stores file content on disk as normal files,
// allowing zero-copy reads from FUSE.
// LoopbackCache in onedriver is a similar implementation, but it ignores
// errors from os.Mkdir and does not propagate them.
// This version explicitly propagates the error to the caller instead of ignoring it, allowing the caller to handle it appropriately.

type ContentCache struct {
	directory string
	fds       sync.Map // id -> *os.File (FDs abiertos y reutilizables)

	// locks maps each inode ID to a per-inode mutex that serializes
	// content reads (snapshot/verify) against content writes, so a
	// snapshot (ReadAll/SumQuickXorHash) never captures a half-written
	// file (issue #64). Entries are intentionally never pruned: removing
	// a mutex another goroutine may still hold is unsafe.
	locks sync.Map // id -> *sync.Mutex

	// maxSize is the maximum size in bytes of content cached on disk
	// before age-based eviction activates (Phase 4b). 0 = no limit.
	maxSize int64

	// totalSize tracks current disk usage (bytes) to decide when
	// evict. We use atomic for lock-free reads in Insert/WriteAt.
	totalSize atomic.Int64

	// evictMu serializes the creation of new files (Open) with
	// eviction (evictBySize), eliminating the TOCTOU window:
	//   1. Evictor: IsOpen(id) → false
	//   2. Opener:  os.OpenFile() creates the file
	//   3. Evictor: os.Remove() deletes the newly created file
	// With evictMu, steps 2 and 3 cannot interleave.
	evictMu sync.Mutex

	// evicting is a deduplication flag: prevents launching multiple
	// simultaneous eviction goroutines from maybeEvict().
	evicting atomic.Bool

	// closed indicates the cache has been closed (e.g. during shutdown).
	// The cache ignores subsequent writes after closing, but still allows reads of existing content.

	closed atomic.Bool
}

// NewContentCache creates a new ContentCache. Creates the directory if it does not exist.
//
// LoopbackCache ignore the
// error of os.Mkdir with `os.Mkdir(directory, 0700)` without checking the return value.
// Here the error is propagated explicitly.
func NewContentCache(directory string) (*ContentCache, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	return &ContentCache{directory: directory}, nil
}

// lockFor returns the per-inode mutex for id, creating it lazily. It
// serializes snapshot reads (ReadAll/SumQuickXorHash) against content writes
// (WriteAt/Insert/InsertStream) for the same file.
func (c *ContentCache) lockFor(id string) *sync.Mutex {
	m, _ := c.locks.LoadOrStore(id, &sync.Mutex{})
	mu, _ := m.(*sync.Mutex)
	return mu
}

// contentPath returns the on-disk path for a file ID.
//
// 🔒 Defense in depth against path traversal: although the IDs handled by
// ContentCache come from controlled sources (Microsoft Graph IDs or hex
// generated with crypto/rand, none of which can contain ".." or "/"),
// applying filepath.IsLocal as extra validation ensures that even if an
// upstream bug introduces a malicious ID, it cannot escape the cache
// directory.
func (c *ContentCache) contentPath(id string) string {
	safe := id

	// Null bytes: path/filepath doesn't filter them, filesystems reject them
	// with EINVAL. Always remove them, even if the ID is
	// "local" — a null byte is never valid in a file name.
	if strings.ContainsRune(id, '\x00') {
		log.Warn().Str("id", id).Msg("ContentCache: ID contains null byte — sanitizing")
		safe = strings.ReplaceAll(id, "\x00", "")
	}

	// Path components: if the ID contains path separators, ".." sequences,
	// or anything else filepath.IsLocal rejects, apply full hex-encoding.
	// This guarantees the result only contains [0-9a-f], without separators
	// or dangerous sequences.
	if !filepath.IsLocal(safe) {
		log.Warn().Str("id", id).Msg("ContentCache: ID contains non-local path components — sanitizing with hex")
		safe = hex.EncodeToString([]byte(id))
	}

	return filepath.Join(c.directory, safe)
}

// Open opens or creates a file in the cache. If already open, reuses the FD.
// Uses runtime.SetFinalizer(fd, nil) to prevent the GC from closing the FD
// while still in use (same pattern as onedriver).
//
// Phase 4b: briefly acquires evictMu during file creation to prevent
// evictBySize() from deleting the file between os.OpenFile and fds.Store
// (ventana TOCTOU). Si el FD ya existe en fds, no necesita lock.
func (c *ContentCache) Open(id string) (*os.File, error) {
	if fd, ok := c.fds.Load(id); ok {
		f, _ := fd.(*os.File)
		return f, nil
	}

	// Avoid TOCTOU with evictBySize: while we create the file, the
	// eviction cannot delete it because it only releases evictMu after checking
	// IsOpen() → true.
	c.evictMu.Lock()
	fd, err := os.OpenFile(c.contentPath(id), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		c.evictMu.Unlock()
		return nil, err
	}
	runtime.SetFinalizer(fd, nil) // Prevents the GC from closing the FD while still in use
	c.fds.Store(id, fd)
	c.evictMu.Unlock()

	return fd, nil
}

// Insert writes content to a cache file in one shot.
// After writing, checks if maxSize was exceeded and triggers eviction if needed.
func (c *ContentCache) Insert(id string, content []byte) error {
	if c.closed.Load() {
		return nil
	}
	mu := c.lockFor(id)
	mu.Lock()
	defer mu.Unlock()

	prevSize := c.Size(id)
	if err := os.WriteFile(c.contentPath(id), content, 0600); err != nil {
		return err
	}
	newSize := int64(len(content))
	c.totalSize.Add(newSize - prevSize)
	c.maybeEvict()
	return nil
}

// InsertStream copies a stream to the cached file, repositioning to the start
// and truncating the previous content before writing.
//
// 🔧 Correction from the previous version: NO independent Writer()/nopCloser
// method is exposed. InsertStream operates directly on the FD returned by
// Open(), like the original LoopbackCache. An additional wrapper that
// truncates the FD outside this single entry point would open a corruption
// window if another goroutine had the same FD open for reading in parallel
// (the fds sync.Map design assumes the FD is shared and only positioned/
// truncated here, in a controlled way).
//
// After writing, checks if maxSize was exceeded and triggers eviction if needed.
func (c *ContentCache) InsertStream(id string, reader io.Reader) (int64, error) {
	if c.closed.Load() {
		return 0, nil
	}
	mu := c.lockFor(id)
	mu.Lock()
	defer mu.Unlock()

	prevSize := c.Size(id)
	fd, err := c.Open(id)
	if err != nil {
		return 0, err
	}
	if _, err := fd.Seek(0, 0); err != nil {
		return 0, err
	}
	if err := fd.Truncate(0); err != nil {
		return 0, err
	}
	n, err := io.Copy(fd, reader)
	if err != nil {
		return 0, err
	}
	c.totalSize.Add(n - prevSize)
	c.maybeEvict()
	return n, nil
}

// Close closes the open FD for an ID, if it exists. It does not delete the file.
// Only triggers eviction if the FD was actually open (when closing it,
// the file stops being protected by IsOpen and could become evictable).
func (c *ContentCache) Close(id string) {
	fdVal, wasOpen := c.fds.LoadAndDelete(id)
	if !wasOpen {
		return // it was not open, nothing to do
	}
	file, _ := fdVal.(*os.File)
	if err := file.Sync(); err != nil {
		log.Warn().Err(err).Str("id", id).Msg("Error syncing file in ContentCache")
	}
	if err := file.Close(); err != nil {
		log.Warn().Err(err).Str("id", id).Msg("Error closing file in ContentCache")
	}
	// Only trigger eviction if the limit is configured and could be necessary
	if c.maxSize > 0 && c.totalSize.Load() > c.maxSize {
		c.maybeEvict()
	}
}

// Delete closes the FD and removes the content from disk.
func (c *ContentCache) Delete(id string) error {
	if c.closed.Load() {
		return nil
	}
	prevSize := c.Size(id)
	c.Close(id)
	if err := os.Remove(c.contentPath(id)); err != nil {
		if os.IsNotExist(err) {
			// Adjust totalSize even if the file no longer exists
			c.totalSize.Add(-prevSize)
		}
		return err
	}
	c.totalSize.Add(-prevSize)
	return nil
}

// HasContent checks whether the content exists in the cache (either open or on disk).
func (c *ContentCache) HasContent(id string) bool {
	if _, ok := c.fds.Load(id); ok {
		return true
	}
	_, err := os.Stat(c.contentPath(id))
	return err == nil
}

// WriteAt writes data at a specific position of the cached file.
// Useful for incremental writes from FUSE Write (WriteAt allows arbitrary offsets).
// After writing, adjusts totalSize and triggers eviction if the file grew.
func (c *ContentCache) WriteAt(id string, data []byte, off int64) (int, error) {
	if c.closed.Load() {
		return 0, nil
	}
	mu := c.lockFor(id)
	mu.Lock()
	defer mu.Unlock()

	prevSize := c.Size(id)
	fd, err := c.Open(id)
	if err != nil {
		return 0, err
	}
	n, err := fd.WriteAt(data, off)
	if err != nil {
		return n, err
	}
	newEnd := off + int64(n)
	if newEnd > prevSize {
		c.totalSize.Add(newEnd - prevSize)
		c.maybeEvict()
	}
	return n, nil
}

// Size returns the current size of the file cached on disk.
// If the file does not exist, it returns 0.
func (c *ContentCache) Size(id string) int64 {
	if fd, ok := c.fds.Load(id); ok {
		f, _ := fd.(*os.File)
		st, err := f.Stat()
		if err == nil {
			return st.Size()
		}
	}
	st, err := os.Stat(c.contentPath(id))
	if err != nil {
		return 0
	}
	return st.Size()
}

// ReadAll reads all the cached file content and returns it as
// []byte. It is used to take content snapshots before enqueuing an
// upload (UploadManager.QueueUpload). If the file does not exist, it returns nil.
func (c *ContentCache) ReadAll(id string) []byte {
	mu := c.lockFor(id)
	mu.Lock()
	defer mu.Unlock()

	fd, err := c.Open(id)
	if err != nil {
		return nil
	}
	if _, err := fd.Seek(0, 0); err != nil {
		return nil
	}
	data, err := io.ReadAll(fd)
	if err != nil {
		return nil
	}
	return data
}

// snapshotsDir returns the path of the subdirectory where upload snapshots
// live. Snapshots are stored in a subdirectory (rather than the cache root)
// so that evictBySize / TotalDiskUsage — which only scan top-level files —
// never remove a snapshot that an in-flight upload still needs.
//
// 🔒 It always derives from the user's cache directory (c.directory, e.g.
// ~/.cache/onecloudriver/<account>/content), never from a shared location
// like /tmp: a snapshot may hold file content and must not be readable by
// other local users.
func (c *ContentCache) snapshotsDir() string {
	return filepath.Join(c.directory, "snapshots")
}

// Snapshot creates an independent on-disk copy of the cached content for id
// and returns the snapshot's path and size. The copy is taken under the
// per-inode lock, so it is atomic with respect to concurrent writes
// (issue #64), and it is independent of the live cache file, so later edits
// or eviction do not affect it. This lets the upload path stream from disk
// without materializing the whole file in heap (issue #69).
//
// The caller owns the returned file and must remove it when it is no longer
// needed. A missing file returns ("", 0, os.ErrNotExist); an empty file
// returns ("", 0, nil) after removing its (empty) snapshot.
//
// 🔒 Security: the snapshot is created inside the user's cache directory
// (see snapshotsDir), not in a shared location like /tmp, and with owner-only
// permissions (0700 directory, 0600 file) so its content is not exposed to
// other local users.
func (c *ContentCache) Snapshot(id string) (string, int64, error) {
	if !c.HasContent(id) {
		return "", 0, os.ErrNotExist
	}

	mu := c.lockFor(id)
	mu.Lock()
	defer mu.Unlock()

	fd, err := c.Open(id)
	if err != nil {
		return "", 0, err
	}
	if _, err := fd.Seek(0, 0); err != nil {
		return "", 0, err
	}

	dir := c.snapshotsDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", 0, err
	}
	// os.CreateTemp(dir, ...) creates the file in dir (NOT in os.TempDir())
	// with 0600 permissions.
	tmp, err := os.CreateTemp(dir, "snapshot-*")
	if err != nil {
		return "", 0, err
	}
	path := tmp.Name()

	n, copyErr := io.Copy(tmp, fd)
	closeErr := tmp.Close()
	if copyErr != nil {
		os.Remove(path)
		return "", 0, copyErr
	}
	if closeErr != nil {
		os.Remove(path)
		return "", 0, closeErr
	}
	if n == 0 {
		os.Remove(path)
		return "", 0, nil
	}

	return path, n, nil
}

// IsOpen returns true if the file is already open somewhere.
func (c *ContentCache) IsOpen(id string) bool {
	_, ok := c.fds.Load(id)
	return ok
}

// SumQuickXorHash computes the base64 quickXorHash of the cached content.
// It returns ("", false) when the content does not exist or cannot be read.
// Used to verify cached/downloaded content against the server metadata
// (issue #32, content-integrity verification).
func (c *ContentCache) SumQuickXorHash(id string) (string, bool) {
	if !c.HasContent(id) {
		return "", false
	}
	mu := c.lockFor(id)
	mu.Lock()
	defer mu.Unlock()

	fd, err := c.Open(id)
	if err != nil {
		return "", false
	}
	if _, err := fd.Seek(0, 0); err != nil {
		return "", false
	}
	h := quickxorhash.New()
	if _, err := io.Copy(h, fd); err != nil {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil)), true
}

// ──── Phase 4b: Eviction configuration (setter/getter) ────

// SetMaxSize sets the maximum size in bytes of the ContentCache on disk.
// When totalSize exceeds maxSize, automatic age-based eviction activates.
// 0 = no limit.
func (c *ContentCache) SetMaxSize(maxBytes int64) {
	c.maxSize = maxBytes
	if maxBytes > 0 {
		log.Debug().Int64("maxSize", maxBytes).Msg("ContentCache: maxSize configured, age-based eviction activated")
	}
}

// MaxSize returns the configured maximum size.
func (c *ContentCache) MaxSize() int64 {
	return c.maxSize
}

// TotalSize returns the total tracked size of the cache on disk.
func (c *ContentCache) TotalSize() int64 {
	return c.totalSize.Load()
}

// TotalDiskUsage walks the cache directory and sums the real sizes
// of all files on disk. Useful for reconciling totalSize with filesystem
// reality (e.g. at startup, or for debugging).
func (c *ContentCache) TotalDiskUsage() int64 {
	var total int64
	entries, err := os.ReadDir(c.directory)
	if err != nil {
		return c.totalSize.Load() // fallback to the in-memory counter
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		total += info.Size()
	}
	return total
}

// evictBySize frees files from disk, starting with the least recently
// accessed (oldest modTime), until the total size no longer exceeds maxSize.
//
// Respects IsOpen(): files with an open FD are never evicted.
// Uses evictMu to serialize eviction with new file creation in Open(),
// eliminating the TOCTOU window documented in the struct.
//
// 🔧 Unlike the previous version, uses totalSize.Add(-file.size) per
// deleted file instead of totalSize.Store(target) at the end. This avoids
// losing totalSize updates from concurrent writes.
func (c *ContentCache) evictBySize() {
	c.evictMu.Lock()
	defer c.evictMu.Unlock()

	if c.maxSize <= 0 {
		return
	}

	// Recalculate the real usage from disk to have an accurate reference.
	currentUsage := c.TotalDiskUsage()
	if currentUsage <= c.maxSize {
		return
	}

	type fileInfo struct {
		path    string
		modTime time.Time
		size    int64
	}

	entries, err := os.ReadDir(c.directory)
	if err != nil {
		log.Warn().Err(err).Msg("ContentCache: could not read directory for eviction")
		return
	}

	files := make([]fileInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		// IMPORTANT: contentPath() may hex-encode IDs with characters
		// unsafe for filesystems. In practice, Graph API IDs and those
		// generated with crypto/rand only contain [0-9a-fA-F], so they
		// never trigger sanitization. If in the future IDs with path
		// separators were introduced, the file name would need to be mapped
		// to the original ID in fds. For now, we assume filename == ID.
		id := entry.Name()
		if c.IsOpen(id) {
			continue // do not evict open files
		}
		files = append(files, fileInfo{
			path:    filepath.Join(c.directory, entry.Name()),
			modTime: info.ModTime(),
			size:    info.Size(),
		})
	}

	if len(files) == 0 {
		return
	}

	// Sort by ascending modTime: oldest first (least used).
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})

	remaining := currentUsage
	for _, file := range files {
		if remaining <= c.maxSize {
			break
		}
		// Double check: was it opened between the scan and the deletion?
		id := filepath.Base(file.path)
		if c.IsOpen(id) {
			continue
		}
		if err := os.Remove(file.path); err != nil {
			if !os.IsNotExist(err) {
				log.Warn().Err(err).Str("path", file.path).Msg("ContentCache: error deleting for eviction")
			}
			continue
		}
		// Update totalSize with Add instead of Store: preserves
		// concurrent write updates that may have occurred between
		// TotalDiskUsage() and now.
		c.totalSize.Add(-file.size)
		remaining -= file.size
		log.Debug().
			Str("path", file.path).
			Int64("size", file.size).
			Time("modTime", file.modTime).
			Msg("ContentCache: file evicted due to size limit")
	}

	log.Debug().
		Int64("before", currentUsage).
		Int64("maxSize", c.maxSize).
		Msg("ContentCache: size eviction completed")
}

// maybeEvict checks if the total size exceeds maxSize and, if so,
// triggers eviction. Called after each write operation (Insert,
// InsertStream, WriteAt) and when closing files (Close).
//
// Uses evicting as a deduplication flag: if an eviction goroutine is
// already in flight, it doesn't launch another. evictMu serializes
// concurrent executions with Open().
//
// Eviction runs in background to not block the FUSE operation
// that triggered it.
func (c *ContentCache) maybeEvict() {
	if c.maxSize <= 0 {
		return
	}
	if c.evicting.Swap(true) {
		return // an eviction goroutine is already in flight
	}
	go func() {
		defer c.evicting.Store(false)
		c.evictBySize()
	}()
}

// ForceEvict runs synchronous eviction (useful for tests and UI).
func (c *ContentCache) ForceEvict() {
	c.evictBySize()
}

// CloseAll closes all open FDs and clears the fds map.
// It is called during shutdown to free resources.
func (c *ContentCache) CloseAll() {
	c.closed.Store(true)
	c.fds.Range(func(key, value interface{}) bool {
		if fd, ok := value.(*os.File); ok {
			fd.Sync()  //nolint:errcheck // best-effort on shutdown
			fd.Close() //nolint:errcheck // best-effort on shutdown
		}
		c.fds.Delete(key)
		return true
	})
	log.Debug().Msg("ContentCache: all  open FDs closed")
}
