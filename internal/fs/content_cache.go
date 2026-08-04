package fs

import (
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

	"github.com/rs/zerolog/log"
)

// ContentCache almacena el contenido de archivos en disco como archivos
// normales, permitiendo zero-copy reads desde FUSE.
//
// Fiel al LoopbackCache de onedriver, con una diferencia deliberada: el
// constructor propagates the directory creation error instead of ignoring it.
type ContentCache struct {
	directory string
	fds       sync.Map // id -> *os.File (FDs abiertos y reutilizables)

	// maxSize is the maximum size in bytes of content cached on disk
	// before age-based eviction activates (Phase 4b). 0 = no limit.
	maxSize int64

	// totalSize tracks current disk usage (bytes) to decide when
	// evictar. Usamos atomic para lecturas sin lock en Insert/WriteAt.
	totalSize atomic.Int64

	// evictMu serializes the creation of new files (Open) with
	// eviction (evictBySize), eliminating the TOCTOU window:
	//   1. Evictor: IsOpen(id) → false
	//   2. Opener:  os.OpenFile() crea el archivo
	//   3. Evictor: os.Remove() deletes the newly created file
	// Con evictMu, los pasos 2 y 3 no pueden intercalarse.
	evictMu sync.Mutex

	// evicting is a deduplication flag: prevents launching multiple
	// simultaneous eviction goroutines from maybeEvict().
	evicting atomic.Bool

	// closed indicates the cache has been closed (e.g. during shutdown).
	// Las operaciones de escritura posteriores al cierre se ignoran.
	closed atomic.Bool
}

// NewContentCache crea un nuevo ContentCache. Crea el directorio si no existe.
//
// 🔧 Mejora deliberada sobre el original de onedriver: LoopbackCache ignora el
// error de os.Mkdir con `os.Mkdir(directory, 0700)` sin comprobar el retorno.
// Here the error is propagated explicitly.
func NewContentCache(directory string) (*ContentCache, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	return &ContentCache{directory: directory}, nil
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
		return fd.(*os.File), nil
	}

	// Evitar TOCTOU con evictBySize: mientras creamos el archivo, la
	// eviction cannot delete it because it only releases evictMu after checking
	// IsOpen() → true.
	c.evictMu.Lock()
	fd, err := os.OpenFile(c.contentPath(id), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		c.evictMu.Unlock()
		return nil, err
	}
	runtime.SetFinalizer(fd, nil) // Evita que el GC cierre el FD mientras sigue en uso
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

// Close cierra el FD abierto para un ID, si existe. No elimina el archivo.
// Only triggers eviction if the FD was actually open (when closing it,
// the file stops being protected by IsOpen and could become evictable).
func (c *ContentCache) Close(id string) {
	fdVal, wasOpen := c.fds.LoadAndDelete(id)
	if !wasOpen {
		return // no estaba abierto, nada que hacer
	}
	file := fdVal.(*os.File)
	if err := file.Sync(); err != nil {
		log.Warn().Err(err).Str("id", id).Msg("Error al sincronizar archivo en ContentCache")
	}
	if err := file.Close(); err != nil {
		log.Warn().Err(err).Str("id", id).Msg("Error al cerrar archivo en ContentCache")
	}
	// Only trigger eviction if the limit is configured and could be necessary
	if c.maxSize > 0 && c.totalSize.Load() > c.maxSize {
		c.maybeEvict()
	}
}

// Delete cierra el FD y elimina el contenido del disco.
func (c *ContentCache) Delete(id string) error {
	if c.closed.Load() {
		return nil
	}
	prevSize := c.Size(id)
	c.Close(id)
	if err := os.Remove(c.contentPath(id)); err != nil {
		if os.IsNotExist(err) {
			// Ajustar totalSize aunque el archivo ya no exista
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
// Si el archivo no existe, devuelve 0.
func (c *ContentCache) Size(id string) int64 {
	if fd, ok := c.fds.Load(id); ok {
		st, err := fd.(*os.File).Stat()
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

// ReadAll lee todo el contenido del archivo cacheado y lo devuelve como
// []byte. Se usa para tomar snapshots del contenido antes de encolar una
// subida (UploadManager.QueueUpload). Si el archivo no existe, devuelve nil.
func (c *ContentCache) ReadAll(id string) []byte {
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

// IsOpen returns true if the file is already open somewhere.
func (c *ContentCache) IsOpen(id string) bool {
	_, ok := c.fds.Load(id)
	return ok
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

	// Recalcular el uso real desde disco para tener una referencia precisa.
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

	var files []fileInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		// IMPORTANTE: contentPath() puede hex-encodear IDs con caracteres
		// unsafe for filesystems. In practice, Graph API IDs and those
		// generated with crypto/rand only contain [0-9a-fA-F], so they
		// never trigger sanitization. If in the future IDs with path
		// separators were introduced, the file name would need to be mapped
		// al ID original en fds. Por ahora, asumimos filename == ID.
		id := entry.Name()
		if c.IsOpen(id) {
			continue // no evictar archivos abiertos
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
		// Actualizar totalSize con Add en vez de Store: preserva las
		// actualizaciones de escrituras concurrentes que hayan ocurrido
		// entre TotalDiskUsage() y ahora.
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

// CloseAll cierra todos los FDs abiertos y limpia el mapa fds.
// Se llama durante el shutdown para liberar recursos.
func (c *ContentCache) CloseAll() {
	c.closed.Store(true)
	c.fds.Range(func(key, value interface{}) bool {
		if fd, ok := value.(*os.File); ok {
			fd.Sync()  //nolint:errcheck // best-effort en shutdown
			fd.Close() //nolint:errcheck // best-effort en shutdown
		}
		c.fds.Delete(key)
		return true
	})
	log.Debug().Msg("ContentCache: todos los FDs cerrados")
}
