package fs

import (
	"context"
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

	// Contadores para Stats (UI)
	hits      atomic.Uint64
	misses    atomic.Uint64
	evictions atomic.Uint64 // Fase 4: contador de children evictados

	// ──── Fase 3: BoltDB + modo offline ────
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
}

// NewInodeCache creates a new empty inode cache with default values.
func NewInodeCache() *InodeCache {
	return &InodeCache{
		rootID:     "root",
		maxEntries: 2000,
		baseTTL:    60 * time.Second,
	}
}

// ──── Basic operations ────

// Get obtains an inode by ID. Returns nil if it doesn't exist.
func (c *InodeCache) Get(id string) *Inode {
	if val, ok := c.inodes.Load(id); ok {
		return val.(*Inode)
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

	// Update parent: add this ID to its children and increment subdir
	parentID := inode.ParentID()
	if parentID != "" {
		if parent := c.Get(parentID); parent != nil {
			parent.Lock()
			if parent.children == nil {
				parent.children = []string{inode.ID()}
			} else {
				// Avoid duplicates
				found := false
				for _, id := range parent.children {
					if id == inode.ID() {
						found = true
						break
					}
				}
				if !found {
					parent.children = append(parent.children, inode.ID())
				}
			}
			if inode.IsDir() {
				parent.subdir++
			}
			parent.Unlock()
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
			parent.Lock()
			// Remove from children
			filtered := parent.children[:0]
			for _, childID := range parent.children {
				if childID != id {
					filtered = append(filtered, childID)
				}
			}
			parent.children = filtered
			if inode.IsDir() && parent.subdir > 0 {
				parent.subdir--
			}
			parent.Unlock()
		}
	}

	c.inodes.Delete(id)
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
	c.inodes.Range(func(key, value interface{}) bool {
		inode := value.(*Inode)
		if inode.ParentID() == parentID {
			inode.RLock()
			items = append(items, inode.DriveItem)
			inode.RUnlock()
		}
		return true
	})
	return items
}

// buildChildMap construye un mapa name→*Inode a partir de IDs de children.
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
// por "/" y devuelve el Inode en la hoja. Útil para resolver rutas completas
// (p.ej. CLI o modo offline) sin consultar a Graph.
//
// The path "/" returns ErrIsRoot (the root is not an Inode in cache, OneCloudFS
// la representa directamente). Rutas como "a/b/c" se navegan componente a
// componente.
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
			return nil, fmt.Errorf("navegando %q en %q: %w", name, currentID, err)
		}
		child, exists := children[name]
		if !exists {
			return nil, fmt.Errorf("%w: %q no encontrado en %q", syscall.ENOENT, name, currentID)
		}
		// Is it the last component?
		if i == len(components)-1 {
			return child, nil
		}
		// Continuar navegando — debe ser una carpeta
		if !child.IsDir() {
			return nil, fmt.Errorf("%w: %q no es una carpeta", syscall.ENOTDIR, name)
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
		return // ya iniciado
	}
	c.stopCh = make(chan struct{})
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(sweepInterval)
		defer ticker.Stop()
		log.Trace().Dur("interval", sweepInterval).Msg("InodeCache: sweep iniciado")
		for {
			select {
			case <-c.stopCh:
				log.Trace().Msg("InodeCache: sweep detenido")
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

	c.evictExpiredChildren()     // Tier 1: TTL con decay de frecuencia
	c.evictChildrenBySizeLimit() // Tier 2: size limit by score
}

// ForceSweep runs an immediate sweep (useful for tests and UI).
// Does not wait for the next timer tick.
func (c *InodeCache) ForceSweep() {
	c.sweep()
}

// evictExpiredChildren recorre los inodos con children cacheados, aplica
// decay al accessCount, y evicta aquellos cuyo TTL efectivo haya expirado.
func (c *InodeCache) evictExpiredChildren() {
	now := time.Now()

	c.inodes.Range(func(key, value interface{}) bool {
		inode := value.(*Inode)
		if !inode.IsChildrenFetched() {
			return true // sin children cacheados, nada que evictar
		}

		// Aplicar decay al contador de acceso
		inode.DecayChildrenAccess()

		// Calcular si el TTL efectivo ha expirado
		accessCount := inode.ChildrenAccessCount()
		ttl := effectiveTTL(c.baseTTL, accessCount)
		expiry := inode.ChildrenLastAccess().Add(ttl)

		if now.After(expiry) {
			log.Debug().
				Str("id", inode.ID()).
				Str("name", inode.Name()).
				Uint64("accessCount", accessCount).
				Dur("ttl", ttl).
				Msg("Evictando children por TTL expirado")
			inode.EvictChildren()
			c.evictions.Add(1)
		}
		return true
	})
}

// evictChildrenBySizeLimit libera los children de las carpetas con menor
// score until the number of folders with cached children returns to
// por debajo de maxEntries.
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

	now := time.Now()
	var scores []scoredEntry
	count := 0

	c.inodes.Range(func(key, value interface{}) bool {
		inode := value.(*Inode)
		if !inode.IsChildrenFetched() {
			return true
		}
		count++
		accessCount := inode.ChildrenAccessCount()
		minutesSinceLastAccess := now.Sub(inode.ChildrenLastAccess()).Minutes()
		score := float64(accessCount) / (minutesSinceLastAccess + 1)
		scores = append(scores, scoredEntry{
			id:    key.(string),
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

// Invalidate marca los children de una carpeta como no inicializados,
// forcing a refetch on the next GetChildren. Does not delete individual
// Inodes (they remain part of the tree).
//
// Useful after creating/deleting/moving files in a folder.
func (c *InodeCache) Invalidate(parentID string) {
	if parent := c.Get(parentID); parent != nil {
		parent.SetChildren(nil)
		log.Debug().Str("parentID", parentID).Msg("InodeCache invalidada (children → nil)")
	}
}

// InvalidateAll resets ALL children of the cache, forcing a complete refetch
// on the next accesses. Useful for "Force refresh" in the UI.
func (c *InodeCache) InvalidateAll() {
	c.inodes.Range(func(key, value interface{}) bool {
		inode := value.(*Inode)
		if inode.HasChildren() {
			inode.SetChildren(nil)
		}
		return true
	})
	log.Info().Msg("InodeCache invalidada completamente")
}

// ──── API de observabilidad (para UI) ────

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
	c.inodes.Range(func(key, value interface{}) bool {
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

// ──── Ciclo de vida ────

// Insert adds a child inode to a specific parent. Useful for Mkdir/Create
// donde el inodo se acaba de crear y ya conocemos padre e hijo.
func (c *InodeCache) InsertChild(parentID, name string, childInode *Inode) {
	if childInode == nil {
		return
	}
	c.inodes.Store(childInode.ID(), childInode)

	// Establecer la referencia al padre en el hijo (para que MoveID pueda
	// actualizar parent.children tras el swap de ID local → remoto)
	childInode.Lock()
	childInode.DriveItem.Parent = &graph.DriveItemParent{ID: parentID}
	childInode.Unlock()

	// Actualizar padre
	if parent := c.Get(parentID); parent != nil {
		parent.Lock()
		// If children is nil, the folder hasn't been fully listed yet
		// (or was evicted). In that case, we DON'T initialize children
		// with a single item — that would make GetChildren think it already
		// has all children and never call the fetcher, losing the rest.
		// We only add to the list if it was ALREADY initialized (≠ nil).
		if parent.children != nil {
			found := false
			for _, id := range parent.children {
				if id == childInode.ID() {
					found = true
					break
				}
			}
			if !found {
				parent.children = append(parent.children, childInode.ID())
			}
		}
		// NOTA: no tocamos parent.children si es nil. El inode hijo queda
		// stored in sync.Map (accessible via Get by ID) but doesn't appear
		// in the parent's children list. On the next GetChildren,
		// the fetcher will bring ALL children from Graph, including this one.
		if childInode.IsDir() {
			parent.subdir++
		}
		parent.Unlock()
	}

	// NOTE: We don't invalidate the parent's children. The list is already
	// updated directly (the new child was added). Invalidating here
	// would break subsequent Unlink/Rmdir because the next GetChildren
	// would refetch from the API and might not find the newly created item.
}

// RemoveChild elimina un hijo de un padre. Útil para Rmdir/Unlink.
func (c *InodeCache) RemoveChild(parentID, childID string) {
	inode := c.Get(childID)
	if inode == nil {
		return
	}

	// Quitar del padre
	if parent := c.Get(parentID); parent != nil {
		parent.Lock()
		filtered := parent.children[:0]
		for _, id := range parent.children {
			if id != childID {
				filtered = append(filtered, id)
			}
		}
		parent.children = filtered
		if inode.IsDir() && parent.subdir > 0 {
			parent.subdir--
		}
		parent.Unlock()
	}

	c.inodes.Delete(childID)

	// NOTE: We don't invalidate the parent's children. The list is already
	// updated directly (the child was removed).
}

// MoveID mueve un inodo de una key vieja a una nueva. Se usa cuando un item
// local recibe un ID real de OneDrive tras la primera subida.
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
	inode := val.(*Inode)

	// Actualizar el ID interno del inodo (thread-safe)
	inode.Lock()
	inode.DriveItem.ID = newID
	inode.Unlock()

	c.inodes.Store(newID, inode)
	c.inodes.Delete(oldID)

	// Actualizar children del padre
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
		}
	}
}

// MoveChild mueve un hijo de una carpeta padre a otra. Útil para rename/move.
func (c *InodeCache) MoveChild(oldParentID, newParentID, childID string) {
	child := c.Get(childID)
	if child == nil {
		return
	}

	// Quitar del padre antiguo
	if oldParent := c.Get(oldParentID); oldParent != nil {
		oldParent.Lock()
		filtered := oldParent.children[:0]
		for _, id := range oldParent.children {
			if id != childID {
				filtered = append(filtered, id)
			}
		}
		oldParent.children = filtered
		if child.IsDir() && oldParent.subdir > 0 {
			oldParent.subdir--
		}
		oldParent.Unlock()
	}

	// Add to new parent
	if newParent := c.Get(newParentID); newParent != nil {
		newParent.Lock()
		if newParent.children == nil {
			newParent.children = []string{childID}
		} else {
			newParent.children = append(newParent.children, childID)
		}
		if child.IsDir() {
			newParent.subdir++
		}
		newParent.Unlock()
	}
}

// ──── Persistencia con BoltDB ────

// boltBucketMetadata, boltBucketDelta y boltBucketUploads son los nombres
// de los buckets en la DB.
var (
	boltBucketMetadata = []byte("metadata") // id → JSON de Inode
	boltBucketDelta    = []byte("delta")    // "link" → deltaURL
	boltBucketUploads  = []byte("uploads")  // id → JSON de UploadSession
)

// InitBoltDB abre (o crea) la base de datos BoltDB y carga los datos existentes.
// Must be called once, before using the cache.
func (c *InodeCache) InitBoltDB(dbPath string) error {
	db, err := bolt.Open(dbPath, 0600, &bolt.Options{Timeout: 1})
	if err != nil {
		return fmt.Errorf("error abriendo BoltDB en %s: %w", dbPath, err)
	}

	// Crear buckets si no existen
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

	// Cargar datos existentes desde BoltDB a memoria
	if err := c.DeserializeFromDisk(); err != nil {
		log.Warn().Err(err).Msg("Error loading data from BoltDB — cache starts empty")
	}

	log.Info().Str("path", dbPath).Msg("BoltDB inicializado correctamente")
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

	return c.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(boltBucketMetadata)
		if bucket == nil {
			return fmt.Errorf("bucket metadata no encontrado")
		}

		// Pass 1: fetched folders + inodes with ParentID (browsed tree) +
		// recoger los children de las carpetas fetched.
		toPersist := make(map[string]bool)
		var childIDs []string

		c.inodes.Range(func(key, value interface{}) bool {
			inode := value.(*Inode)
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

		// Pasada 3: escribir
		for id := range toPersist {
			if inode := c.Get(id); inode != nil {
				if err := bucket.Put([]byte(id), inode.AsJSON()); err != nil {
					log.Warn().Err(err).Str("id", id).Msg("Error persistiendo inodo")
				}
			}
		}
		return nil
	})
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
				log.Warn().Err(err).Str("id", string(k)).Msg("Error deserializando inodo, omitiendo")
				return nil // Continuar con el siguiente
			}
			// Solo cargar si no existe ya en memoria (la memoria gana)
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
		log.Info().Int("count", count).Msg("Inodos cargados desde BoltDB")
	}
	return nil
}

// GetDeltaLink devuelve la URL del delta link almacenado.
func (c *InodeCache) GetDeltaLink() string {
	if c.db == nil {
		return ""
	}

	var link string
	_ = c.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(boltBucketDelta)
		if bucket != nil {
			if data := bucket.Get([]byte("link")); data != nil {
				link = string(data)
			}
		}
		return nil
	})
	return link
}

// SetDeltaLink guarda la URL del delta link en BoltDB.
func (c *InodeCache) SetDeltaLink(link string) {
	if c.db == nil {
		return
	}

	_ = c.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(boltBucketDelta)
		if bucket != nil {
			return bucket.Put([]byte("link"), []byte(link))
		}
		return nil
	})
}

// ──── Persistencia de UploadSession (Fase 5b) ────

// SaveUploadSession persiste una UploadSession a BoltDB para que sobreviva
// across restarts. The caller must serialize the session to JSON beforehand.
func (c *InodeCache) SaveUploadSession(id string, data []byte) {
	if c.db == nil {
		return
	}
	_ = c.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(boltBucketUploads)
		if bucket != nil {
			return bucket.Put([]byte(id), data)
		}
		return nil
	})
}

// LoadUploadSessions carga todas las sesiones de subida incompletas desde
// BoltDB y las devuelve como un mapa id → JSON. Se usa al arrancar para
// restaurar sesiones que no terminaron (cierre abrupto, crash, etc.).
func (c *InodeCache) LoadUploadSessions() map[string][]byte {
	if c.db == nil {
		return nil
	}
	result := make(map[string][]byte)
	_ = c.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(boltBucketUploads)
		if bucket == nil {
			return nil
		}
		return bucket.ForEach(func(k, v []byte) error {
			result[string(k)] = v
			return nil
		})
	})
	return result
}

// DeleteUploadSession elimina una UploadSession de BoltDB.
func (c *InodeCache) DeleteUploadSession(id string) {
	if c.db == nil {
		return
	}
	_ = c.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(boltBucketUploads)
		if bucket != nil {
			return bucket.Delete([]byte(id))
		}
		return nil
	})
}

// ──── Modo offline ────

// IsOffline returns true if the cache is in offline mode.
func (c *InodeCache) IsOffline() bool {
	return c.offline.Load()
}

// SetOffline activa/desactiva el modo offline.
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

// Close cierra BoltDB y detiene la goroutine de sweep. Seguro para llamar
// multiple times and from several concurrent goroutines (Mount's defer + the
// signal handler de desmontaje), gracias a closeMu.
func (c *InodeCache) Close() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()

	// Detener sweep (idempotente: la primera llamada pone stopCh a nil)
	if c.stopCh != nil {
		close(c.stopCh)
		c.wg.Wait()
		c.stopCh = nil
	}

	// Cerrar BoltDB (idempotente: la primera llamada pone c.db a nil)
	if c.db != nil {
		if err := c.SerializeAll(); err != nil {
			log.Warn().Err(err).Msg("Error serializing cache to BoltDB during Close")
		}
		if err := c.db.Close(); err != nil {
			return fmt.Errorf("error cerrando BoltDB: %w", err)
		}
		c.db = nil
		log.Info().Msg("BoltDB cerrado correctamente")
	}
	return nil
}

// ──── Eviction configuration ────

// SetBaseTTL sets the base TTL for metadata eviction.
// Will be used in Phase 4 to compute effectiveTTL = baseTTL × frequencyMultiplier.
func (c *InodeCache) SetBaseTTL(ttl time.Duration) {
	c.baseTTL = ttl
	log.Debug().Dur("ttl", ttl).Msg("InodeCache: TTL base configurado")
}

// BaseTTL devuelve el TTL base configurado.
func (c *InodeCache) BaseTTL() time.Duration {
	return c.baseTTL
}

// SetMaxEntries sets the maximum number of folders with cached children
// before size eviction activates (Phase 4).
func (c *InodeCache) SetMaxEntries(n int) {
	c.maxEntries = n
	log.Debug().Int("maxEntries", n).Msg("InodeCache: maxEntries configurado")
}

// MaxEntries returns the configured maximum.
func (c *InodeCache) MaxEntries() int {
	return c.maxEntries
}
