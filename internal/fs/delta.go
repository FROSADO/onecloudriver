package fs

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/frosado/onecloudriver/internal/types"
	"github.com/rs/zerolog/log"
)

// DeltaSync synchronizes remote changes (created, modified, deleted from
// other clients) with the local InodeCache tree. Uses the Microsoft Graph
// delta endpoint with periodic polling.
//
// Faithful to onedriver's DeltaLoop, with these differences:
//   - Injected as an independent service (DeltaSync) instead of a method
//     directly from the filesystem, to keep OneCloudFS focused on FUSE.
//   - Reconciliation of local items (isLocalID) uses InodeCache.MoveID
//     instead of the MoveID of the original onedriver.
//   - The delta link is persisted via InodeCache.SetDeltaLink (BoltDB).
type DeltaSync struct {
	graphClient   *graph.Client
	tokenProvider types.TokenProvider
	inodeCache    *InodeCache
	contentCache  *ContentCache

	// Lifecycle
	stopCh chan struct{}
	wg     sync.WaitGroup

	// Metrics
	syncCount  uint64
	errorCount uint64
}

// NewDeltaSync creates a new delta synchronization service.
func NewDeltaSync(
	graphClient *graph.Client,
	tokenProvider types.TokenProvider,
	inodeCache *InodeCache,
	contentCache *ContentCache,
) *DeltaSync {
	return &DeltaSync{
		graphClient:   graphClient,
		tokenProvider: tokenProvider,
		inodeCache:    inodeCache,
		contentCache:  contentCache,
		stopCh:        make(chan struct{}),
	}
}

// Start begins the delta polling in the background with the specified interval.
// Must be called only once. To stop it, call Stop().
func (d *DeltaSync) Start(ctx context.Context, interval time.Duration) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		log.Info().Dur("interval", interval).Msg("DeltaSync: started")
		d.deltaLoop(ctx, interval)
	}()
}

// Stop stops the delta polling and waits for the goroutine to finish.
func (d *DeltaSync) Stop() {
	select {
	case <-d.stopCh:
		// already closed
	default:
		close(d.stopCh)
	}
	d.wg.Wait()
	log.Info().Msg("DeltaSync: stopped")
}

// deltaLoop is the main delta polling loop.
// Inspired by onedriver's DeltaLoop (docs/onedriverCode/fs/delta.go).
func (d *DeltaSync) deltaLoop(ctx context.Context, interval time.Duration) {
	link := d.inodeCache.GetDeltaLink()

	for {
		select {
		case <-d.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		pollSuccess, newLink, err := d.pollAndApply(ctx, link)
		if err != nil {
			d.errorCount++
			log.Error().Err(err).Msg("DeltaSync: error during delta fetch, entering offline mode")
			d.inodeCache.SetOffline(true)

			// Short wait before retrying in offline mode
			select {
			case <-d.stopCh:
				return
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}

		// Success: online mode, persist
		if pollSuccess {
			if d.inodeCache.IsOffline() {
				d.inodeCache.SetOffline(false)
				log.Info().Msg("DeltaSync: connection restored, online mode")
			}
			link = newLink
			d.inodeCache.SetDeltaLink(link)
			if err := d.inodeCache.SerializeAll(); err != nil {
				log.Warn().Err(err).Msg("DeltaSync: error persisting cache after delta")
			}
			d.syncCount++

			// Wait until the next interval
			select {
			case <-d.stopCh:
				return
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
		}
	}
}

// pollAndApply queries the delta endpoint (with pagination) and applies the changes.
// Returns true if the poll was successful, the new delta link, and error if it failed.
func (d *DeltaSync) pollAndApply(ctx context.Context, link string) (bool, string, error) {
	allItems := make(map[string]graph.DeltaItem)
	var pollSuccess bool

	// Pagination: continue while there is @odata.nextLink
	for {
		items, nextLink, cont, err := d.graphClient.PollDelta(ctx, d.tokenProvider, link)
		if err != nil {
			// Network error: activate offline mode (not a fatal error)
			if isNetworkError(err) {
				d.inodeCache.SetOffline(true)
			}
			return false, link, err
		}

		// Deduplicate: the last delta for an ID is the one that counts
		for i := range items {
			allItems[items[i].ID] = items[i]
		}

		if !cont {
			// Last page: full cycle
			pollSuccess = true
			link = nextLink
			log.Debug().Int("count", len(allItems)).Msg("DeltaSync: delta cycle completed")
			break
		}
		link = nextLink
	}

	// Apply deltas (two-pass: first everything, then retry non-empty folders)
	secondPass := make([]string, 0)
	for _, delta := range allItems {
		if err := d.applyDelta(&delta); err != nil {
			if err.Error() == "directory is non-empty" {
				secondPass = append(secondPass, delta.ID)
			}
		}
	}
	for _, id := range secondPass {
		// Failures in the second pass are ignored (per Graph documentation)
		if delta, ok := allItems[id]; ok {
			_ = d.applyDelta(&delta)
		}
	}

	return pollSuccess, link, nil
}

// applyDelta applies a remote change (delta) to the local state.
// Inspired by onedriver's applyDelta.
//
// Cases it handles:
//  1. Deleted item → RemoveChild + ContentCache.Delete
//  2. New item (does not exist locally) → InsertChild
//  3. Moved/renamed item → MoveChild + update Name
//  4. Content modified remotely → invalidate ContentCache, update metadata
func (d *DeltaSync) applyDelta(delta *graph.DeltaItem) error {
	id := delta.ID
	name := delta.Name
	parentID := ""
	if delta.Parent != nil {
		parentID = delta.Parent.ID
	}

	logger := log.With().
		Str("id", id).
		Str("parentID", parentID).
		Str("name", name).
		Logger()

	// Is the parent in cache? If not, this delta doesn't affect us
	if parent := d.inodeCache.Get(parentID); parent == nil {
		logger.Trace().Msg("DeltaSync: skipping delta, parent not in cache")
		return nil
	}

	local := d.inodeCache.Get(id)

	// ──── Case 1: Deleted item ────
	if delta.Deleted != nil {
		if delta.IsFolder() && local != nil && local.HasChildren() {
			logger.Warn().Msg("DeltaSync: rejecting deletion of non-empty folder")
			return fmt.Errorf("directory is non-empty")
		}
		logger.Info().Msg("DeltaSync: applying remote deletion")
		d.inodeCache.RemoveChild(parentID, id)
		if err := d.contentCache.Delete(id); err != nil {
			logger.Warn().Err(err).Msg("DeltaSync: error clearing ContentCache after delete")
		}
		return nil
	}

	// ──── Case 2: New item ────
	if local == nil {
		// Does it already exist locally with another ID? (cache-only, no HTTP call)
		existing, _ := d.inodeCache.GetChild(context.Background(), parentID, name, nil)
		if existing != nil && isLocalID(existing.ID()) {
			logger.Info().Str("localID", existing.ID()).Msg("DeltaSync: reconciliando item local con ID remoto")
			d.inodeCache.MoveID(existing.ID(), id)
			return nil
		}

		logger.Info().Msg("DeltaSync: creating inode from delta")
		childInode := NewInodeDriveItem(&delta.DriveItem)
		d.inodeCache.InsertChild(parentID, name, childInode)
		return nil
	}

	// ──── Case 3: Moved/renamed item ────
	localName := local.Name()
	if local.ParentID() != parentID || local.Name() != name {
		logger.Info().
			Str("oldParent", local.ParentID()).
			Str("oldName", localName).
			Msg("DeltaSync: applying remote rename/move")

		oldParentID := local.ParentID()
		d.inodeCache.MoveChild(oldParentID, parentID, id)
		local.Lock()
		local.DriveItem.Name = name
		if delta.Parent != nil {
			local.DriveItem.Parent = delta.Parent
		}
		local.Unlock()
		// Don't return: there may be more changes (content modification)
	}

	// ──── Case 4: Content modified remotely ────
	if delta.ModTime != nil && delta.ModTimeUnix() > local.ModTime() {
		localETag := ""
		local.RLock()
		if local.DriveItem.ETag != "" {
			localETag = local.DriveItem.ETag
		}
		local.RUnlock()

		if delta.ETag != localETag {
			logger.Info().Msg("DeltaSync: overwriting local metadata with remote version")
			local.Lock()
			local.DriveItem.ModTime = delta.ModTime
			local.DriveItem.Size = delta.Size
			local.DriveItem.ETag = delta.ETag
			if delta.File != nil {
				local.DriveItem.File = delta.File
			}
			local.hasChanges = false
			local.Unlock()

			// Invalidate cached content (will be re-downloaded on the next Open)
			if err := d.contentCache.Delete(id); err != nil {
				logger.Warn().Err(err).Msg("DeltaSync: error clearing ContentCache after remote change")
			}
		}
	}

	return nil
}
