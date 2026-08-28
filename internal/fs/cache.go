package fs

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/rs/zerolog/log"
	bolt "go.etcd.io/bbolt"
	boltErrors "go.etcd.io/bbolt/errors"
)

// ──── Eviction constants (Phase 4) ────

const (
	// freqGrowthRate controls how much the TTL extends per hit.
	// With 0.5: 4 hits → 3.0× TTL, 8 hits → 5.0× TTL.
	freqGrowthRate = 0.5

	// freqMultiplierMax is the maximum TTL multiplier by frequency.
	// Protects very active directories from expiring prematurely.
	freqMultiplierMax = 20.0

	// sweepInterval is the time between eviction sweeps.
	sweepInterval = 30 * time.Second
)

// effectiveTTL calculates the effective TTL based on access frequency.
// Formula: baseTTL × min(1.0 + accessCount × 0.5, 20.0)
func effectiveTTL(baseTTL time.Duration, accessCount uint64) time.Duration {
	multiplier := 1.0 + float64(accessCount)*freqGrowthRate
	if multiplier > freqMultiplierMax {
		multiplier = freqMultiplierMax
	}
	return time.Duration(float64(baseTTL) * multiplier)
}

// ──── ChildrenFetcher ────

// ChildrenFetcher queries a folder's children from the Graph API.
// It's injected to decouple InodeCache from graph.Client and facilitate testing.
type ChildrenFetcher func(ctx context.Context, parentID string) ([]graph.DriveItem, error)

// ──── InodeCache ────

// InodeCache is the global metadata cache. Stores *Inode in a sync.Map
// indexed by item ID. Each Inode knows its children via []string IDs.
//
// Faithful to onedriver's design (sync.Map + Inode tree), with this difference:
//   - BoltDB added in Phase 3 for persistence across restarts
//   - Background TTL+LFU eviction (Phase 4)
//   - ChildrenFetcher injected to decouple from graph.Client
//
// The key pattern is children nil = not initialized:
//   - nil  → never fetched → GetChildren calls the fetcher
//   - []string{} → fetched and empty → GetChildren returns empty without HTTP
//   - []string{"id1","id2"} → fetched with children → O(1) lookup
type InodeCache struct {
	inodes sync.Map // id (string) → *Inode
	rootID string

	// Counters for Stats (UI)
	hits      atomic.Uint64
	misses    atomic.Uint64
	evictions atomic.Uint64 // Phase 4: counter of evicted children

	// ──── Phase 3: BoltDB + offline mode ────
	db      *bolt.DB // nil if not initialized with BoltDB
	dbPath  string
	offline atomic.Bool

	// ──── Phase 4: TTL+LFU eviction ────
	maxEntries int           // max folders with cached children
	baseTTL    time.Duration // base TTL before considering stale

	// Background sweep lifecycle
	sweepMu sync.Mutex    // serializes sweeps (only one at a time)
	stopCh  chan struct{} // close to stop the sweep goroutine
	wg      sync.WaitGroup

	// closeMu serializes Close() to make it safe to call from multiple
	// goroutines (defer from Mount + signal handler on unmount). Without this,
	// two concurrent Close() calls could close c.db twice (nil panic)
	// or close stopCh twice (panic on closed channel).
	closeMu sync.Mutex

	// ──── Issue #67: dirty inode tracking ────
	// dirtyMu guards the dirty/deleted sets. A plain map + mutex is used
	// instead of a sync.Map so SerializeDirty can atomically SWAP the sets
	// out: mutations that happen during serialization are captured in the
	// fresh sets and can never be lost by a delete-after-commit race.
	dirtyMu sync.Mutex
	dirty   map[string]struct{} // id → struct{}: persisted state changed in memory
	deleted map[string]struct{} // id → struct{}: removed from memory; disk entry must go

	// serializedBytes counts the bytes of inode JSON written to BoltDB
	// (observability + SerializeAll vs SerializeDirty benchmarks).
	serializedBytes atomic.Uint64
	// now returns the current time.
	// Used to identify the time of the last access for TTL+LFU eviction. Injected for testing (time.Now).
	now func() time.Time // injected for testing (time.Now)
}

// NewInodeCache creates a new empty inode cache with default values.
func NewInodeCache() *InodeCache {
	return &InodeCache{
		rootID:     "root",
		maxEntries: 2000,
		baseTTL:    60 * time.Second,
		dirty:      make(map[string]struct{}),
		deleted:    make(map[string]struct{}),
		now:        time.Now,
	}
}

// ──── Parent/children bookkeeping helpers ────
//
// Every mutation of a folder's children list follows one of two shapes, so
// both live here instead of being copy-pasted in Insert/InsertChild/Delete/
// RemoveChild/MoveChild.

// attachChild adds childID to the parent's children list (no duplicates) and
// bumps its subdir counter when the child is a directory.
//
// children == nil means the folder has not been listed yet: seedNilChildren
// decides whether to seed the list with childID or leave it nil. Leaving it
// nil matters for InsertChild, where a one-item list would make GetChildren
// believe it already holds the full listing and never call the fetcher.
func attachChild(parent *Inode, childID string, childIsDir, seedNilChildren bool) {
	parent.Lock()
	defer parent.Unlock()

	if parent.children != nil || seedNilChildren {
		if !containsID(parent.children, childID) {
			parent.children = append(parent.children, childID)
		}
	}
	if childIsDir {
		parent.subdir++
	}
}

// detachChild removes childID from the parent's children list (in place) and
// decrements its subdir counter when the child is a directory.
func detachChild(parent *Inode, childID string, childIsDir bool) {
	parent.Lock()
	defer parent.Unlock()

	filtered := parent.children[:0]
	for _, id := range parent.children {
		if id != childID {
			filtered = append(filtered, id)
		}
	}
	parent.children = filtered
	if childIsDir && parent.subdir > 0 {
		parent.subdir--
	}
}

// containsID reports whether ids already holds id.
func containsID(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

// ──── Basic operations ────

// Get obtains an inode by ID. Returns nil if it doesn't exist.
func (c *InodeCache) Get(id string) *Inode {
	if val, ok := c.inodes.Load(id); ok {
		inode, _ := val.(*Inode)
		return inode
	}
	return nil
}

// Insert adds or replaces an inode in the cache. If the inode has a ParentID,
// updates the parent's subdir and children automatically.
func (c *InodeCache) Insert(inode *Inode) {
	if inode == nil {
		return
	}
	c.inodes.Store(inode.ID(), inode)
	c.markDirty(inode.ID())

	// Update parent: add this ID to its children and increment subdir
	parentID := inode.ParentID()
	if parentID != "" {
		if parent := c.Get(parentID); parent != nil {
			attachChild(parent, inode.ID(), inode.IsDir(), true)
			c.markDirty(parentID)
		}
	}
}

// Delete removes an inode from the cache and from the parent's children.
func (c *InodeCache) Delete(id string) {
	inode := c.Get(id)
	if inode == nil {
		return
	}

	// Remove from parent
	parentID := inode.ParentID()
	if parentID != "" {
		if parent := c.Get(parentID); parent != nil {
			detachChild(parent, id, inode.IsDir())
		}
	}

	c.inodes.Delete(id)
	c.markDeleted(id)
	if parentID != "" {
		c.markDirty(parentID)
	}
}

// ──── Children operations ────

// GetChildren gets a folder's children. If the children are already cached
// (non-nil) AND fresh (within the effective TTL), it returns them without
// calling the fetcher. If they are nil or stale, it invokes the fetcher,
// populates the cache, and creates Inodes for each child.
//
// Returns a name → *Inode map for O(1) lookups by name (compatible
// with the current DriveItemNode/OneCloudFS API).
func (c *InodeCache) GetChildren(
	ctx context.Context,
	parentID string,
	fetch ChildrenFetcher,
) (map[string]*Inode, error) {
	return c.getChildren(ctx, parentID, fetch, true)
}

// PrefetchChildren populates the cache exactly like GetChildren, but does not
// mark the fetched inodes dirty. Bulk warm-ups (preWarm) use it so that
// fetching a large subtree does not force SerializeDirty to rewrite the whole
// tree on the next delta poll; the tree is still persisted on unmount by
// SerializeAll. Cache hit/miss counters and eviction tracking behave exactly
// like GetChildren.
func (c *InodeCache) PrefetchChildren(
	ctx context.Context,
	parentID string,
	fetch ChildrenFetcher,
) (map[string]*Inode, error) {
	return c.getChildren(ctx, parentID, fetch, false)
}

// getChildren implements the shared children-fetching logic. When markDirty is
// false the fetched inodes are stored in memory but not flagged for persistence
// (see PrefetchChildren).
func (c *InodeCache) getChildren(
	ctx context.Context,
	parentID string,
	fetch ChildrenFetcher,
	markDirty bool,
) (map[string]*Inode, error) {
	// 1. Look in cache
	parent := c.Get(parentID)
	if parent != nil && parent.IsChildrenFetched() && c.isChildrenFresh(parent) {
		c.hits.Add(1)
		parent.BumpChildrenAccess() // Phase 4: frequency tracking for eviction
		log.Trace().Str("parentID", parentID).Msg("InodeCache hit")
		return c.buildChildMap(parent.Children()), nil
	}

	c.misses.Add(1)
	if parent != nil && parent.IsChildrenFetched() {
		// Children present but stale (exceeded effective TTL): refetch.
		log.Debug().
			Str("parentID", parentID).
			Dur("age", time.Since(parent.ChildrenCachedAt())).
			Msg("InodeCache children stale, refetching from Graph")
	}
	log.Trace().Str("parentID", parentID).Msg("InodeCache miss, querying fetcher...")

	// 2. If there's no fetcher, we can't query the API — return error
	if fetch == nil {
		return nil, fmt.Errorf("cache miss for %q and no fetcher available (test without mock?)", parentID)
	}

	// 3. Call the fetcher (Graph API)
	items, err := fetch(ctx, parentID)
	if err != nil {
		return nil, err
	}

	// 3. Create/populate Inodes for each child
	childIDs := make([]string, 0, len(items))
	childMap := make(map[string]*Inode, len(items))

	for i := range items {
		item := &items[i]
		inode := NewInodeDriveItem(item)
		// 🔧 Assign ParentID if Graph didn't return it. ListChildren does NOT include
		// parentReference (only returned with $expand), so without this the
		// child inodes became orphans (ParentID=""). Consequences:
		//   1. ItemsByParent (offline fallback) couldn't rebuild the list of
		//      a folder evicted by the TTL sweep → "Error in Lookup" offline.
		//   2. SerializeAll didn't persist those inodes (only persists those with
		//      ParentID) → the subtree didn't survive the round-trip.
		if inode.ParentID() == "" {
			inode.Lock()
			inode.DriveItem.Parent = &graph.DriveItemParent{ID: parentID}
			inode.Unlock()
		}
		c.inodes.Store(inode.ID(), inode)
		if markDirty {
			c.markDirty(inode.ID())
		}
		childIDs = append(childIDs, inode.ID())
		childMap[inode.Name()] = inode
	}

	// 4. Update parent
	if parent == nil {
		// Create a minimal Inode for the parent if it doesn't exist
		parent = &Inode{DriveItem: graph.DriveItem{ID: parentID, Name: parentID, Folder: &graph.Folder{}}}
		c.inodes.Store(parentID, parent)
	}
	parent.SetChildren(childIDs)
	if markDirty {
		c.markDirty(parentID)
	}

	// Calculate subdir
	var subdir uint32
	for _, inode := range childMap {
		if inode.IsDir() {
			subdir++
		}
	}
	parent.SetSubdir(subdir)

	return childMap, nil
}

// GetChild searches for a child by name within a folder.
func (c *InodeCache) GetChild(ctx context.Context, parentID, name string, fetch ChildrenFetcher) (*Inode, error) {
	children, err := c.GetChildren(ctx, parentID, fetch)
	if err != nil {
		return nil, err
	}
	return children[name], nil
}

// isChildrenFresh checks whether an inode's cached children have not yet
// exceeded their effective TTL (baseTTL × frequency multiplier).
//
// Based on childrenCachedAt (when they were populated from Graph), NOT on
// childrenLastAccess: lastAccess is updated on every cache hit, so an
// actively browsed directory would never expire — and data restored from a
// previous session would be served indefinitely (the "only shows cached
// files" bug).
//
// On mount, DeserializeFromDisk restores inodes with children from the
// previous session with an old childrenCachedAt → isChildrenFresh returns
// false → GetChildren refetches from Graph → the user sees the real account
// content. In offline mode, the fetchChildren fallback serves the cache anyway.
func (c *InodeCache) isChildrenFresh(inode *Inode) bool {
	ttl := effectiveTTL(c.baseTTL, inode.ChildrenAccessCount())
	return time.Since(inode.ChildrenCachedAt()) < ttl
}

// ItemsByParent returns the DriveItems of all inodes in memory whose
// ParentID matches parentID. Used as a fallback in offline mode when
// the parent's children list was evicted by the TTL sweep (children=nil)
// but the child inodes remain in the sync.Map with their parent reference.
func (c *InodeCache) ItemsByParent(parentID string) []graph.DriveItem {
	var items []graph.DriveItem
	c.inodes.Range(func(_, value interface{}) bool {
		inode, _ := value.(*Inode)
		if inode.ParentID() == parentID {
			inode.RLock()
			items = append(items, inode.DriveItem)
			inode.RUnlock()
		}
		return true
	})
	return items
}

// buildChildMap builds a name→*Inode map from the children IDs.
func (c *InodeCache) buildChildMap(ids []string) map[string]*Inode {
	result := make(map[string]*Inode, len(ids))
	for _, id := range ids {
		if child := c.Get(id); child != nil {
			result[child.Name()] = child
		}
	}
	return result
}

// ──── Tree navigation ────

// ErrIsRoot is returned when GetPath receives the root path "/".
// The root has no Inode in the cache — OneCloudFS represents it directly.
var ErrIsRoot = fmt.Errorf("the root has no representation in InodeCache")

// GetPath navigates the inode tree from the root following a path separated
// by "/" and returns the Inode at the leaf. Useful for resolving full paths
// (e.g. CLI or offline mode) without querying Graph.
//
// The path "/" returns ErrIsRoot (the root is not an Inode in cache, OneCloudFS
// represents it directly). Paths like "a/b/c" are navigated component by
// component.
func (c *InodeCache) GetPath(ctx context.Context, path string, fetch ChildrenFetcher) (*Inode, error) {
	// Clean the path: remove leading/trailing "/", ignore empty/root
	path = strings.Trim(path, "/")
	if path == "" || path == "." {
		return nil, ErrIsRoot // the root has no Inode
	}

	components := strings.Split(path, "/")
	currentID := "root"

	for i, name := range components {
		if name == "" || name == "." {
			continue
		}
		children, err := c.GetChildren(ctx, currentID, fetch)
		if err != nil {
			return nil, fmt.Errorf("navigating %q in %q: %w", name, currentID, err)
		}
		child, exists := children[name]
		if !exists {
			return nil, fmt.Errorf("%w: %q not found in %q", syscall.ENOENT, name, currentID)
		}
		// Is it the last component?
		if i == len(components)-1 {
			return child, nil
		}
		// Keep navigating — it must be a folder
		if !child.IsDir() {
			return nil, fmt.Errorf("%w: %q is not a folder", syscall.ENOTDIR, name)
		}
		currentID = child.ID()
	}

	return nil, fmt.Errorf("%w: empty path after cleaning", syscall.ENOENT)
}

// ──── TTL+LFU eviction ────

// StartSweep starts the background eviction goroutine.
// Must be called once, after creating the cache.
func (c *InodeCache) StartSweep() {
	if c.stopCh != nil {
		return // already started
	}
	c.stopCh = make(chan struct{})
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(sweepInterval)
		defer ticker.Stop()
		log.Trace().Dur("interval", sweepInterval).Msg("InodeCache: sweep started")
		for {
			select {
			case <-c.stopCh:
				log.Trace().Msg("InodeCache: sweep stopped")
				return
			case <-ticker.C:
				c.sweep()
			}
		}
	}()
}

// sweep runs a full eviction pass: first TTL, then size.
func (c *InodeCache) sweep() {
	c.sweepMu.Lock()
	defer c.sweepMu.Unlock()

	c.evictExpiredChildren()     // Tier 1: TTL with frequency decay
	c.evictChildrenBySizeLimit() // Tier 2: size limit by score
}

// ForceSweep runs an immediate sweep (useful for tests and UI).
// Does not wait for the next timer tick.
func (c *InodeCache) ForceSweep() {
	c.sweep()
}

// evictExpiredChildren iterates over inodes with cached children, applies
// decay to the accessCount, and evicts those whose effective TTL has expired.
func (c *InodeCache) evictExpiredChildren() {
	now := c.currentTime()

	c.inodes.Range(func(_, value interface{}) bool {
		inode, _ := value.(*Inode)
		if !inode.IsChildrenFetched() {
			return true // no cached children, nothing to evict
		}

		// Apply decay to the access counter
		inode.DecayChildrenAccess()

		// Calculate whether the effective TTL has expired
		accessCount := inode.ChildrenAccessCount()
		ttl := effectiveTTL(c.baseTTL, accessCount)
		expiry := inode.ChildrenLastAccess().Add(ttl)

		if now.After(expiry) {
			log.Debug().
				Str("id", inode.ID()).
				Str("name", inode.Name()).
				Uint64("accessCount", accessCount).
				Dur("ttl", ttl).
				Msg("Evicting children due to expired TTL")
			inode.EvictChildren()
			c.evictions.Add(1)
		}
		return true
	})
}

// evictChildrenBySizeLimit evicts the children of the folders with the lowest
// score until the number of folders with cached children returns to
// below maxEntries.
//
// Score = accessCount / (minutosDesdeLastAccess + 1)
// Tiebreaker: the oldest childrenCachedAt is evicted first.
func (c *InodeCache) evictChildrenBySizeLimit() {
	if c.maxEntries <= 0 {
		return
	}

	type scoredEntry struct {
		id    string
		score float64
		age   time.Time // childrenCachedAt: oldest = tiebreaker
	}

	now := c.currentTime()
	var scores []scoredEntry
	count := 0

	c.inodes.Range(func(key, value interface{}) bool {
		inode, _ := value.(*Inode)
		if !inode.IsChildrenFetched() {
			return true
		}
		count++
		accessCount := inode.ChildrenAccessCount()
		minutesSinceLastAccess := now.Sub(inode.ChildrenLastAccess()).Minutes()
		score := float64(accessCount) / (minutesSinceLastAccess + 1)
		id, _ := key.(string)
		scores = append(scores, scoredEntry{
			id:    id,
			score: score,
			age:   inode.ChildrenCachedAt(),
		})
		return true
	})

	if count <= c.maxEntries {
		return
	}

	// Ordenar por score ascendente (menor score = menos valioso)
	// Tie: the oldest (smaller age) is evicted first
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].score == scores[j].score {
			return scores[i].age.Before(scores[j].age)
		}
		return scores[i].score < scores[j].score
	})

	toRemove := count - c.maxEntries
	for i := 0; i < toRemove && i < len(scores); i++ {
		if inode := c.Get(scores[i].id); inode != nil {
			log.Debug().
				Str("id", inode.ID()).
				Str("name", inode.Name()).
				Float64("score", scores[i].score).
				Msg("Evicting children due to size limit")
			inode.EvictChildren()
			c.evictions.Add(1)
		}
	}
}

// ──── Invalidation ────

// Invalidate marks a folder's children as uninitialized,
// forcing a refetch on the next GetChildren. Does not delete individual
// Inodes (they remain part of the tree).
//
// Useful after creating/deleting/moving files in a folder.
func (c *InodeCache) Invalidate(parentID string) {
	if parent := c.Get(parentID); parent != nil {
		parent.SetChildren(nil)
		c.markDirty(parentID)
		log.Debug().Str("parentID", parentID).Msg("InodeCache invalidated (children → nil)")
	}
}

// InvalidateAll resets ALL children of the cache, forcing a complete refetch
// on the next accesses. Useful for "Force refresh" in the UI.
func (c *InodeCache) InvalidateAll() {
	c.inodes.Range(func(_, value interface{}) bool {
		inode, _ := value.(*Inode)
		if inode.HasChildren() {
			inode.SetChildren(nil)
			c.markDirty(inode.ID())
		}
		return true
	})
	log.Info().Msg("InodeCache invalidated completely")
}

// ──── Observability API (for UI) ────

// InodeCacheStats is the public snapshot of the cache state.
type InodeCacheStats struct {
	InodeCount int    `json:"inode_count"`
	Hits       uint64 `json:"hits"`
	Misses     uint64 `json:"misses"`
	Evictions  uint64 `json:"evictions"`
}

// Stats returns a copy of the statistics for the UI.
func (c *InodeCache) Stats() InodeCacheStats {
	count := 0
	c.inodes.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return InodeCacheStats{
		InodeCount: count,
		Hits:       c.hits.Load(),
		Misses:     c.misses.Load(),
		Evictions:  c.evictions.Load(),
	}
}

// ──── Lifecycle ────

// Insert adds a child inode to a specific parent. Useful for Mkdir/Create
// where the inode was just created and we already know parent and child.
func (c *InodeCache) InsertChild(parentID, _ string, childInode *Inode) {
	if childInode == nil {
		return
	}
	c.inodes.Store(childInode.ID(), childInode)

	// Set the parent reference on the child (so MoveID can
	// update parent.children after the local → remote ID swap)
	childInode.Lock()
	childInode.DriveItem.Parent = &graph.DriveItemParent{ID: parentID}
	childInode.Unlock()

	// Update parent. seedNilChildren is false: if children is nil the folder
	// hasn't been fully listed yet (or was evicted), and a one-item list would
	// make GetChildren think it already has all children and never call the
	// fetcher, losing the rest. The child inode stays stored in the sync.Map
	// (accessible via Get by ID) and the next GetChildren brings ALL children
	// from Graph, including this one.
	if parent := c.Get(parentID); parent != nil {
		attachChild(parent, childInode.ID(), childInode.IsDir(), false)
		c.markDirty(parentID)
	}
	c.markDirty(childInode.ID())

	// NOTE: We don't invalidate the parent's children. The list is already
	// updated directly (the new child was added). Invalidating here
	// would break subsequent Unlink/Rmdir because the next GetChildren
	// would refetch from the API and might not find the newly created item.
}

// RemoveChild removes a child from a parent. Useful for Rmdir/Unlink.
func (c *InodeCache) RemoveChild(parentID, childID string) {
	inode := c.Get(childID)
	if inode == nil {
		return
	}

	// Remove from parent
	if parent := c.Get(parentID); parent != nil {
		detachChild(parent, childID, inode.IsDir())
	}

	c.inodes.Delete(childID)
	c.markDeleted(childID)
	if parentID != "" {
		c.markDirty(parentID)
	}

	// NOTE: We don't invalidate the parent's children. The list is already
	// updated directly (the child was removed).
}

// MoveID moves an inode from an old key to a new one. Used when a local
// item receives a real OneDrive ID after the first upload.
//
// 🔧 In addition to re-keying in the sync.Map, updates DriveItem.ID to the new ID:
// before, the inode kept the local ID in its internal field, so
// ItemsByParent (offline fallback) and any code reading inode.ID()
// kept seeing the old ID → inconsistency after the local→remote swap
// (bug destapado por TestInodeCache_GetChildren_ParentID_Then_MoveID).
func (c *InodeCache) MoveID(oldID, newID string) {
	val, ok := c.inodes.Load(oldID)
	if !ok {
		return
	}
	inode, _ := val.(*Inode)

	// Update the internal ID of the inode (thread-safe)
	inode.Lock()
	inode.DriveItem.ID = newID
	inode.Unlock()

	c.inodes.Store(newID, inode)
	c.inodes.Delete(oldID)
	if oldID != newID {
		c.markDeleted(oldID)
	}
	c.markDirty(newID)

	// Update the parent's children
	parentID := inode.ParentID()
	if parentID != "" {
		if parent := c.Get(parentID); parent != nil {
			parent.Lock()
			for i, id := range parent.children {
				if id == oldID {
					parent.children[i] = newID
					break
				}
			}
			parent.Unlock()
			c.markDirty(parentID)
		}
	}
}

// MoveChild moves a child from one parent folder to another. Useful for rename/move.
func (c *InodeCache) MoveChild(oldParentID, newParentID, childID string) {
	child := c.Get(childID)
	if child == nil {
		return
	}

	// Remove from the old parent
	if oldParent := c.Get(oldParentID); oldParent != nil {
		detachChild(oldParent, childID, child.IsDir())
	}

	// Add to new parent
	if newParent := c.Get(newParentID); newParent != nil {
		attachChild(newParent, childID, child.IsDir(), true)
		c.markDirty(newParentID)
	}
	c.markDirty(childID)
}

// ──── BoltDB persistence ────

// markDirty records that an inode's persisted state changed in memory and
// must be rewritten to BoltDB by the next SerializeDirty. Re-inserting an id
// also clears any pending tombstone for it (the inode is alive again).
func (c *InodeCache) markDirty(id string) {
	c.dirtyMu.Lock()
	c.dirty = markExclusive(c.dirty, c.deleted, id)
	c.dirtyMu.Unlock()
}

// markDeleted records that an inode was removed from memory; its stale entry
// in BoltDB must be deleted by the next SerializeDirty (tombstone).
func (c *InodeCache) markDeleted(id string) {
	c.dirtyMu.Lock()
	c.deleted = markExclusive(c.deleted, c.dirty, id)
	c.dirtyMu.Unlock()
}

// markExclusive adds id to set (creating it when nil) and drops it from other,
// keeping the dirty and deleted sets mutually exclusive. Returns the set so
// the caller can store a freshly created map. Callers hold dirtyMu.
func markExclusive(set, other map[string]struct{}, id string) map[string]struct{} {
	if set == nil {
		set = make(map[string]struct{})
	}
	set[id] = struct{}{}
	delete(other, id)
	return set
}

// sortedKeys returns the keys of a set in sorted order, giving SerializeDirty
// a deterministic batching order.
func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// boltBucketMetadata, boltBucketDelta and boltBucketUploads are the names
// of the buckets in the DB.
var (
	boltBucketMetadata = []byte("metadata") // id → JSON de Inode
	boltBucketDelta    = []byte("delta")    // "link" → deltaURL
	boltBucketUploads  = []byte("uploads")  // id → JSON de UploadSession
)

// boltOpenTimeout is how long InitBoltDB waits to acquire the exclusive file
// lock on the BoltDB file before reporting failure. A real (non-zero) timeout
// is required: with Timeout: 0 bbolt blocks forever, and a sub-millisecond
// value fails instantly even when the lock is only briefly contended (e.g.
// right after a crash). 5s absorbs transient contention while keeping a
// double mount fast to fail.
const boltOpenTimeout = 5 * time.Second

// InitBoltDB abre (o crea) la base de datos BoltDB y carga los datos existentes.
// Must be called once, before using the cache.
func (c *InodeCache) InitBoltDB(dbPath string) error {
	return c.initBoltDB(dbPath, boltOpenTimeout)
}

// initBoltDB opens (or creates) the BoltDB database with the given lock
// acquisition timeout. Split from InitBoltDB so tests can exercise the
// double-mount path without waiting boltOpenTimeout.
func (c *InodeCache) initBoltDB(dbPath string, timeout time.Duration) error {
	// NoSync defaults to false: fsync on every Commit. Durability over
	// throughput — SerializeAll/SerializeDirty run only on delta poll and
	// unmount, so fsync cost is negligible vs. the network cost of Graph.
	db, err := bolt.Open(dbPath, 0600, &bolt.Options{Timeout: timeout})
	if err != nil {
		if errors.Is(err, boltErrors.ErrTimeout) {
			return fmt.Errorf("BoltDB at %s is locked by another running instance (double mount?): only one mount can use a cache directory at a time; close the other instance or use a different --cache-dir: %w", dbPath, err)
		}
		return fmt.Errorf("error opening BoltDB at %s: %w", dbPath, err)
	}

	// Create buckets if they don't exist
	if err := db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(boltBucketMetadata); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(boltBucketDelta); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(boltBucketUploads); err != nil {
			return err
		}
		return nil
	}); err != nil {
		db.Close()
		return fmt.Errorf("error creando buckets en BoltDB: %w", err)
	}

	c.db = db
	c.dbPath = dbPath

	// Load existing data from BoltDB into memory
	if err := c.DeserializeFromDisk(); err != nil {
		log.Warn().Err(err).Msg("Error loading data from BoltDB — cache starts empty")
	}

	log.Info().Str("path", dbPath).Msg("BoltDB initialized successfully")
	return nil
}

// SerializeAll persiste todos los inodos en memoria a BoltDB.
//
// Persists the complete browsed tree:
//   - Folders whose children were already fetched (IsChildrenFetched)
//   - ALL inodes with ParentID (files and subfolders of the tree), even if
//     their parent's children list was evicted by the TTL sweep
//   - Child inodes referenced by fetched folders
//
// 🔧 Without persisting inodes with ParentID, a subtree whose folder was
// evicted by TTL (children→nil) didn't survive the round-trip: when
// remounting offline, the ParentID fallback (ItemsByParent) couldn't find
// the inodes and the subfolders appeared empty (bug detected in the real
// offline test).
func (c *InodeCache) SerializeAll() error {
	if c.db == nil {
		return nil // No BoltDB, nothing to persist
	}

	err := c.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(boltBucketMetadata)
		if bucket == nil {
			return fmt.Errorf("metadata bucket not found")
		}

		// Pass 1: fetched folders + inodes with ParentID (browsed tree) +
		// gather the children of the fetched folders.
		toPersist := make(map[string]bool)
		var childIDs []string

		c.inodes.Range(func(_, value interface{}) bool {
			inode, _ := value.(*Inode)
			if !inode.IsChildrenFetched() {
				// Inode without children: only persisted if it belongs to the tree
				// (has ParentID). Orphans (without parent) are not persisted.
				if inode.ParentID() != "" {
					toPersist[inode.ID()] = true
				}
				return true
			}
			toPersist[inode.ID()] = true
			childIDs = append(childIDs, inode.Children()...)
			return true
		})

		// Pass 2: inodes referenced as children are also persisted
		// (in case a child lacks ParentID for any reason).
		for _, id := range childIDs {
			if inode := c.Get(id); inode != nil {
				toPersist[id] = true
			}
		}

		// Pass 3: write
		for id := range toPersist {
			if inode := c.Get(id); inode != nil {
				data := inode.AsJSON()
				if err := bucket.Put([]byte(id), data); err != nil {
					log.Warn().Err(err).Str("id", id).Msg("Error persisting inode")
					continue
				}
				c.serializedBytes.Add(uint64(len(data)))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// A full serialization persists everything: the dirty tracking sets are
	// now stale, so drop them.
	c.dirtyMu.Lock()
	c.dirty = make(map[string]struct{})
	c.deleted = make(map[string]struct{})
	c.dirtyMu.Unlock()

	return nil
}

// serializeDirtyBatchSize bounds the size of each SerializeDirty transaction:
// ~500 inodes per bolt.Tx instead of the whole tree in one giant transaction.
const serializeDirtyBatchSize = 500

// SerializeDirty persiste solo los inodos sucios (marcados por markDirty desde
// la última serialización) a BoltDB, en batches de serializeDirtyBatchSize
// inodos por transacción. Los inodos eliminados de memoria (tombstones) se
// borran también de BoltDB para que el estado en disco converja con memoria.
//
// A diferencia de SerializeAll (árbol completo en una sola transacción), el
// coste de escritura por delta sync crece con el número de cambios, no con el
// tamaño del árbol (issue #67). La garantía final de durabilidad sigue siendo
// el SerializeAll de unmount.
func (c *InodeCache) SerializeDirty() error {
	if c.db == nil {
		return nil // No BoltDB, nothing to persist
	}

	// Atomically swap out the dirty sets: any mutation during serialization is
	// captured in the fresh sets, so a mutation can never be lost by the
	// flag-clearing of this pass (a sync.Map delete-after-commit would race).
	c.dirtyMu.Lock()
	dirty := c.dirty
	deleted := c.deleted
	c.dirty = make(map[string]struct{})
	c.deleted = make(map[string]struct{})
	c.dirtyMu.Unlock()

	if len(dirty) == 0 && len(deleted) == 0 {
		return nil
	}

	dirtyIDs := sortedKeys(dirty)
	deletedIDs := sortedKeys(deleted)
	log.Debug().
		Int("dirty", len(dirtyIDs)).
		Int("deleted", len(deletedIDs)).
		Msg("SerializeDirty: persisting dirty inodes")

	var firstErr error

	// Persist dirty inodes in batches, one transaction per batch.
	for i := 0; i < len(dirtyIDs); i += serializeDirtyBatchSize {
		end := i + serializeDirtyBatchSize
		if end > len(dirtyIDs) {
			end = len(dirtyIDs)
		}
		batch := dirtyIDs[i:end]
		if err := c.db.Update(func(tx *bolt.Tx) error {
			bucket := tx.Bucket(boltBucketMetadata)
			if bucket == nil {
				return fmt.Errorf("metadata bucket not found")
			}
			for _, id := range batch {
				inode := c.Get(id)
				if inode == nil {
					// Removed since it was marked dirty (or moved to a new ID):
					// drop the stale disk entry.
					if err := bucket.Delete([]byte(id)); err != nil {
						return err
					}
					continue
				}
				// Same persistence rule as SerializeAll: only inodes of the
				// browsed tree (fetched folders or inodes with a parent) are
				// written; orphans are skipped, never written to the DB.
				if !inode.IsChildrenFetched() && inode.ParentID() == "" {
					continue
				}
				data := inode.AsJSON()
				if err := bucket.Put([]byte(id), data); err != nil {
					return err
				}
				c.serializedBytes.Add(uint64(len(data)))
			}
			return nil
		}); err != nil {
			// Re-mark so a later SerializeDirty retries this batch.
			for _, id := range batch {
				c.markDirty(id)
			}
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	// Tombstones: remove from disk the inodes deleted from memory.
	for i := 0; i < len(deletedIDs); i += serializeDirtyBatchSize {
		end := i + serializeDirtyBatchSize
		if end > len(deletedIDs) {
			end = len(deletedIDs)
		}
		batch := deletedIDs[i:end]
		if err := c.db.Update(func(tx *bolt.Tx) error {
			bucket := tx.Bucket(boltBucketMetadata)
			if bucket == nil {
				return fmt.Errorf("metadata bucket not found")
			}
			for _, id := range batch {
				if err := bucket.Delete([]byte(id)); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			for _, id := range batch {
				c.markDeleted(id)
			}
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	if firstErr != nil {
		log.Warn().Err(firstErr).Msg("SerializeDirty: some batches failed, will retry on next call")
	}
	return firstErr
}

// DeserializeFromDisk carga inodos desde BoltDB a sync.Map.
func (c *InodeCache) DeserializeFromDisk() error {
	if c.db == nil {
		return nil
	}

	count := 0
	err := c.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(boltBucketMetadata)
		if bucket == nil {
			return nil // Bucket doesn't exist yet (first run)
		}

		return bucket.ForEach(func(k, v []byte) error {
			inode, err := NewInodeJSON(v)
			if err != nil {
				log.Warn().Err(err).Str("id", string(k)).Msg("Error deserializing inode, skipping")
				return nil // Continue with the next one
			}
			// Only load if it does not already exist in memory (memory wins)
			if _, loaded := c.inodes.LoadOrStore(string(k), inode); !loaded {
				count++
			}
			return nil
		})
	})
	if err != nil {
		return err
	}

	if count > 0 {
		log.Info().Int("count", count).Msg("Inodes loaded from BoltDB")
	}
	return nil
}

// GetDeltaLink returns the stored delta link URL. A read failure returns an
// empty link (the caller then starts a full delta cycle), and is logged: a
// silently dropped link makes the next sync re-enumerate the whole drive with
// no explanation.
func (c *InodeCache) GetDeltaLink() string {
	if c.db == nil {
		return ""
	}

	var link string
	err := c.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(boltBucketDelta)
		if bucket == nil {
			return errBucketMissing(boltBucketDelta)
		}
		if data := bucket.Get([]byte("link")); data != nil {
			link = string(data)
		}
		return nil
	})
	if err != nil {
		log.Warn().Err(err).Msg("InodeCache: error reading the delta link from BoltDB")
	}
	return link
}

// SetDeltaLink stores the delta link URL in BoltDB. The delta loop cannot act
// on a persistence failure (it keeps the link in memory for this run), but an
// unpersisted link silently costs a full re-sync after the next restart, so
// the failure is logged.
func (c *InodeCache) SetDeltaLink(link string) {
	if c.db == nil {
		return
	}

	err := c.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(boltBucketDelta)
		if bucket == nil {
			return errBucketMissing(boltBucketDelta)
		}
		return bucket.Put([]byte("link"), []byte(link))
	})
	if err != nil {
		log.Warn().Err(err).Msg("InodeCache: error persisting the delta link to BoltDB")
	}
}

// errBucketMissing describes a BoltDB bucket that InitBoltDB should have
// created. Reaching it means the database was not initialized (or was
// initialized partially), which is why it is reported rather than treated as
// an empty result.
func errBucketMissing(bucket []byte) error {
	return fmt.Errorf("bucket %q does not exist", bucket)
}

// ──── UploadSession persistence (Phase 5b) ────

// SaveUploadSession persists an UploadSession to BoltDB so it survives
// across restarts. The caller must serialize the session to JSON beforehand.
// A persistence failure only costs the session on the next restart (the
// upload itself proceeds from memory), so it is logged rather than
// propagated.
func (c *InodeCache) SaveUploadSession(id string, data []byte) {
	if c.db == nil {
		return
	}
	err := c.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(boltBucketUploads)
		if bucket == nil {
			return errBucketMissing(boltBucketUploads)
		}
		return bucket.Put([]byte(id), data)
	})
	if err != nil {
		log.Warn().Err(err).Str("id", id).Msg("InodeCache: error persisting upload session to BoltDB")
	}
}

// LoadUploadSessions loads all incomplete upload sessions from
// BoltDB and returns them as an id → JSON map. Used at startup to
// restore sessions that did not finish (abrupt shutdown, crash, etc.).
func (c *InodeCache) LoadUploadSessions() map[string][]byte {
	if c.db == nil {
		return nil
	}
	result := make(map[string][]byte)
	err := c.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(boltBucketUploads)
		if bucket == nil {
			return errBucketMissing(boltBucketUploads)
		}
		return bucket.ForEach(func(k, v []byte) error {
			result[string(k)] = v
			return nil
		})
	})
	if err != nil {
		// Reported because an unreadable bucket is indistinguishable from
		// "no pending uploads": the sessions would be dropped in silence.
		log.Warn().Err(err).Msg("InodeCache: error loading upload sessions from BoltDB")
	}
	return result
}

// DeleteUploadSession removes an UploadSession from BoltDB.
// A failure leaves a stale session that would be restored (and cancelled) on
// the next start, so it is logged.
func (c *InodeCache) DeleteUploadSession(id string) {
	if c.db == nil {
		return
	}
	err := c.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(boltBucketUploads)
		if bucket == nil {
			return errBucketMissing(boltBucketUploads)
		}
		return bucket.Delete([]byte(id))
	})
	if err != nil {
		log.Warn().Err(err).Str("id", id).Msg("InodeCache: error removing upload session from BoltDB")
	}
}

// ──── Offline mode ────

// IsOffline returns true if the cache is in offline mode.
func (c *InodeCache) IsOffline() bool {
	return c.offline.Load()
}

// SetOffline enables/disables offline mode.
func (c *InodeCache) SetOffline(v bool) {
	prev := c.offline.Swap(v)
	if prev != v {
		if v {
			log.Warn().Msg("🔴 Entering offline mode — serving from local cache")
		} else {
			log.Info().Msg("🟢 Connection restored — leaving offline mode")
		}
	}
}

// Close closes BoltDB and stops the sweep goroutine. Safe to call
// multiple times and from several concurrent goroutines (Mount's defer + the
// unmount signal handler), thanks to closeMu.
func (c *InodeCache) Close() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()

	// Stop sweep (idempotent: the first call sets stopCh to nil)
	if c.stopCh != nil {
		close(c.stopCh)
		c.wg.Wait()
		c.stopCh = nil
	}

	// Close BoltDB (idempotent: the first call sets c.db to nil)
	if c.db != nil {
		if err := c.SerializeAll(); err != nil {
			log.Warn().Err(err).Msg("Error serializing cache to BoltDB during Close")
		}
		if err := c.db.Close(); err != nil {
			return fmt.Errorf("error cerrando BoltDB: %w", err)
		}
		c.db = nil
		log.Info().Msg("BoltDB closed successfully")
	}
	return nil
}

// ──── Eviction configuration ────

// SetBaseTTL sets the base TTL for metadata eviction.
// Will be used in Phase 4 to compute effectiveTTL = baseTTL × frequencyMultiplier.
func (c *InodeCache) SetBaseTTL(ttl time.Duration) {
	c.baseTTL = ttl
	log.Debug().Dur("ttl", ttl).Msg("InodeCache: base TTL configured")
}

// BaseTTL returns the configured base TTL.
func (c *InodeCache) BaseTTL() time.Duration {
	return c.baseTTL
}

// SetMaxEntries sets the maximum number of folders with cached children
// before size eviction activates (Phase 4).
func (c *InodeCache) SetMaxEntries(n int) {
	c.maxEntries = n
	log.Debug().Int("maxEntries", n).Msg("InodeCache: maxEntries configured")
}

// MaxEntries returns the configured maximum.
func (c *InodeCache) MaxEntries() int {
	return c.maxEntries
}

func (c *InodeCache) currentTime() time.Time {
	if c.now == nil {
		return time.Now()
	}
	return c.now()
}
