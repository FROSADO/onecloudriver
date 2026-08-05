package fs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Inode represents a OneDrive file or folder as a node of the metadata tree.
// Wraps a graph.DriveItem with thread-safe protection (RWMutex)
// and adds hierarchical child tracking.
//
// Faithful to the onedriver Inode (docs/onedriverCode/fs/inode.go), with these
// diferencias deliberadas:
//   - No nodeID (the go-fuse/v2 framework manages IDs automatically)
//   - hasChanges: dirty tracking flag for write-back to OneDrive
//   - localID/isLocalID: items created locally that don't exist in OneDrive yet
//   - Mode() is computed from the DriveItem, without its own mode field (API items only)
type Inode struct {
	sync.RWMutex
	graph.DriveItem

	// children is nil when the children have NOT been fetched yet
	// (uninitialized). Once fetched, it is non-nil (can be an empty slice
	// if the folder is empty). This is the "nil = uninitialized" pattern
	// from the original onedriver.
	children []string

	// subdir counts subdirectories for the NLink = 2 + subdir calculation (POSIX).
	subdir uint32

	// mode is the custom POSIX mode (0 = use the default for the type).
	// Updated via Setattr(chmod). If 0, Mode() returns the default
	// (S_IFDIR|0755 for folders, S_IFREG|0644 for files).
	mode uint32

	// hasChanges is true when the local content differs from the server.
	// Used for write-back: on Flush/Fsync, it is uploaded to OneDrive.
	hasChanges bool

	// ──── Phase 4: TTL+LFU eviction ────
	// These fields only apply to folders with cached children (directories).
	// Used to calculate the eviction score and decide which children to free
	// when maxEntries is exceeded or the TTL expires.
	childrenAccessCount uint64    // hit counter, with decay on each sweep
	childrenLastAccess  time.Time // last time the children were accessed
	childrenCachedAt    time.Time // when the children were populated
}

// localID prefix is used to identify items created locally that haven't
// have not been uploaded to OneDrive yet.
const localIDPrefix = "local-"

// isLocalID returns true if the ID was generated locally (item not yet uploaded).
func isLocalID(id string) bool {
	return len(id) > len(localIDPrefix) && id[:len(localIDPrefix)] == localIDPrefix
}

// newLocalID generates a unique ID for a locally created item.
func newLocalID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Deterministic fallback if rand fails (extremely unlikely)
		return localIDPrefix + "fallback"
	}
	return localIDPrefix + hex.EncodeToString(b)
}

// SerializeableInode is a DTO (Data Transfer Object) for JSON/BoltDB serialization.
// Avoids serializing sync.RWMutex and time.Time (which has unexported fields).
type SerializeableInode struct {
	graph.DriveItem
	Children             []string `json:"children"`
	Subdir               uint32   `json:"subdir"`
	Mode                 uint32   `json:"mode"`
	HasChanges           bool     `json:"hasChanges"`
	ChildrenAccessCount  uint64   `json:"childrenAccessCount"`
	ChildrenLastAccessMs int64    `json:"childrenLastAccessMs"` // UnixMilli
	ChildrenCachedAtMs   int64    `json:"childrenCachedAtMs"`   // UnixMilli
}

// NewInodeDriveItem creates an Inode from a DriveItem obtained from the API.
func NewInodeDriveItem(item *graph.DriveItem) *Inode {
	if item == nil {
		return nil
	}
	return &Inode{
		DriveItem: *item,
	}
}

// NewInodeLocal creates an Inode for a locally created file/folder that
// doesn't have a representation in OneDrive yet. Uses a UUID-generated local ID.
// Faithful to onedriver's NewInode pattern.
func NewInodeLocal(name string, mode uint32, parent *Inode) *Inode {
	item := &graph.DriveItem{
		ID:   newLocalID(),
		Name: name,
	}
	if mode&syscall.S_IFDIR != 0 {
		item.Folder = &graph.Folder{}
	}
	if parent != nil {
		item.Parent = &graph.DriveItemParent{ID: parent.ID()}
	}
	return &Inode{
		DriveItem:  *item,
		hasChanges: !item.IsFolder(), // new files: dirty until uploaded
	}
}

// HasChanges returns true if the local content differs from the server.
func (i *Inode) HasChanges() bool {
	i.RLock()
	defer i.RUnlock()
	return i.hasChanges
}

// SetHasChanges sets/clears the dirty tracking flag.
func (i *Inode) SetHasChanges(v bool) {
	i.Lock()
	defer i.Unlock()
	i.hasChanges = v
}

// ID returns the item's internal ID (thread-safe).
func (i *Inode) ID() string {
	i.RLock()
	defer i.RUnlock()
	return i.DriveItem.ID
}

// Name returns the item's name (thread-safe).
func (i *Inode) Name() string {
	i.RLock()
	defer i.RUnlock()
	return i.DriveItem.Name
}

// IsDir returns true if the item is a folder.
func (i *Inode) IsDir() bool {
	i.RLock()
	defer i.RUnlock()
	return i.DriveItem.IsFolder()
}

// Mode returns the POSIX permissions/mode of the item.
// If a custom mode has been set via chmod (Setattr),
// that one is returned; otherwise it is computed from the type (folder vs file).
func (i *Inode) Mode() uint32 {
	i.RLock()
	customMode := i.mode
	i.RUnlock()
	if customMode != 0 {
		return customMode
	}
	if i.IsDir() {
		return syscall.S_IFDIR | 0755
	}
	return syscall.S_IFREG | 0644
}

// SetMode sets a custom POSIX mode. Use 0 to return to the default.
func (i *Inode) SetMode(m uint32) {
	i.Lock()
	defer i.Unlock()
	i.mode = m
}

// ModTime returns the modification date as a Unix timestamp.
func (i *Inode) ModTime() uint64 {
	i.RLock()
	defer i.RUnlock()
	return i.DriveItem.ModTimeUnix()
}

// Size returns the size in bytes. For folders, returns 4096 (standard).
func (i *Inode) Size() uint64 {
	if i.IsDir() {
		return 4096
	}
	i.RLock()
	defer i.RUnlock()
	return i.DriveItem.Size
}

// NLink returns the number of hard links. For files it's 1.
// For folders: 2 + subdir (POSIX standard, faithful to the original onedriver).
func (i *Inode) NLink() uint32 {
	if i.IsDir() {
		i.RLock()
		defer i.RUnlock()
		return 2 + i.subdir
	}
	return 1
}

// ParentID returns the parent ID, or "" if it has none.
func (i *Inode) ParentID() string {
	i.RLock()
	defer i.RUnlock()
	if i.DriveItem.Parent == nil {
		return ""
	}
	return i.DriveItem.Parent.ID
}

// Path returns the full path of the item.
func (i *Inode) Path() string {
	name := i.Name()
	if i.ParentID() == "" && name == "root" {
		return "/"
	}
	return name
}

// Children returns the child IDs. nil = not initialized.
func (i *Inode) Children() []string {
	i.RLock()
	defer i.RUnlock()
	return i.children
}

// HasChildren returns true if the item has real children (according to the
// Graph API or the already-fetched children). Faithful to the original onedriver
// that uses Folder.ChildCount.
//
// For local items (created with Mkdir but without fetching children yet),
// it returns false because ChildCount is 0.
func (i *Inode) HasChildren() bool {
	i.RLock()
	defer i.RUnlock()
	// If the API reports ChildCount > 0, there are children (even if not fetched)
	if i.DriveItem.Folder != nil && i.DriveItem.Folder.ChildCount > 0 {
		return true
	}
	// For items without ChildCount (local or API with ChildCount=0),
	// check the local list if it was already initialized
	return len(i.children) > 0
}

// IsChildrenFetched returns true if the children were already fetched from the
// API (children != nil), regardless of whether there are 0 or N children.
// Used in InodeCache to decide cache hit vs miss.
func (i *Inode) IsChildrenFetched() bool {
	i.RLock()
	defer i.RUnlock()
	return i.children != nil
}

// SetChildren sets the children IDs. Calling with an empty slice
// indicates the folder was fetched and is empty (different from nil).
// Also initializes the eviction tracking fields (Phase 4).
func (i *Inode) SetChildren(ids []string) {
	i.Lock()
	defer i.Unlock()
	now := time.Now()
	i.children = ids
	i.childrenCachedAt = now
	i.childrenLastAccess = now
	// We intentionally do not reset accessCount: the freshness window
	// of GetChildren (time.Since(childrenCachedAt) < effectiveTTL) grows with
	// the frequency, and refetching should not punish it. The sweep decay
	// (accessCount >>= 1 every 30s) + the 20× effectiveTTL cap keep it
	// bounded over the process lifetime. If it is the first time, it starts
	// en 0.
}

// ──── Phase 4: Eviction getters/setters ────

// ChildrenAccessCount returns the children hit counter.
func (i *Inode) ChildrenAccessCount() uint64 {
	i.RLock()
	defer i.RUnlock()
	return i.childrenAccessCount
}

// ChildrenLastAccess returns the last time the children were accessed.
func (i *Inode) ChildrenLastAccess() time.Time {
	i.RLock()
	defer i.RUnlock()
	return i.childrenLastAccess
}

// ChildrenCachedAt returns when the children were populated.
func (i *Inode) ChildrenCachedAt() time.Time {
	i.RLock()
	defer i.RUnlock()
	return i.childrenCachedAt
}

// BumpChildrenAccess increments the hit counter and updates lastAccess.
// Thread-safe: usa el lock del Inode.
func (i *Inode) BumpChildrenAccess() {
	i.Lock()
	i.childrenAccessCount++
	i.childrenLastAccess = time.Now()
	i.Unlock()
}

// DecayChildrenAccess applies decay to the counter: accessCount >>= 1.
// Called from the eviction sweep (Phase 4).
func (i *Inode) DecayChildrenAccess() {
	i.Lock()
	i.childrenAccessCount >>= 1
	i.Unlock()
}

// EvictChildren frees the cached children (sets them to nil).
// The Inode still exists in the tree; only memory is freed.
func (i *Inode) EvictChildren() {
	i.Lock()
	i.children = nil
	i.Unlock()
}

// Subdir returns the number of subdirectories.
func (i *Inode) Subdir() uint32 {
	i.RLock()
	defer i.RUnlock()
	return i.subdir
}

// SetSubdir sets the number of subdirectories.
func (i *Inode) SetSubdir(n uint32) {
	i.Lock()
	defer i.Unlock()
	i.subdir = n
}

// DriveItemPtr returns a pointer to the internal DriveItem for compatibility
// with code that expects *graph.DriveItem.
func (i *Inode) DriveItemPtr() *graph.DriveItem {
	return &i.DriveItem
}

// makeAttr builds a complete fuse.Attr from the Inode for use with FUSE.
// Faithful to the original onedriver, including Owner (process UID/GID).
func (i *Inode) makeAttr() fuse.Attr {
	mtime := i.ModTime()
	return fuse.Attr{
		Ino:   0, // go-fuse assigns the Ino automatically
		Size:  i.Size(),
		Nlink: i.NLink(),
		Ctime: mtime,
		Mtime: mtime,
		Atime: mtime,
		Mode:  i.Mode(),
		Owner: fuse.Owner{
			Uid: uint32(os.Getuid()), //#nosec G115 -- UID/GID range is within uint32 on Linux systems
			Gid: uint32(os.Getgid()), //#nosec G115 -- UID/GID range is within uint32 on Linux systems
		},
	}
}

// AsJSON serializes the Inode to JSON for persistence (BoltDB).
// ⚠️ No es MarshalJSON() — el original de onedriver documenta que implementar
// the standard interface breaks delta sync for business accounts.
func (i *Inode) AsJSON() []byte {
	i.RLock()
	defer i.RUnlock()
	data, _ := json.Marshal(SerializeableInode{
		DriveItem:            i.DriveItem,
		Children:             i.children,
		Subdir:               i.subdir,
		Mode:                 i.mode,
		HasChanges:           i.hasChanges,
		ChildrenAccessCount:  i.childrenAccessCount,
		ChildrenLastAccessMs: i.childrenLastAccess.UnixMilli(),
		ChildrenCachedAtMs:   i.childrenCachedAt.UnixMilli(),
	})
	return data
}

// fillEntryOut fills a fuse.EntryOut with the metadata of a DriveItem
// so the Linux kernel can cache the lookup.
func fillEntryOut(out *fuse.EntryOut, item *graph.DriveItem) {
	if item.IsFolder() {
		out.Mode = syscall.S_IFDIR | 0755
		out.Nlink = 2
		out.Size = 4096
	} else {
		out.Mode = syscall.S_IFREG | 0644
		out.Nlink = 1
		out.Size = item.Size
	}
	out.Owner.Uid = uint32(os.Getuid()) //#nosec G115 -- UID/GID range is within uint32 on Linux systems
	out.Owner.Gid = uint32(os.Getgid()) //#nosec G115 -- UID/GID range is within uint32 on Linux systems

	if item.ModTime != nil {
		out.Mtime = item.ModTimeUnix()
		out.Atime = out.Mtime
		out.Ctime = out.Mtime
	} else {
		unix := max(time.Now().Unix(), 0)
		now := uint64(unix) //#nosec G115 -- guarded: unix >= 0 checked above
		out.Mtime = now
		out.Atime = now
		out.Ctime = now
	}
	out.Blksize = 4096
	out.Blocks = (out.Size + 511) / 512
	out.SetEntryTimeout(1 * time.Second)
	out.SetAttrTimeout(1 * time.Second)
}

// NewInodeJSON rebuilds an Inode from JSON stored in BoltDB.
func NewInodeJSON(data []byte) (*Inode, error) {
	var raw SerializeableInode
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return &Inode{
		DriveItem:           raw.DriveItem,
		children:            raw.Children,
		subdir:              raw.Subdir,
		mode:                raw.Mode,
		hasChanges:          raw.HasChanges,
		childrenAccessCount: raw.ChildrenAccessCount,
		childrenLastAccess:  time.UnixMilli(raw.ChildrenLastAccessMs),
		childrenCachedAt:    time.UnixMilli(raw.ChildrenCachedAtMs),
	}, nil
}
