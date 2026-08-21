package fs

import (
	"context"
	"errors"
	"strings"
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
//
// uploadQuery is the minimal interface DeltaSync needs to know whether a
// local item has a pending upload (so a remote change does not clobber it).
type uploadQuery interface {
	HasPendingUpload(id string) bool
}

// errDirNotEmpty is returned by applyDelta when a remote folder deletion
// cannot be applied because the folder still has children in cache. It is a
// sentinel (matched with errors.Is) so the retry logic in applyPage does not
// depend on the error's message text.
var errDirNotEmpty = errors.New("directory is non-empty")

type DeltaSync struct {
	graphClient   *graph.Client
	tokenProvider types.TokenProvider
	inodeCache    *InodeCache
	contentCache  *ContentCache

	// uploads is optional (nil-safe); wired via SetUploadQuery before Start.
	uploads uploadQuery

	// rootRealID is the drive root's real item ID (e.g. "DF54C3BD0A263A52!sea8cc...").
	// Graph delta items reference the root by this ID in parentReference, while
	// the cache stores the root inode under graph.RootID ("root"). Learned once
	// (from the delta root item itself, or a lazy GetItem on the first parent
	// miss) and used to canonicalize parent lookups so root-level changes apply
	// (issue #101). rootIDOnce guarantees the network fetch runs at most once.
	rootRealID string
	rootIDOnce sync.Once

	// Lifecycle
	stopCh chan struct{}
	wg     sync.WaitGroup

	// Metrics
	syncCount  uint64
	errorCount uint64
}

// SetUploadQuery wires a source of pending-upload state (the UploadManager)
// so applyDelta can skip remote changes for items whose local upload has not
// completed yet. Must be called before Start. Nil is allowed (guards skipped).
func (d *DeltaSync) SetUploadQuery(q uploadQuery) {
	d.uploads = q
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

// PollOnce performs a single delta cycle immediately and returns the number
// of remote changes applied. It is the one-shot counterpart of the background
// deltaLoop, used by the `onecloudriver sync` command (issue #73): it reuses
// pollAndApply and, on success, persists the delta link and the dirty inodes
// exactly like a background cycle, so a later PollOnce or the mount's loop
// resumes from where it left off. Does not start any goroutine and does not
// require a mount.
func (d *DeltaSync) PollOnce(ctx context.Context) (int, error) {
	applied, newLink, err := d.pollAndApply(ctx, d.inodeCache.GetDeltaLink())
	if err != nil {
		// Pages applied before the failure are already persisted per page
		// (issue #68), so the partial count is accurate.
		return applied, err
	}
	d.inodeCache.SetDeltaLink(newLink)
	if err := d.inodeCache.SerializeDirty(); err != nil {
		log.Warn().Err(err).Msg("DeltaSync: error persisting cache after sync")
	}
	d.syncCount++
	return applied, nil
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

		_, newLink, err := d.pollAndApply(ctx, link)
		if err != nil {
			d.errorCount++
			log.Error().Err(err).Msg("DeltaSync: error during delta fetch, entering offline mode")
			d.inodeCache.SetOffline(true)

			// Resume from the last persisted per-page link (issue #68): pages
			// already applied before the failure are not re-fetched on the
			// next attempt. No-op when no page was applied yet (the persisted
			// link is still the previous cycle's).
			if persisted := d.inodeCache.GetDeltaLink(); persisted != "" {
				link = persisted
			}

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
		if d.inodeCache.IsOffline() {
			d.inodeCache.SetOffline(false)
			log.Info().Msg("DeltaSync: connection restored, online mode")
		}
		link = newLink
		d.inodeCache.SetDeltaLink(link)
		// Only the inodes changed since the last poll are persisted
		// (issue #67): the bytes written per delta sync now grow with the
		// number of changes, not with the size of the tree.
		if err := d.inodeCache.SerializeDirty(); err != nil {
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

// pollAndApply queries the delta endpoint (with pagination) and applies the
// changes **as each page arrives** (streaming, issue #68). The delta link is
// persisted after every page, so memory stays bounded (O(page size)) and a
// crash or transient failure resumes from the last applied page instead of
// re-fetching the whole cycle. Graph delta is idempotent and ordered, so
// partial progress is safe and converges.
//
// Returns the number of changes applied (partial if a later page failed,
// already persisted per page), the new delta link, and error if it failed.
func (d *DeltaSync) pollAndApply(ctx context.Context, link string) (int, string, error) {
	// Folder deletions blocked by a non-empty directory whose child deletions
	// may arrive on a later page. Bounded: only blocked folder deletions.
	var pendingDeletes []graph.DeltaItem
	total := 0

	// Pagination: continue while there is @odata.nextLink
	for {
		items, nextLink, cont, err := d.graphClient.PollDelta(ctx, d.tokenProvider, link)
		if err != nil {
			// Network error: activate offline mode (not a fatal error)
			if isNetworkError(err) {
				d.inodeCache.SetOffline(true)
			}
			return total, link, err
		}

		// Apply this page immediately; retry previously-blocked folder
		// deletions whose children may have been deleted in this page.
		pendingDeletes = d.applyPage(items, pendingDeletes)
		total += len(items)

		// Progress guarantee: persist the link so a crash or failure resumes
		// from here (delta is idempotent, so re-applying is also safe).
		d.inodeCache.SetDeltaLink(nextLink)

		if !cont {
			// Last page: full cycle. One final retry for any folder deletion
			// still blocked after all pages. A deletion that is still blocked
			// here is not fatal (the next cycle re-sends it), but it leaves the
			// cache diverged from the server, so it is reported.
			for i := range pendingDeletes {
				if err := d.applyDelta(&pendingDeletes[i]); err != nil {
					log.Warn().Err(err).
						Str("id", pendingDeletes[i].ID).
						Str("name", pendingDeletes[i].Name).
						Msg("DeltaSync: pending deletion still not applicable at end of cycle")
				}
			}
			log.Debug().Int("count", total).Msg("DeltaSync: delta cycle completed")
			return total, nextLink, nil
		}
		link = nextLink
	}
}

// applyPage applies one delta page immediately, keeping memory bounded to
// O(page size). Folder deletions that fail because the folder still has
// children in cache are retried once within the page (children deleted
// earlier in the same page may have freed the folder) and then returned so
// the caller can retry them on the next page — their child deletions may
// arrive there. A failure other than errDirNotEmpty cannot be retried
// usefully (the item is skipped for this cycle), but it is logged instead of
// discarded so a delta item that never applies is diagnosable.
func (d *DeltaSync) applyPage(items []graph.DeltaItem, pending []graph.DeltaItem) []graph.DeltaItem {
	// First pass: apply everything; collect blocked folder deletions.
	blocked := make([]graph.DeltaItem, 0, len(items))
	for i := range items {
		if d.applyOrReport(&items[i]) {
			blocked = append(blocked, items[i])
		}
	}

	// Second pass (within this page): retry blocked deletions once.
	stillBlocked := blocked[:0]
	for i := range blocked {
		if d.applyOrReport(&blocked[i]) {
			stillBlocked = append(stillBlocked, blocked[i])
		}
	}

	// Retry deletions blocked on earlier pages: their children may have been
	// deleted in this page. Keeps the cross-page retry semantics of the
	// previous full-set second pass, so no ghost folders are left behind.
	merged := make([]graph.DeltaItem, 0, len(stillBlocked)+len(pending))
	for i := range pending {
		if d.applyOrReport(&pending[i]) {
			merged = append(merged, pending[i])
		}
	}
	return append(merged, stillBlocked...)
}

// applyOrReport applies one delta item and reports whether it should be
// retried later, i.e. whether it is a folder deletion blocked by children
// that a later page may delete (errDirNotEmpty). Any other failure is logged
// and not retried.
func (d *DeltaSync) applyOrReport(item *graph.DeltaItem) (retryable bool) {
	err := d.applyDelta(item)
	switch {
	case err == nil:
		return false
	case errors.Is(err, errDirNotEmpty):
		return true
	default:
		d.errorCount++
		log.Warn().Err(err).
			Str("id", item.ID).
			Str("name", item.Name).
			Msg("DeltaSync: could not apply remote change")
		return false
	}
}

// contentMatchesRemote reports whether the locally cached content for id
// matches the remote quickXorHash in f. Nil-safe: returns false when the
// remote hash is absent or the cached content cannot be hashed (in which
// case the caller treats the content as changed and re-downloads it).
func (d *DeltaSync) contentMatchesRemote(id string, f *graph.File) bool {
	if f == nil || f.Hashes.QuickXorHash == "" {
		return false
	}
	sum, ok := d.contentCache.SumQuickXorHash(id)
	if !ok {
		return false
	}
	return strings.EqualFold(sum, f.Hashes.QuickXorHash)
}

// resolveParentID canonicalizes a delta item's parent reference to a cache
// key. Graph delta items reference the drive root by its real item ID (e.g.
// "DF54C3BD0A263A52!sea8cc..."), but the cache stores the root inode under
// graph.RootID ("root"), so without this mapping root-level changes would be
// skipped as "parent not in cache" (issue #101).
//
// The real root ID is learned at most once, from either:
//   - the delta root item itself (name "root", folder, no parent) — free, no
//     network, and typically precedes root-level changes in the same cycle; or
//   - a lazy GetItem(root) on the first parent miss — covers cycles that only
//     carry a root-level edit (no root item), with at most one extra call per
//     DeltaSync lifetime (sync.Once). Fails gracefully: on error the parent is
//     left unchanged and the item is skipped as before.
func (d *DeltaSync) resolveParentID(parentID string, delta *graph.DeltaItem) string {
	// The delta root item itself reveals the drive's real root ID for free.
	// Guarded by the empty parent so a user folder literally named "root"
	// (which has a parent) is never mistaken for the drive root.
	if parentID == "" && delta.IsFolder() && delta.Name == "root" {
		d.rootRealID = delta.ID
		return parentID // the root has no parent: nothing to look up
	}
	if parentID == "" || parentID == string(graph.RootID) {
		return parentID
	}
	if parentID == d.rootRealID {
		return string(graph.RootID)
	}
	// Parent not in cache: it might be the real root (a root-level edit does
	// not emit the root item). Fetch the drive root once, lazily, to find out.
	if d.inodeCache.Get(parentID) == nil && d.graphClient != nil {
		d.rootIDOnce.Do(func() {
			root, err := d.graphClient.GetItem(context.Background(), d.tokenProvider, graph.RootID)
			if err == nil && root != nil && root.ID != "" {
				d.rootRealID = root.ID
			}
		})
	}
	if parentID == d.rootRealID {
		return string(graph.RootID)
	}
	return parentID
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
	// Map the drive root's real ID to the cache key so root-level changes apply.
	parentID = d.resolveParentID(parentID, delta)

	logger := log.With().
		Str("id", id).
		Str("parentID", parentID).
		Str("name", name).
		Logger()

	// The delta root item itself: refresh the cached root inode's folder
	// metadata (e.g. childCount) and persist it as dirty. Its ID is the real
	// root ID, which resolveParentID learned above.
	if parentID == "" && delta.IsFolder() && delta.Name == "root" && delta.Folder != nil {
		if root := d.inodeCache.Get(string(graph.RootID)); root != nil {
			root.Lock()
			root.DriveItem.Folder = delta.Folder
			root.Unlock()
			d.inodeCache.markDirty(string(graph.RootID))
			logger.Trace().Msg("DeltaSync: refreshed root folder metadata")
		}
		return nil
	}

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
			return errDirNotEmpty
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
		// Guard against data loss: if the local item has pending local work —
		// a write not yet Fsync'ed (hasChanges) or an upload session still
		// queued/in flight — the local version wins and the remote delta is
		// not applied.
		if local.HasChanges() {
			logger.Info().Msg("DeltaSync: skipping remote change, local item has pending (un-uploaded) changes")
			return nil
		}
		if d.uploads != nil && d.uploads.HasPendingUpload(id) {
			logger.Info().Msg("DeltaSync: skipping remote change, local item has a pending upload")
			return nil
		}

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

			// Content-change detection (issue #32): only invalidate the cache
			// when the local bytes actually differ from the remote quickXorHash.
			// If they already match (e.g. both sides hold the same server
			// content), keep the cache and avoid an unnecessary re-download.
			if d.contentMatchesRemote(id, delta.File) {
				logger.Debug().Msg("DeltaSync: local content hash matches remote, keeping cache")
			} else if err := d.contentCache.Delete(id); err != nil {
				logger.Warn().Err(err).Msg("DeltaSync: error clearing ContentCache after remote change")
			}
		}
	}

	return nil
}
