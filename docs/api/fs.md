# API: internal/fs

> Auto-generated with `go doc -all`. Date: 2026-08-27 09:06:13

```
package fs // import "github.com/frosado/onecloudriver/internal/fs"


VARIABLES

var ErrCacheClosed = errors.New("content cache is closed")
    ErrCacheClosed is returned by the mutating ContentCache operations once
    the cache has been closed (CloseAll, during shutdown). Reporting it is what
    distinguishes "the content was written" from "the content was dropped":
    returning nil would make the FUSE layer acknowledge a write whose bytes
    never reached the disk.

var ErrIsRoot = fmt.Errorf("the root has no representation in InodeCache")
    ErrIsRoot is returned when GetPath receives the root path "/". The root has
    no Inode in the cache — OneCloudFS represents it directly.


TYPES

type CacheHandles struct {
	Metadata *InodeCache
	Content  *ContentCache
	Delta    *DeltaSync
	Uploads  *UploadManager
}
    CacheHandles lets the UI (or other components) interact with the caches
    while hot: query stats, modify configuration, force refreshes.

func Mount(mountpoint string, account *auth.Account, config MountConfig) (*CacheHandles, error)
    Mount starts the FUSE server and handles safe unmounting on Ctrl+C. Returns
    CacheHandles so the UI can manage the cache in real time.

type ChildrenFetcher func(ctx context.Context, parentID string) ([]graph.DriveItem, error)
    ChildrenFetcher queries a folder's children from the Graph API. It's
    injected to decouple InodeCache from graph.Client and facilitate testing.

type ContentCache struct {
	// Has unexported fields.
}

func NewContentCache(directory string) (*ContentCache, error)
    NewContentCache creates a new ContentCache. Creates the directory if it does
    not exist.

    LoopbackCache ignore the error of os.Mkdir with `os.Mkdir(directory, 0700)`
    without checking the return value. Here the error is propagated explicitly.

func (c *ContentCache) Close(id string)
    Close closes the open FD for an ID, if it exists. It does not delete the
    file. Only triggers eviction if the FD was actually open (when closing it,
    the file stops being protected by IsOpen and could become evictable).

func (c *ContentCache) CloseAll()
    CloseAll closes all open FDs and clears the fds map. It is called during
    shutdown to free resources.

func (c *ContentCache) Delete(id string) error
    Delete closes the FD and removes the content from disk.

func (c *ContentCache) ForceEvict()
    ForceEvict runs synchronous eviction (useful for tests and UI).

func (c *ContentCache) HasContent(id string) bool
    HasContent checks whether the content exists in the cache (either open or on
    disk).

func (c *ContentCache) Insert(id string, content []byte) error
    Insert writes content to a cache file in one shot. After writing, checks if
    maxSize was exceeded and triggers eviction if needed.

func (c *ContentCache) InsertStream(id string, reader io.Reader) (int64, error)
    InsertStream copies a stream to the cached file, repositioning to the start
    and truncating the previous content before writing.

    🔧 Correction from the previous version: NO independent Writer()/nopCloser
    method is exposed. InsertStream operates directly on the FD returned
    by Open(), like the original LoopbackCache. An additional wrapper that
    truncates the FD outside this single entry point would open a corruption
    window if another goroutine had the same FD open for reading in parallel
    (the fds sync.Map design assumes the FD is shared and only positioned/
    truncated here, in a controlled way).

    After writing, checks if maxSize was exceeded and triggers eviction if
    needed.

func (c *ContentCache) IsOpen(id string) bool
    IsOpen returns true if the file is already open somewhere.

func (c *ContentCache) MaxSize() int64
    MaxSize returns the configured maximum size.

func (c *ContentCache) Open(id string) (*os.File, error)
    Open opens or creates a file in the cache. If already open, reuses the FD.
    Uses runtime.SetFinalizer(fd, nil) to prevent the GC from closing the FD
    while still in use (same pattern as onedriver).

    Phase 4b: briefly acquires evictMu during file creation to prevent
    evictBySize() from deleting the file between os.OpenFile and fds.Store
    (ventana TOCTOU). Si el FD ya existe en fds, no necesita lock.

func (c *ContentCache) ReadAll(id string) []byte
    ReadAll reads all the cached file content and returns it as []byte.
    It is used to take content snapshots before enqueuing an upload
    (UploadManager.QueueUpload). If the file does not exist, it returns nil.

    The signature cannot carry an error (callers only need "content or
    nothing"), so an I/O failure is logged: an unreadable cache file and an
    absent one are indistinguishable to the caller otherwise.

func (c *ContentCache) SetMaxSize(maxBytes int64)
    SetMaxSize sets the maximum size in bytes of the ContentCache on disk.
    When totalSize exceeds maxSize, automatic age-based eviction activates.
    0 = no limit.

func (c *ContentCache) Size(id string) int64
    Size returns the current size of the file cached on disk. If the file does
    not exist, it returns 0.

func (c *ContentCache) Snapshot(id string) (string, int64, error)
    Snapshot creates an independent on-disk copy of the cached content for
    id and returns the snapshot's path and size. The copy is taken under the
    per-inode lock, so it is atomic with respect to concurrent writes (issue
    #64), and it is independent of the live cache file, so later edits or
    eviction do not affect it. This lets the upload path stream from disk
    without materializing the whole file in heap (issue #69).

    The caller owns the returned file and must remove it when it is no longer
    needed. A missing file returns ("", 0, os.ErrNotExist); an empty file
    returns ("", 0, nil) after removing its (empty) snapshot.

    🔒 Security: the snapshot is created inside the user's cache directory (see
    snapshotsDir), not in a shared location like /tmp, and with owner-only
    permissions (0700 directory, 0600 file) so its content is not exposed to
    other local users.

func (c *ContentCache) SumQuickXorHash(id string) (string, bool)
    SumQuickXorHash computes the base64 quickXorHash of the cached content.
    It returns ("", false) when the content does not exist or cannot be read.
    Used to verify cached/downloaded content against the server metadata (issue
    #32, content-integrity verification).

func (c *ContentCache) TotalDiskUsage() int64
    TotalDiskUsage walks the cache directory and sums the real sizes of all
    files on disk. Useful for reconciling totalSize with filesystem reality
    (e.g. at startup, or for debugging).

func (c *ContentCache) TotalSize() int64
    TotalSize returns the total tracked size of the cache on disk.

func (c *ContentCache) WriteAt(id string, data []byte, off int64) (int, error)
    WriteAt writes data at a specific position of the cached file. Useful for
    incremental writes from FUSE Write (WriteAt allows arbitrary offsets).
    After writing, adjusts totalSize and triggers eviction if the file grew.

type DeltaSync struct {
	// Has unexported fields.
}

func NewDeltaSync(
	graphClient *graph.Client,
	tokenProvider types.TokenProvider,
	inodeCache *InodeCache,
	contentCache *ContentCache,
) *DeltaSync
    NewDeltaSync creates a new delta synchronization service.

func (d *DeltaSync) Counters() (cycles, errors uint64)
    Counters returns the total number of completed delta sync cycles and the
    number of cycles that ended in error. Safe to call from any goroutine (used
    by the expvar debug server to expose delta_sync_count/ delta_error_count,
    issue #74).

func (d *DeltaSync) PollOnce(ctx context.Context) (int, error)
    PollOnce performs a single delta cycle immediately and returns the number
    of remote changes applied. It is the one-shot counterpart of the background
    deltaLoop, used by the `onecloudriver sync` command (issue #73): it reuses
    pollAndApply and, on success, persists the delta link and the dirty inodes
    exactly like a background cycle, so a later PollOnce or the mount's loop
    resumes from where it left off. Does not start any goroutine and does not
    require a mount.

func (d *DeltaSync) SetUploadQuery(q uploadQuery)
    SetUploadQuery wires a source of pending-upload state (the UploadManager)
    so applyDelta can skip remote changes for items whose local upload has not
    completed yet. Must be called before Start. Nil is allowed (guards skipped).

func (d *DeltaSync) Start(ctx context.Context, interval time.Duration)
    Start begins the delta polling in the background with the specified
    interval. Must be called only once. To stop it, call Stop().

func (d *DeltaSync) Stop()
    Stop stops the delta polling and waits for the goroutine to finish.

type DriveItemNode struct {
	fs.Inode

	// Has unexported fields.
}
    DriveItemNode represents a OneDrive file or folder as a FUSE node. Contains
    a *Inode (thread-safe wrapper around graph.DriveItem with hierarchical child
    tracking) and references to the global caches.

func (n *DriveItemNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno)
    Create creates a new file and opens it for writing.

func (n *DriveItemNode) Flush(ctx context.Context, fh fs.FileHandle) syscall.Errno
    Flush is called when a file descriptor is closed. Triggers Fsync + closes
    the FD.

func (n *DriveItemNode) Fsync(_ context.Context, _ fs.FileHandle, _ uint32) syscall.Errno
    Fsync marks the local changes as "uploaded" and enqueues the actual upload
    to the UploadManager to be processed in the background. This decouples the
    FUSE write (fast, interactive) from the HTTP upload (slow, with retries).

    Phase 5b: the UploadManager takes a content snapshot from ContentCache when
    enqueuing, so later modifications to the file do not affect the in-flight
    upload.

    If uploadManager is nil (tests without a manager), it simply marks
    hasChanges=false and returns success — the content stays in ContentCache but
    is not uploaded.

func (n *DriveItemNode) GetFolderID() string
    GetFolderID returns the ID of the folder represented by this node (for
    rename).

func (n *DriveItemNode) Getattr(_ context.Context, _ fs.FileHandle, out *fuse.AttrOut) syscall.Errno
    Getattr returns the file/folder metadata.

func (n *DriveItemNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno)
    Lookup searches for a specific file/folder by name.

func (n *DriveItemNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno)
    Mkdir creates a new folder on OneDrive.

func (n *DriveItemNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno)
    Open opens a file for reading, writing, or both.

func (n *DriveItemNode) Read(_ context.Context, _ fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno)
    Read reads the contents of a file (zero-copy from ContentCache).

func (n *DriveItemNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno)
    Readdir lists the contents of a folder.

func (n *DriveItemNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno
    Rename renames or moves a file/folder.

func (n *DriveItemNode) Rmdir(ctx context.Context, name string) syscall.Errno
    Rmdir removes an empty folder from OneDrive.

func (n *DriveItemNode) Setattr(_ context.Context, _ fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno
    Setattr handles POSIX operations: chmod, utimens (touch), and truncate.
    Faithful to the onedriver SetAttr, adapted to go-fuse/v2.

func (n *DriveItemNode) Statfs(_ context.Context, out *fuse.StatfsOut) syscall.Errno
    Statfs returns information about the filesystem (total space, free space,
    name limits). Uses reasonable default values for OneDrive Personal (5 TB
    quota, 100K files, maximum name 260-character name limit).

    The values are static estimates because we don't cache the /me/drive
    response (it would require an additional HTTP call). If in the future it's
    cached the Drive object, these values can be replaced by the real ones.

func (n *DriveItemNode) Unlink(ctx context.Context, name string) syscall.Errno
    Unlink deletes a file from OneDrive.

func (n *DriveItemNode) Write(_ context.Context, _ fs.FileHandle, data []byte, off int64) (uint32, syscall.Errno)
    Write writes data to the file at the specified position. Writes are local
    (write-back): they are persisted to OneDrive on Flush/Fsync.

type EvictionController struct {
	// Has unexported fields.
}
    EvictionController consolidates the shared plumbing used by both InodeCache
    (metadata) and ContentCache (content) for background eviction:

      - startEviction: a periodically-sweeping goroutine (Stop()/Stop
        idempotent)
      - RunSerialized: a mutex-serialized execution window (used to guard an
        eviction pass so it can't interleave with itself / file creation).
      - RunOnce: a deduplication flag ensuring only one in-flight eviction
        goroutine at a time.

    Each cache keeps its own eviction policy (which items to evict and in what
    order) as plain callbacks / bodies around RunSerialized/RunOnce; this type
    only owns the concurrency and lifecycle mechanics.

func NewEvictionController(interval time.Duration, sweep func()) *EvictionController
    NewEvictionController creates a controller. If interval > 0, it runs a
    periodic sweep goroutine; if sweep == nil the periodic path is unused
    (callers drive eviction directly via RunSerialized/RunOnce).

func (c *EvictionController) Lock()
    Lock acquires the controller's lock. Used by ContentCache.Open to guard file
    creation against eviction (TOCTOU window).

func (c *EvictionController) RunOnce(fn func()) bool
    RunOnce runs fn in a background goroutine but only if no other eviction
    goroutine is already in flight. The execution is serialized with
    RunSerialized to prevent concurrent evictions.

func (c *EvictionController) RunSerialized(fn func())
    RunSerialized executes fn while holding the controller's lock, guaranteeing
    that only one eviction/flush pass runs at a time and that it won't
    interleave with other lock-acquiring operations.

func (c *EvictionController) Start()
    Start launches the periodic eviction goroutine. Idempotent: calling it more
    than once has no effect. On each tick, it calls RunOnce(sweep) so that only
    one eviction (periodic or manual) runs at a time.

func (c *EvictionController) Stop()
    Stop halts the periodic goroutine, if running. Idempotent.

func (c *EvictionController) Unlock()
    Unlock releases the controller's lock.

type Inode struct {
	sync.RWMutex
	graph.DriveItem

	// Has unexported fields.
}
    Inode represents a OneDrive file or folder as a node of the metadata tree.
    Wraps a graph.DriveItem with thread-safe protection (RWMutex) and adds
    hierarchical child tracking.

    Faithful to the onedriver Inode (docs/onedriverCode/fs/inode.go), with these
    diferencias deliberadas:
      - No nodeID (the go-fuse/v2 framework manages IDs automatically)
      - hasChanges: dirty tracking flag for write-back to OneDrive
      - localID/isLocalID: items created locally that don't exist in OneDrive
        yet
      - Mode() is computed from the DriveItem, without its own mode field (API
        items only)

func NewInodeDriveItem(item *graph.DriveItem) *Inode
    NewInodeDriveItem creates an Inode from a DriveItem obtained from the API.

func NewInodeJSON(data []byte) (*Inode, error)
    NewInodeJSON rebuilds an Inode from JSON stored in BoltDB.

func NewInodeLocal(name string, mode uint32, parent *Inode) *Inode
    NewInodeLocal creates an Inode for a locally created file/folder that
    doesn't have a representation in OneDrive yet. Uses a UUID-generated local
    ID. Faithful to onedriver's NewInode pattern.

func (i *Inode) AsJSON() []byte
    AsJSON serializes the Inode to JSON for persistence (BoltDB). ⚠️ No es
    MarshalJSON() — el original de onedriver documenta que implementar the
    standard interface breaks delta sync for business accounts.

func (i *Inode) BumpChildrenAccess()
    BumpChildrenAccess increments the hit counter and updates lastAccess.
    Thread-safe: usa el lock del Inode.

func (i *Inode) Children() []string
    Children returns the child IDs. nil = not initialized.

func (i *Inode) ChildrenAccessCount() uint64
    ChildrenAccessCount returns the children hit counter.

func (i *Inode) ChildrenCachedAt() time.Time
    ChildrenCachedAt returns when the children were populated.

func (i *Inode) ChildrenLastAccess() time.Time
    ChildrenLastAccess returns the last time the children were accessed.

func (i *Inode) DecayChildrenAccess()
    DecayChildrenAccess applies decay to the counter: accessCount >>= 1.
    Called from the eviction sweep (Phase 4).

func (i *Inode) DriveItemPtr() *graph.DriveItem
    DriveItemPtr returns a pointer to the internal DriveItem for compatibility
    with code that expects *graph.DriveItem.

func (i *Inode) EvictChildren()
    EvictChildren frees the cached children (sets them to nil). The Inode still
    exists in the tree; only memory is freed.

func (i *Inode) HasChanges() bool
    HasChanges returns true if the local content differs from the server.

func (i *Inode) HasChildren() bool
    HasChildren returns true if the item has real children (according to the
    Graph API or the already-fetched children). Faithful to the original
    onedriver that uses Folder.ChildCount.

    For local items (created with Mkdir but without fetching children yet),
    it returns false because ChildCount is 0.

func (i *Inode) ID() string
    ID returns the item's internal ID (thread-safe).

func (i *Inode) IsChildrenFetched() bool
    IsChildrenFetched returns true if the children were already fetched from
    the API (children != nil), regardless of whether there are 0 or N children.
    Used in InodeCache to decide cache hit vs miss.

func (i *Inode) IsDir() bool
    IsDir returns true if the item is a folder.

func (i *Inode) ModTime() uint64
    ModTime returns the modification date as a Unix timestamp.

func (i *Inode) Mode() uint32
    Mode returns the POSIX permissions/mode of the item. If a custom mode has
    been set via chmod (Setattr), that one is returned; otherwise it is computed
    from the type (folder vs file).

func (i *Inode) NLink() uint32
    NLink returns the number of hard links. For files it's 1. For folders:
    2 + subdir (POSIX standard, faithful to the original onedriver).

func (i *Inode) Name() string
    Name returns the item's name (thread-safe).

func (i *Inode) ParentID() string
    ParentID returns the parent ID, or "" if it has none.

func (i *Inode) Path() string
    Path returns the full path of the item.

func (i *Inode) SetChildren(ids []string)
    SetChildren sets the children IDs. Calling with an empty slice indicates the
    folder was fetched and is empty (different from nil). Also initializes the
    eviction tracking fields (Phase 4).

func (i *Inode) SetHasChanges(v bool)
    SetHasChanges sets/clears the dirty tracking flag.

func (i *Inode) SetMode(m uint32)
    SetMode sets a custom POSIX mode. Use 0 to return to the default.

func (i *Inode) SetSubdir(n uint32)
    SetSubdir sets the number of subdirectories.

func (i *Inode) Size() uint64
    Size returns the size in bytes. For folders, returns 4096 (standard).

func (i *Inode) Subdir() uint32
    Subdir returns the number of subdirectories.

type InodeCache struct {
	// Has unexported fields.
}
    InodeCache is the global metadata cache. Stores *Inode in a sync.Map indexed
    by item ID. Each Inode knows its children via []string IDs.

    Faithful to onedriver's design (sync.Map + Inode tree), with this
    difference:
      - BoltDB added in Phase 3 for persistence across restarts
      - Background TTL+LFU eviction (Phase 4)
      - ChildrenFetcher injected to decouple from graph.Client

    The key pattern is children nil = not initialized:
      - nil → never fetched → GetChildren calls the fetcher
      - []string{} → fetched and empty → GetChildren returns empty without HTTP
      - []string{"id1","id2"} → fetched with children → O(1) lookup

func NewInodeCache() *InodeCache
    NewInodeCache creates a new empty inode cache with default values.

func (c *InodeCache) BaseTTL() time.Duration
    BaseTTL returns the configured base TTL.

func (c *InodeCache) Close() error
    Close closes BoltDB and stops the sweep goroutine. Safe to call multiple
    times and from several concurrent goroutines (Mount's defer + the unmount
    signal handler), thanks to closeMu.

func (c *InodeCache) Delete(id string)
    Delete removes an inode from the cache and from the parent's children.

func (c *InodeCache) DeleteUploadSession(id string)
    DeleteUploadSession removes an UploadSession from BoltDB. A failure leaves
    a stale session that would be restored (and cancelled) on the next start,
    so it is logged.

func (c *InodeCache) DeserializeFromDisk() error
    DeserializeFromDisk carga inodos desde BoltDB a sync.Map.

func (c *InodeCache) ForceSweep()
    ForceSweep runs an immediate sweep (useful for tests and UI). Does not wait
    for the next timer tick.

func (c *InodeCache) Get(id string) *Inode
    Get obtains an inode by ID. Returns nil if it doesn't exist.

func (c *InodeCache) GetChild(ctx context.Context, parentID, name string, fetch ChildrenFetcher) (*Inode, error)
    GetChild searches for a child by name within a folder.

func (c *InodeCache) GetChildren(
	ctx context.Context,
	parentID string,
	fetch ChildrenFetcher,
) (map[string]*Inode, error)
    GetChildren gets a folder's children. If the children are already cached
    (non-nil) AND fresh (within the effective TTL), it returns them without
    calling the fetcher. If they are nil or stale, it invokes the fetcher,
    populates the cache, and creates Inodes for each child.

    Returns a name → *Inode map for O(1) lookups by name (compatible with the
    current DriveItemNode/OneCloudFS API).

func (c *InodeCache) GetDeltaLink() string
    GetDeltaLink returns the stored delta link URL. A read failure returns an
    empty link (the caller then starts a full delta cycle), and is logged:
    a silently dropped link makes the next sync re-enumerate the whole drive
    with no explanation.

func (c *InodeCache) GetPath(ctx context.Context, path string, fetch ChildrenFetcher) (*Inode, error)
    GetPath navigates the inode tree from the root following a path separated by
    "/" and returns the Inode at the leaf. Useful for resolving full paths (e.g.
    CLI or offline mode) without querying Graph.

    The path "/" returns ErrIsRoot (the root is not an Inode in cache,
    OneCloudFS represents it directly). Paths like "a/b/c" are navigated
    component by component.

func (c *InodeCache) InitBoltDB(dbPath string) error
    InitBoltDB abre (o crea) la base de datos BoltDB y carga los datos
    existentes. Must be called once, before using the cache.

func (c *InodeCache) Insert(inode *Inode)
    Insert adds or replaces an inode in the cache. If the inode has a ParentID,
    updates the parent's subdir and children automatically.

func (c *InodeCache) InsertChild(parentID, _ string, childInode *Inode)
    Insert adds a child inode to a specific parent. Useful for Mkdir/Create
    where the inode was just created and we already know parent and child.

func (c *InodeCache) Invalidate(parentID string)
    Invalidate marks a folder's children as uninitialized, forcing a refetch on
    the next GetChildren. Does not delete individual Inodes (they remain part of
    the tree).

    Useful after creating/deleting/moving files in a folder.

func (c *InodeCache) InvalidateAll()
    InvalidateAll resets ALL children of the cache, forcing a complete refetch
    on the next accesses. Useful for "Force refresh" in the UI.

func (c *InodeCache) IsOffline() bool
    IsOffline returns true if the cache is in offline mode.

func (c *InodeCache) ItemsByParent(parentID string) []graph.DriveItem
    ItemsByParent returns the DriveItems of all inodes in memory whose ParentID
    matches parentID. Used as a fallback in offline mode when the parent's
    children list was evicted by the TTL sweep (children=nil) but the child
    inodes remain in the sync.Map with their parent reference.

func (c *InodeCache) LoadUploadSessions() map[string][]byte
    LoadUploadSessions loads all incomplete upload sessions from BoltDB and
    returns them as an id → JSON map. Used at startup to restore sessions that
    did not finish (abrupt shutdown, crash, etc.).

func (c *InodeCache) MaxEntries() int
    MaxEntries returns the configured maximum.

func (c *InodeCache) MoveChild(oldParentID, newParentID, childID string)
    MoveChild moves a child from one parent folder to another. Useful for
    rename/move.

func (c *InodeCache) MoveID(oldID, newID string)
    MoveID moves an inode from an old key to a new one. Used when a local item
    receives a real OneDrive ID after the first upload.

    🔧 In addition to re-keying in the sync.Map, updates DriveItem.ID to
    the new ID: before, the inode kept the local ID in its internal field,
    so ItemsByParent (offline fallback) and any code reading inode.ID() kept
    seeing the old ID → inconsistency after the local→remote swap (bug destapado
    por TestInodeCache_GetChildren_ParentID_Then_MoveID).

func (c *InodeCache) PrefetchChildren(
	ctx context.Context,
	parentID string,
	fetch ChildrenFetcher,
) (map[string]*Inode, error)
    PrefetchChildren populates the cache exactly like GetChildren, but does
    not mark the fetched inodes dirty. Bulk warm-ups (preWarm) use it so that
    fetching a large subtree does not force SerializeDirty to rewrite the whole
    tree on the next delta poll; the tree is still persisted on unmount by
    SerializeAll. Cache hit/miss counters and eviction tracking behave exactly
    like GetChildren.

func (c *InodeCache) RemoveChild(parentID, childID string)
    RemoveChild removes a child from a parent. Useful for Rmdir/Unlink.

func (c *InodeCache) SaveUploadSession(id string, data []byte)
    SaveUploadSession persists an UploadSession to BoltDB so it survives
    across restarts. The caller must serialize the session to JSON beforehand.
    A persistence failure only costs the session on the next restart (the upload
    itself proceeds from memory), so it is logged rather than propagated.

func (c *InodeCache) SerializeAll() error
    SerializeAll persiste todos los inodos en memoria a BoltDB.

    Persists the complete browsed tree:
      - Folders whose children were already fetched (IsChildrenFetched)
      - ALL inodes with ParentID (files and subfolders of the tree), even if
        their parent's children list was evicted by the TTL sweep
      - Child inodes referenced by fetched folders

    🔧 Without persisting inodes with ParentID, a subtree whose folder was
    evicted by TTL (children→nil) didn't survive the round-trip: when remounting
    offline, the ParentID fallback (ItemsByParent) couldn't find the inodes and
    the subfolders appeared empty (bug detected in the real offline test).

func (c *InodeCache) SerializeDirty() error
    SerializeDirty persiste solo los inodos sucios (marcados por markDirty desde
    la última serialización) a BoltDB, en batches de serializeDirtyBatchSize
    inodos por transacción. Los inodos eliminados de memoria (tombstones) se
    borran también de BoltDB para que el estado en disco converja con memoria.

    A diferencia de SerializeAll (árbol completo en una sola transacción),
    el coste de escritura por delta sync crece con el número de cambios,
    no con el tamaño del árbol (issue #67). La garantía final de durabilidad
    sigue siendo el SerializeAll de unmount.

func (c *InodeCache) SetBaseTTL(ttl time.Duration)
    SetBaseTTL sets the base TTL for metadata eviction. Will be used in Phase 4
    to compute effectiveTTL = baseTTL × frequencyMultiplier.

func (c *InodeCache) SetDeltaLink(link string)
    SetDeltaLink stores the delta link URL in BoltDB. The delta loop cannot act
    on a persistence failure (it keeps the link in memory for this run), but
    an unpersisted link silently costs a full re-sync after the next restart,
    so the failure is logged.

func (c *InodeCache) SetMaxEntries(n int)
    SetMaxEntries sets the maximum number of folders with cached children before
    size eviction activates (Phase 4).

func (c *InodeCache) SetOffline(v bool)
    SetOffline enables/disables offline mode.

func (c *InodeCache) StartSweep()
    StartSweep starts the background eviction goroutine. Must be called once,
    after creating the cache.

func (c *InodeCache) Stats() InodeCacheStats
    Stats returns a copy of the statistics for the UI.

type InodeCacheStats struct {
	InodeCount int    `json:"inode_count"`
	Hits       uint64 `json:"hits"`
	Misses     uint64 `json:"misses"`
	Evictions  uint64 `json:"evictions"`
}
    InodeCacheStats is the public snapshot of the cache state.

type MountConfig struct {
	// CacheDir is the root of the cache tree. Content is stored in
	// <CacheDir>/content/ and BoltDB at <CacheDir>/inodes.db.
	// Default: ~/.cache/onecloudriver/<account>
	CacheDir string

	// CacheTTL is the base lifetime of cached metadata before it is
	// considered stale (Phase 4). Default: 60s.
	CacheTTL time.Duration

	// CacheMaxEntries is the maximum number of folders with cached children
	// in memory before activating eviction (Phase 4). Default: 2000.
	CacheMaxEntries int

	// CacheMaxSize is the maximum size in bytes of the ContentCache on disk
	// before activating age-based eviction (Phase 4b). 0 = no limit.
	CacheMaxSize int64

	// DeltaInterval controls how often the /delta endpoint is polled.
	// 0 = use the default (5 min).
	DeltaInterval time.Duration

	// MaxUploadsInFlight limits concurrent uploads (default: 5).
	MaxUploadsInFlight int

	// MaxUploadRetries is the maximum retries per upload (default: 5).
	MaxUploadRetries int

	// GraphRetries is the number of HTTP retries on 429/503 (default: 3).
	GraphRetries int

	// HTTPTimeout is the HTTP client timeout (default: 15s).
	HTTPTimeout time.Duration

	// PreWarmDepth prefetches folder metadata (not content) after mount using a
	// BFS traversal. The value counts listing levels: 1 = root, 2 = root + its
	// immediate subfolders, etc. 0 disables pre-warming. Valid range: [0, 10].
	// Default: 2.
	PreWarmDepth int

	// DebugAddr, when non-empty, starts the expvar + pprof debug HTTP server on
	// that local address (e.g. "127.0.0.1:6060") so a running mount can be
	// inspected with curl /debug/vars and /debug/pprof. Empty disables it
	// (default). Only enabled explicitly via `mount --debug` / --debug-addr.
	DebugAddr string
}
    MountConfig groups the cache configuration that the user can adjust from the
    CLI. Use the DefaultMountConfig() constructor to get default values and then
    override individual fields.

func DefaultMountConfig(accountName string, persisted *auth.AccountPersistedConfig) MountConfig
    DefaultMountConfig returns a configuration with default values reasonable
    values. If persisted is not nil, its non-zero fields override the defaults
    (letting the account JSON persist preferences).

type OneCloudFS struct {
	fs.Inode

	// Has unexported fields.
}
    OneCloudFS represents the root folder of our filesystem.

func NewOneCloudFS(
	graphClient *graph.Client,
	tokenProvider types.TokenProvider,
	inodeCache *InodeCache,
	contentCache *ContentCache,
	uploadManager *UploadManager,
) *OneCloudFS
    NewOneCloudFS creates a new root filesystem connected to OneDrive.

func (r *OneCloudFS) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno)
    Create creates a new file in the root.

func (r *OneCloudFS) GetFolderID() string
    GetFolderID returns "root" (for rename compatibility).

func (r *OneCloudFS) Getattr(_ context.Context, _ fs.FileHandle, out *fuse.AttrOut) syscall.Errno
    Getattr responds to stat on the ROOT folder.

func (r *OneCloudFS) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno)
    Lookup searches for a specific file/folder in the root.

func (r *OneCloudFS) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno)
    Mkdir creates a new folder in the root.

func (r *OneCloudFS) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno)
    Readdir lists the contents of the OneDrive root.

func (r *OneCloudFS) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno
    Rename renames/moves from the root.

func (r *OneCloudFS) Rmdir(ctx context.Context, name string) syscall.Errno
    Rmdir removes an empty folder from the root.

func (r *OneCloudFS) Statfs(_ context.Context, out *fuse.StatfsOut) syscall.Errno

func (r *OneCloudFS) Unlink(ctx context.Context, name string) syscall.Errno
    Unlink removes a file from the root.

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
    SerializeableInode is a DTO (Data Transfer Object) for JSON/BoltDB
    serialization. Avoids serializing sync.RWMutex and time.Time (which has
    unexported fields).

type UploadManager struct {
	// Has unexported fields.
}
    UploadManager orchestrates pending uploads in the background with retries.
    It decouples FUSE writes (Fsync) from the HTTP upload: Fsync only marks
    hasChanges=false and enqueues; UploadManager handles the rest.

    Faithful to onedriver's design (docs/onedriverCode/fs/upload_manager.go),
    adapted to our modular architecture without a global *Filesystem.

func NewUploadManager(
	graphClient *graph.Client,
	tokenProvider types.TokenProvider,
	inodeCache *InodeCache,
	contentCache *ContentCache,
	maxUploadsInFlight, maxUploadRetries int,
) *UploadManager
    NewUploadManager creates a new UploadManager and restores incomplete
    sessions from BoltDB (if any). Restored sessions that were in progress are
    cancelled (non-resumable, like onedriver).

    maxUploadsInFlight and maxUploadRetries wire the corresponding MountConfig
    values; a value <= 0 falls back to the package defaults.

func (um *UploadManager) CancelUpload(id string)
    CancelUpload removes any pending or in-flight upload for the given ID.
    It is called when a file is deleted (Unlink) while it was queued.

func (um *UploadManager) HasPendingUpload(id string) bool
    HasPendingUpload reports whether there is a pending or in-flight upload
    session for the given ID. Used by DeltaSync to avoid overwriting a local
    item whose upload has not completed yet.

func (um *UploadManager) InFlight() int
    InFlight reports the number of uploads currently in flight (bounded by
    maxUploadsInFlight). Safe to call from any goroutine.

func (um *UploadManager) Metrics() (completed, failed uint64)
    Metrics returns the total completed and failed upload counters. Safe to call
    from any goroutine (used by the expvar debug server, issue #74).

func (um *UploadManager) QueueUpload(id, parentID, name string) bool
    QueueUpload enqueues a file for asynchronous upload. Takes a snapshot of
    the content from ContentCache at this moment so the upload is atomic with
    respect to subsequent writes.

    If the file is empty, it is not enqueued (nothing to upload) and false
    is returned. A false return with content on disk means the read failed
    transiently (e.g. the first FUSE flush raced ahead of the write): the caller
    keeps the inode dirty so the next flush retries.

func (um *UploadManager) RenameSession(id, newParentID, newName string)
    RenameSession updates the name (and optionally the parent) of a pending,
    retrying or in-flight upload session, and re-persists it to BoltDB. Used
    when a locally-created file is renamed (or moved) before its first upload:
    the upload must create the remote item with the new name in the new folder.

    For an in-flight session the running request already captured the old name,
    so the update cannot change that PUT — but it makes retries use the new
    name, and executeUpload applies the rename/move remotely after completion
    (see applyInflightTargetChange). Unknown IDs are a no-op.

func (um *UploadManager) Start()
    Start starts the background processing loop.

func (um *UploadManager) Stop()
    Stop gracefully stops the UploadManager. Waits for the loop to finish and
    cleans up the in-memory sessions.

type UploadSession struct {

	// File data (immutable after creation)
	ID       string `json:"id"`       // current inode ID (may be local-xxx)
	ParentID string `json:"parentID"` // parent ID
	Name     string `json:"name"`     // file name

	// Content snapshot (taken when enqueuing)
	Data []byte `json:"data,omitempty"` // content to upload ([]byte variant)

	// Streaming variant (issue #69): when SnapshotPath is non-empty, the
	// content is streamed from that on-disk snapshot file instead of Data,
	// and Size holds its length. SnapshotPath is persisted so an interrupted
	// upload can be resumed after a restart without the content in BoltDB.
	SnapshotPath string `json:"snapshotPath,omitempty"` // on-disk snapshot file
	Size         int64  `json:"size,omitempty"`         // snapshot size in bytes

	// Upload state
	State   uploadState `json:"-"` // not serialized directly; getState/setState are used
	Retries int         `json:"retries"`
	LastErr string      `json:"lastErr,omitempty"` // last error (for diagnostics)

	// Transient reports whether the last upload error was a transient network
	// error (timeout, connection refused/reset, ...). A transient error never
	// abandons the session: it is retried (with backoff) until the connection
	// returns. Permanent errors (HTTP 4xx, ...) still count towards retries.
	Transient bool `json:"transient,omitempty"`

	// Has unexported fields.
}
    UploadSession contains a snapshot of the data to upload and the upload
    state. It is persisted in BoltDB to survive restarts.

    The content snapshot is taken when enqueuing the upload (QueueUpload), not
    at execute it. This avoids concurrent modifications of the file corrupting
    the ongoing upload.

func NewUploadSession(id, parentID, name string, data []byte) (*UploadSession, error)
    NewUploadSession creates an UploadSession from the inode data and a snapshot
    of the content. The snapshot is taken here so the upload is atomic with
    respect to concurrent writes.

func NewUploadSessionJSON(data []byte) (*UploadSession, error)
    NewUploadSessionJSON rebuilds an UploadSession from JSON.

func NewUploadSessionSnapshot(id, parentID, name, snapshotPath string, size int64) (*UploadSession, error)
    NewUploadSessionSnapshot creates an UploadSession that streams its content
    from an on-disk snapshot file instead of holding it in memory (issue #69).
    The snapshot is an independent copy taken under the per-inode lock, so it is
    atomic and stable even if the live cache file is edited or evicted.

func (us *UploadSession) AsJSON() ([]byte, error)
    AsJSON serializes the session to JSON for BoltDB persistence.

func (us *UploadSession) DiscardSnapshot()
    DiscardSnapshot removes the on-disk snapshot file (if any) and clears the
    reference. It is safe to call multiple times and for the []byte variant.

func (us *UploadSession) OpenContent() (io.Reader, int64, error)
    OpenContent returns a fresh io.Reader over the session content, positioned
    at the start, together with its size. For the streaming variant (issue
    #69) it opens a new FD over the on-disk snapshot; for the []byte variant it
    returns a byteReader over Data. Each call returns a new reader, so a retry
    (e.g. the 412 conflict path) never reuses a consumed reader.

    The caller must release a returned *os.File via closeContentReader when
    done; the []byte variant needs no cleanup.

```
