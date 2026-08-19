package fs

import (
	"container/heap"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog/log"
)

// evictEntry is the current eviction metadata for one content file.
//
// Entries are pointers so updates can use heap.Fix instead of appending stale
// records. That keeps the heap bounded to one entry per cached file.
type evictEntry struct {
	id      string
	path    string
	modTime time.Time
	size    int64
	index   int
}

// evictHeap is a min-heap ordered by modification time, with the path as a
// deterministic tie-breaker.
type evictHeap []*evictEntry

func (h evictHeap) Len() int { return len(h) }

func (h evictHeap) Less(i, j int) bool {
	if h[i].modTime.Equal(h[j].modTime) {
		return h[i].path < h[j].path
	}
	return h[i].modTime.Before(h[j].modTime)
}

func (h evictHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *evictHeap) Push(value any) {
	entry, ok := value.(*evictEntry)
	if !ok {
		return
	}
	entry.index = len(*h)
	*h = append(*h, entry)
}

func (h *evictHeap) Pop() any {
	old := *h
	last := len(old) - 1
	entry := old[last]
	old[last] = nil
	entry.index = -1
	*h = old[:last]
	return entry
}

// initializeEvictionIndex hydrates the heap and disk accounting from the
// persistent cache directory. It runs once when the cache is created so files
// left by a previous process remain eligible for eviction.
func (c *ContentCache) initializeEvictionIndex() error {
	c.evictMu.Lock()
	defer c.evictMu.Unlock()

	entries, err := os.ReadDir(c.directory)
	if err != nil {
		return err
	}

	c.evictEntries = make(map[string]*evictEntry, len(entries))
	c.evictionHeap = nil
	var total int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			log.Warn().Err(err).Str("path", filepath.Join(c.directory, entry.Name())).
				Msg("ContentCache: could not index file for eviction")
			continue
		}

		path := filepath.Join(c.directory, entry.Name())
		evictionEntry := &evictEntry{
			id:      entry.Name(),
			path:    path,
			modTime: info.ModTime(),
			size:    info.Size(),
			index:   -1,
		}
		c.evictEntries[path] = evictionEntry
		heap.Push(&c.evictionHeap, evictionEntry)
		total += info.Size()
	}
	c.totalSize.Store(total)
	return nil
}

// trackFileInfoLocked records the current metadata for path. The caller must
// hold evictMu. Updating an existing pointer and fixing its heap position
// avoids stale entries after overwrites.
func (c *ContentCache) trackFileInfoLocked(id, path string, info os.FileInfo) {
	if c.evictEntries == nil {
		c.evictEntries = make(map[string]*evictEntry)
	}

	entry, exists := c.evictEntries[path]
	if !exists {
		entry = &evictEntry{
			id:      id,
			path:    path,
			modTime: info.ModTime(),
			size:    info.Size(),
			index:   -1,
		}
		c.evictEntries[path] = entry
		heap.Push(&c.evictionHeap, entry)
		c.totalSize.Add(entry.size)
		return
	}

	c.totalSize.Add(info.Size() - entry.size)
	entry.id = id
	entry.modTime = info.ModTime()
	entry.size = info.Size()
	if entry.index >= 0 {
		heap.Fix(&c.evictionHeap, entry.index)
	} else {
		heap.Push(&c.evictionHeap, entry)
	}
}

// trackFileLocked records the current on-disk metadata for id. The caller must
// hold evictMu.
func (c *ContentCache) trackFileLocked(id string) error {
	path := c.contentPath(id)
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	c.trackFileInfoLocked(id, path, info)
	return nil
}

// trackOpenFileLocked indexes a newly created file using its already-open file
// descriptor. The caller must hold evictMu.
func (c *ContentCache) trackOpenFileLocked(id, path string, file *os.File) error {
	if c.evictEntries != nil {
		if _, exists := c.evictEntries[path]; exists {
			return nil
		}
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	c.trackFileInfoLocked(id, path, info)
	return nil
}

// forgetEvictionEntryLocked removes path from the current index and heap.
// The caller must hold evictMu.
func (c *ContentCache) forgetEvictionEntryLocked(path string) {
	entry, exists := c.evictEntries[path]
	if !exists {
		return
	}
	if entry.index >= 0 {
		heap.Remove(&c.evictionHeap, entry.index)
	}
	delete(c.evictEntries, path)
}

// evictEntryIsOpen checks both the logical ID and the actual cache path. The
// path fallback is required for files whose logical ID was sanitized before it
// reached the filesystem.
func (c *ContentCache) evictEntryIsOpen(entry *evictEntry) bool {
	if entry.id != "" && c.IsOpen(entry.id) {
		return true
	}

	open := false
	c.fds.Range(func(key, _ any) bool {
		id, ok := key.(string)
		if !ok {
			return true
		}
		if c.contentPath(id) == entry.path {
			open = true
			return false
		}
		return true
	})
	return open
}

// evictBySize removes the oldest closed files until the tracked usage is under
// maxSize. The heap avoids the directory scan, metadata syscalls, and sort that
// the previous implementation performed on every eviction pass.
func (c *ContentCache) evictBySize() {
	c.evictMu.Lock()
	defer c.evictMu.Unlock()

	if c.maxSize <= 0 || c.totalSize.Load() <= c.maxSize || len(c.evictionHeap) == 0 {
		return
	}

	deferred := make([]*evictEntry, 0)
	for c.totalSize.Load() > c.maxSize && len(c.evictionHeap) > 0 {
		entry, ok := heap.Pop(&c.evictionHeap).(*evictEntry)
		if !ok {
			continue
		}

		current, exists := c.evictEntries[entry.path]
		if !exists || current != entry {
			continue
		}
		if c.evictEntryIsOpen(entry) {
			deferred = append(deferred, entry)
			continue
		}

		if err := os.Remove(entry.path); err != nil {
			if os.IsNotExist(err) {
				delete(c.evictEntries, entry.path)
				c.totalSize.Add(-entry.size)
				continue
			}
			log.Warn().Err(err).Str("path", entry.path).
				Msg("ContentCache: error deleting for eviction")
			deferred = append(deferred, entry)
			continue
		}

		delete(c.evictEntries, entry.path)
		c.totalSize.Add(-entry.size)
		log.Debug().
			Str("path", entry.path).
			Int64("size", entry.size).
			Time("modTime", entry.modTime).
			Msg("ContentCache: file evicted due to size limit")
	}

	for _, entry := range deferred {
		heap.Push(&c.evictionHeap, entry)
	}

	log.Debug().
		Int64("remaining", c.totalSize.Load()).
		Int64("maxSize", c.maxSize).
		Msg("ContentCache: size eviction completed")
}
