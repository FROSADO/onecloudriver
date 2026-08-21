package fs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/frosado/onecloudriver/internal/types"
	"github.com/rs/zerolog/log"
)

const (
	// defaultMaxUploadsInFlight limits the number of concurrent uploads to
	// avoid server throttling and bandwidth saturation.
	defaultMaxUploadsInFlight = 5

	// defaultMaxUploadRetries is the maximum number of retries for *permanent*
	// errors (HTTP 4xx, ...) before abandoning the upload. Transient network
	// errors never abandon the session.
	defaultMaxUploadRetries = 5

	// maxRetryBackoff caps the exponential backoff between retries of a
	// transient (network) error.
	maxRetryBackoff = 1 * time.Minute
)

// uploadTickerInterval is the interval of the ticker that processes the queue
// and launches new uploads. Copy of the original onedriver (2s).
const uploadTickerInterval = 2 * time.Second

// UploadManager orchestrates pending uploads in the background with retries.
// It decouples FUSE writes (Fsync) from the HTTP upload: Fsync only marks
// hasChanges=false and enqueues; UploadManager handles the rest.
//
// Faithful to onedriver's design (docs/onedriverCode/fs/upload_manager.go),
// adapted to our modular architecture without a global *Filesystem.
type UploadManager struct {
	// Communication channels
	queue         chan *UploadSession // new uploads
	deletionQueue chan string         // cancelaciones por delete

	// Internal state
	mu       sync.Mutex
	sessions map[string]*UploadSession // id → active session
	inFlight int                       // uploads in flight (cap = maxUploadsInFlight)

	// Limits wired from MountConfig (<= 0 falls back to the defaults).
	maxUploadsInFlight int
	maxUploadRetries   int

	// Dependencias
	graphClient   *graph.Client
	tokenProvider types.TokenProvider
	inodeCache    *InodeCache   // for MoveID after completing the upload
	contentCache  *ContentCache // for reading/writing content

	// Lifecycle
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewUploadManager creates a new UploadManager and restores incomplete
// sessions from BoltDB (if any). Restored sessions that were in progress
// are cancelled (non-resumable, like onedriver).
//
// maxUploadsInFlight and maxUploadRetries wire the corresponding MountConfig
// values; a value <= 0 falls back to the package defaults.
func NewUploadManager(
	graphClient *graph.Client,
	tokenProvider types.TokenProvider,
	inodeCache *InodeCache,
	contentCache *ContentCache,
	maxUploadsInFlight, maxUploadRetries int,
) *UploadManager {
	if maxUploadsInFlight <= 0 {
		maxUploadsInFlight = defaultMaxUploadsInFlight
	}
	if maxUploadRetries <= 0 {
		maxUploadRetries = defaultMaxUploadRetries
	}

	um := &UploadManager{
		queue:              make(chan *UploadSession, 100),
		deletionQueue:      make(chan string, 100),
		sessions:           make(map[string]*UploadSession),
		maxUploadsInFlight: maxUploadsInFlight,
		maxUploadRetries:   maxUploadRetries,
		graphClient:        graphClient,
		tokenProvider:      tokenProvider,
		inodeCache:         inodeCache,
		contentCache:       contentCache,
		stopCh:             make(chan struct{}),
	}

	// Restore incomplete sessions from disk (previous abrupt shutdown)
	um.restoreIncompleteSessions()

	return um
}

// Start starts the background processing loop.
func (um *UploadManager) Start() {
	um.wg.Add(1)
	go um.uploadLoop()
}

// Stop gracefully stops the UploadManager. Waits for the loop to finish
// and cleans up the in-memory sessions.
func (um *UploadManager) Stop() {
	close(um.stopCh)
	um.wg.Wait()
}

// QueueUpload enqueues a file for asynchronous upload. Takes a snapshot
// of the content from ContentCache at this moment so the upload is
// atomic with respect to subsequent writes.
//
// If the file is empty, it is not enqueued (nothing to upload) and false is
// returned. A false return with content on disk means the read failed
// transiently (e.g. the first FUSE flush raced ahead of the write): the
// caller keeps the inode dirty so the next flush retries.
func (um *UploadManager) QueueUpload(id, parentID, name string) bool {
	// Take an on-disk snapshot of the content (issue #69): the upload streams
	// from a dedicated file instead of materializing the whole file in heap.
	path, size, err := um.contentCache.Snapshot(id)
	if err != nil {
		// os.ErrNotExist means there is no content on disk (nothing to
		// upload); any other error is an I/O failure worth logging.
		if !errors.Is(err, os.ErrNotExist) {
			log.Warn().Err(err).Str("id", id).Str("name", name).Msg("UploadManager: error snapshotting content for upload")
		}
		return false
	}
	if size == 0 {
		// Empty file: nothing to upload (Snapshot already removed it).
		return false
	}

	session, err := NewUploadSessionSnapshot(id, parentID, name, path, size)
	if err != nil {
		log.Warn().Err(err).Str("id", id).Msg("UploadManager: error creating UploadSession")
		if rmErr := os.Remove(path); rmErr != nil {
			log.Warn().Err(rmErr).Str("path", path).Msg("UploadManager: error removing orphaned upload snapshot")
		}
		return false
	}

	um.queue <- session
	return true
}

// CancelUpload removes any pending or in-flight upload for the given ID.
// It is called when a file is deleted (Unlink) while it was queued.
func (um *UploadManager) CancelUpload(id string) {
	um.deletionQueue <- id
}

// HasPendingUpload reports whether there is a pending or in-flight upload
// session for the given ID. Used by DeltaSync to avoid overwriting a local
// item whose upload has not completed yet.
func (um *UploadManager) HasPendingUpload(id string) bool {
	um.mu.Lock()
	defer um.mu.Unlock()
	_, exists := um.sessions[id]
	return exists
}

// RenameSession updates the name (and optionally the parent) of a pending,
// retrying or in-flight upload session, and re-persists it to BoltDB. Used
// when a locally-created file is renamed (or moved) before its first upload:
// the upload must create the remote item with the new name in the new folder.
//
// For an in-flight session the running request already captured the old name,
// so the update cannot change that PUT — but it makes retries use the new
// name, and executeUpload applies the rename/move remotely after completion
// (see applyInflightTargetChange). Unknown IDs are a no-op.
func (um *UploadManager) RenameSession(id, newParentID, newName string) {
	um.mu.Lock()
	session, exists := um.sessions[id]
	um.mu.Unlock()
	if !exists {
		return
	}

	session.mu.Lock()
	session.Name = newName
	if newParentID != "" {
		session.ParentID = newParentID
	}
	session.mu.Unlock()

	// Re-persist so a restart keeps the new name.
	um.persistSession(id, session)
}

// ──── Main loop ────

// uploadLoop processes the upload queue, handles retries, and completes/
// cancels sessions. The queue and deletionQueue channels are drained before
// the ticker launches new uploads.
func (um *UploadManager) uploadLoop() {
	defer um.wg.Done()

	ticker := time.NewTicker(uploadTickerInterval)
	defer ticker.Stop()

	log.Info().Msg("UploadManager: loop started")

	for {
		select {
		case <-um.stopCh:
			log.Info().Msg("UploadManager: stopped")
			return

		case session := <-um.queue:
			// Drain new sessions: deduplicate and persist
			um.enqueueSession(session)

		case cancelID := <-um.deletionQueue:
			// Drain cancellations
			um.finishSession(cancelID)

		case <-ticker.C:
			// Process active sessions: launch new ones, retry failed ones,
			// complete successful ones
			um.processSessions()
		}
	}
}

// enqueueSession registers a new session, deduplicating if one already exists
// for the same ID, and persists it to BoltDB.
func (um *UploadManager) enqueueSession(session *UploadSession) {
	um.mu.Lock()
	defer um.mu.Unlock()

	// Deduplicate: if there's already a session for this ID, cancel it first
	if old, exists := um.sessions[session.ID]; exists {
		log.Debug().Str("id", session.ID).Msg("UploadManager: deduplicating existing session")
		old.setState(uploadComplete, nil) // mark as complete so it gets cleaned up
		old.DiscardSnapshot()             // its snapshot is no longer needed
	}

	um.sessions[session.ID] = session

	// Persist to BoltDB to survive restarts
	um.persistSession(session.ID, session)
}

// persistSession serializes a session and stores it in BoltDB. Serialization
// is not expected to fail, and the upload proceeds from memory either way, but
// an unpersisted session is lost on restart — so the failure is logged instead
// of being dropped by an `err == nil` guard.
func (um *UploadManager) persistSession(id string, session *UploadSession) {
	data, err := session.AsJSON()
	if err != nil {
		log.Warn().Err(err).Str("id", id).
			Msg("UploadManager: could not serialize session; it will not survive a restart")
		return
	}
	um.inodeCache.SaveUploadSession(id, data)
}

// processSessions iterates over all active sessions and decides what to do
// based on their state.
func (um *UploadManager) processSessions() {
	um.mu.Lock()
	// Local copy so we don't hold the mutex during uploads
	sessions := make(map[string]*UploadSession, len(um.sessions))
	for id, s := range um.sessions {
		sessions[id] = s
	}
	um.mu.Unlock()

	for _, session := range sessions {
		switch session.getState() {
		case uploadPending:
			um.launchIfCapacity(session)

		case uploadErrored:
			// Respect the backoff window for transient (network) errors.
			if time.Now().Before(session.getNextRetryAt()) {
				continue
			}

			session.Retries++
			if session.isTransient() {
				// Transient network error: NEVER abandon the session. Back off
				// and keep it so the upload completes once the connection
				// returns. This is what guarantees that a local edit made during
				// an outage is not silently stranded in ContentCache.
				session.setNextRetryAt(time.Now().Add(um.backoffFor(session.Retries)))
				log.Warn().
					Str("id", session.ID).
					Str("name", session.Name).
					Int("retries", session.Retries).
					Str("lastErr", session.LastErr).
					Msg("UploadManager: network error, keeping session for retry")
				session.setState(uploadPending, nil)
				um.launchIfCapacity(session)
			} else if session.Retries > um.retryCap() {
				log.Error().
					Str("id", session.ID).
					Str("name", session.Name).
					Int("retries", session.Retries).
					Str("lastErr", session.LastErr).
					Msg("UploadManager: too many retries on permanent error, abandoning upload")
				um.finishSession(session.ID)
			} else {
				log.Warn().
					Str("id", session.ID).
					Str("name", session.Name).
					Int("retries", session.Retries).
					Msg("UploadManager: retrying upload")
				session.setState(uploadPending, nil)
				um.launchIfCapacity(session)
			}

		case uploadComplete:
			um.finishSession(session.ID)
		}
	}
}

// launchIfCapacity starts an upload goroutine for the session if there is
// room under the in-flight cap; otherwise the session stays pending and is
// retried on the next tick.
func (um *UploadManager) launchIfCapacity(session *UploadSession) {
	um.mu.Lock()
	if um.inFlight >= um.inFlightCap() {
		um.mu.Unlock()
		return
	}
	um.inFlight++
	um.mu.Unlock()

	go um.executeUpload(session)
}

// inFlightCap returns the configured maximum number of concurrent uploads,
// falling back to the default when the value was not wired (<= 0).
func (um *UploadManager) inFlightCap() int {
	if um.maxUploadsInFlight <= 0 {
		return defaultMaxUploadsInFlight
	}
	return um.maxUploadsInFlight
}

// retryCap returns the configured maximum retries for permanent errors,
// falling back to the default when the value was not wired (<= 0).
func (um *UploadManager) retryCap() int {
	if um.maxUploadRetries <= 0 {
		return defaultMaxUploadRetries
	}
	return um.maxUploadRetries
}

// backoffFor returns the delay before retrying a session that failed with a
// transient network error: exponential, capped at maxRetryBackoff.
func (um *UploadManager) backoffFor(retries int) time.Duration {
	d := uploadTickerInterval
	for i := 1; i < retries && d < maxRetryBackoff; i++ {
		d *= 2
		if d > maxRetryBackoff {
			d = maxRetryBackoff
		}
	}
	return d
}

// executeUpload performs the real upload (simple PUT or upload session depending
// on size). Runs in a goroutine to not block the main loop.
//
// The in-flight slot is released on EVERY exit path (success or error):
// otherwise a failed upload would leave inFlight consumed forever and the
// pipeline would stall with the first error (issue #87).
func (um *UploadManager) executeUpload(session *UploadSession) {
	defer um.releaseInFlight()

	session.setState(uploadUploading, nil)

	id := session.ID
	isLocal := isLocalID(id)
	parentID := session.ParentID
	name := session.Name

	ctx := context.Background()

	// For an existing file, capture the known ETag so the upload can use
	// optimistic concurrency control (If-Match). A locally-created file
	// (local ID) has no remote version yet, so no ETag is sent.
	var etag string
	if !isLocal {
		if inode := um.inodeCache.Get(id); inode != nil {
			inode.RLock()
			etag = inode.DriveItem.ETag
			inode.RUnlock()
		}
	}

	item, createdNew, err := um.upload(ctx, id, isLocal, parentID, name, session.OpenContent, etag)
	if err != nil {
		session.setState(uploadErrored, err)
		session.setTransient(isNetworkError(err))
		return
	}

	// Successful upload: update the inode in cache
	if createdNew {
		// A new remote item was created (local file, or a conflict resolved by
		// re-uploading under the original name): swap the inode ID.
		oldID := id
		if inode := um.inodeCache.Get(oldID); inode != nil {
			inode.Lock()
			inode.DriveItem.ID = item.ID
			inode.DriveItem.ETag = item.ETag
			inode.DriveItem.Size = item.Size
			inode.DriveItem.ModTime = item.ModTime
			inode.Unlock()
		}
		um.inodeCache.MoveID(oldID, item.ID)
	} else {
		// Overwrite of an existing file (same remote ID)
		if inode := um.inodeCache.Get(id); inode != nil {
			inode.Lock()
			inode.DriveItem.ETag = item.ETag
			inode.DriveItem.Size = item.Size
			inode.DriveItem.ModTime = item.ModTime
			inode.Unlock()
		}
	}

	log.Info().
		Str("id", item.ID).
		Str("name", session.Name).
		Msg("UploadManager: upload completed")

	// Invalidate the parent's children so the next Readdir
	// gets updated metadata from the server.
	// Note: Invalidate only sets children=nil (doesn't delete inodes), so
	// un-uploaded local items survive in the sync.Map.
	um.inodeCache.Invalidate(parentID)

	// The item may have been renamed/moved while the upload was in flight
	// (RenameSession updates in-flight sessions too). Apply the change
	// remotely with the real ID so the rename is not lost.
	session.mu.Lock()
	curName := session.Name
	curParent := session.ParentID
	session.mu.Unlock()
	if curName != name || curParent != parentID {
		if err := um.applyInflightTargetChange(ctx, item.ID, name, curName, parentID, curParent); err != nil {
			log.Warn().Err(err).
				Str("id", item.ID).
				Str("oldName", name).
				Str("newName", curName).
				Msg("UploadManager: could not apply rename/move after in-flight upload; delta sync will reconcile")
		} else {
			log.Info().
				Str("id", item.ID).
				Str("oldName", name).
				Str("newName", curName).
				Msg("UploadManager: applied rename/move after in-flight upload")
		}
	}

	session.setState(uploadComplete, nil)
}

// upload performs the upload with optimistic concurrency control and resolves
// an If-Match conflict (HTTP 412) using the "local wins, preserve remote"
// policy. It returns the uploaded DriveItem and whether a new remote item was
// created (true: local file or conflict re-upload) rather than an existing
// item overwritten in place (false).
func (um *UploadManager) upload(ctx context.Context, id string, isLocal bool, parentID, name string, openContent func() (io.Reader, int64, error), etag string) (*graph.DriveItem, bool, error) {
	reader, size, err := openContent()
	if err != nil {
		return nil, false, err
	}
	defer closeContentReader(reader)

	item, err := um.doUpload(ctx, id, isLocal, parentID, name, reader, size, etag)
	if err == nil {
		return item, isLocal, nil
	}

	// Conflict: the remote item changed since the ETag we sent was read. Only
	// existing (non-local) files send If-Match, so only they can get a 412.
	if !isLocal && errors.Is(err, graph.ErrPreconditionFailed) {
		return um.uploadAfterConflict(ctx, id, parentID, name, openContent)
	}

	return nil, false, err
}

// uploadAfterConflict applies the conflict policy: preserve the remote version
// under a `_conflict_<timestamp>` suffix, then upload the local content as a
// fresh item under the original name. Local always wins, but the remote copy
// is never lost.
func (um *UploadManager) uploadAfterConflict(ctx context.Context, id, parentID, name string, openContent func() (io.Reader, int64, error)) (*graph.DriveItem, bool, error) {
	conflicted := conflictName(name, time.Now())

	if _, err := um.graphClient.RenameItem(ctx, um.tokenProvider, graph.ItemID(id), conflicted, ""); err != nil {
		log.Warn().Err(err).
			Str("id", id).
			Str("conflictName", conflicted).
			Msg("UploadManager: could not preserve remote version under conflict suffix; uploading local copy anyway")
	} else {
		log.Info().
			Str("id", id).
			Str("conflictName", conflicted).
			Msg("UploadManager: remote conflict preserved under suffix")
	}

	reader, size, err := openContent()
	if err != nil {
		return nil, false, fmt.Errorf("conflict: re-open upload snapshot: %w", err)
	}
	defer closeContentReader(reader)

	item, err := um.doUpload(ctx, id, true, parentID, name, reader, size, "")
	if err != nil {
		return nil, false, fmt.Errorf("conflict: re-upload local version: %w", err)
	}
	return item, true, nil
}

// doUpload dispatches the actual upload: existing files are overwritten by ID
// (OverwriteItem/OverwriteItemStream, addressing the item's own /content
// endpoint), new files are created in their parent folder
// (UploadItem/UploadItemStream).
func (um *UploadManager) doUpload(ctx context.Context, id string, isLocal bool, parentID, name string, reader io.Reader, size int64, etag string) (*graph.DriveItem, error) {
	if !isLocal {
		if size <= 4*1024*1024 {
			return um.graphClient.OverwriteItem(ctx, um.tokenProvider, id, reader, etag)
		}
		return um.graphClient.OverwriteItemStream(ctx, um.tokenProvider, id, reader, size, etag)
	}

	resource := parentResource(parentID)
	if size <= 4*1024*1024 {
		return um.graphClient.UploadItem(ctx, um.tokenProvider, resource, name, reader, etag)
	}
	return um.graphClient.UploadItemStream(ctx, um.tokenProvider, resource, name, reader, size, etag)
}

// closeContentReader closes the reader if it owns a file descriptor. The
// streaming snapshot variant opens a fresh *os.File per attempt (issue #69),
// so the caller must release it once the upload attempt has consumed it.
func closeContentReader(r io.Reader) {
	if f, ok := r.(*os.File); ok {
		if err := f.Close(); err != nil {
			log.Warn().Err(err).Str("path", f.Name()).Msg("UploadManager: error closing upload snapshot")
		}
	}
}

// parentResource returns the Graph resource of the parent folder, used to
// create a new item inside it.
func parentResource(parentID string) graph.Resource {
	if parentID == "root" || parentID == "" {
		return graph.ItemPath("/")
	}
	return graph.ItemID(parentID)
}

// conflictName builds the name used to preserve a conflicting remote version:
// the base name gets a `_conflict_<timestamp>` suffix before the extension.
// The timestamp format is OneDrive-safe (no `:` or other forbidden chars).
func conflictName(name string, t time.Time) string {
	ext := ""
	base := name
	if i := strings.LastIndex(name, "."); i > 0 {
		base = name[:i]
		ext = name[i:]
	}
	return base + "_conflict_" + t.Format("2006-01-02_15-04-05") + ext
}

// applyInflightTargetChange renames and/or moves a just-uploaded item to the
// target name/parent that RenameSession recorded while the upload was running.
// Uses the real remote ID returned by the upload, so the rename is applied
// server-side exactly like a regular (non-local) rename.
func (um *UploadManager) applyInflightTargetChange(ctx context.Context, id, launchedName, curName, launchedParent, curParent string) error {
	if curName != launchedName {
		if _, err := um.graphClient.RenameItem(ctx, um.tokenProvider, graph.ItemID(id), curName, ""); err != nil {
			return fmt.Errorf("rename after in-flight upload: %w", err)
		}
	}
	if curParent != launchedParent {
		if _, err := um.graphClient.MoveItem(ctx, um.tokenProvider, graph.ItemID(id), graph.ItemID(curParent), ""); err != nil {
			return fmt.Errorf("move after in-flight upload: %w", err)
		}
	}
	return nil
}

// releaseInFlight frees one in-flight slot. Called via defer by
// executeUpload, so the slot is always released when an upload finishes
// (success or error). finishSession no longer decrements: a session can be
// removed from the map (dedupe, cancel) while its goroutine is still
// running, and only the goroutine owns its slot.
func (um *UploadManager) releaseInFlight() {
	um.mu.Lock()
	if um.inFlight > 0 {
		um.inFlight--
	}
	um.mu.Unlock()
}

// finishSession cleans up a completed/cancelled session from memory and disk.
func (um *UploadManager) finishSession(id string) {
	um.mu.Lock()
	defer um.mu.Unlock()

	session, exists := um.sessions[id]
	if !exists {
		return
	}

	delete(um.sessions, id)
	if session != nil {
		// Release the on-disk snapshot (streaming variant, issue #69).
		session.DiscardSnapshot()
	}

	// Clean up from BoltDB
	um.inodeCache.DeleteUploadSession(id)
}

// restoreIncompleteSessions loads incomplete sessions from BoltDB and
// re-enqueues them. Sessions that were in progress (uploading/errored)
// are marked as pending for retry.
func (um *UploadManager) restoreIncompleteSessions() {
	raw := um.inodeCache.LoadUploadSessions()
	if len(raw) == 0 {
		return
	}

	log.Info().Int("count", len(raw)).Msg("UploadManager: restoring incomplete sessions from disk")

	for id, data := range raw {
		session, err := NewUploadSessionJSON(data)
		if err != nil {
			log.Warn().Err(err).Str("id", id).Msg("UploadManager: error deserializing session, skipping")
			um.inodeCache.DeleteUploadSession(id)
			continue
		}

		// Incomplete sessions are re-enqueued
		state := session.getState()
		if state != uploadComplete {
			log.Info().Str("id", id).Str("name", session.Name).Msg("UploadManager: re-enqueueing incomplete session")
			session.setState(uploadPending, nil)
			um.mu.Lock()
			um.sessions[id] = session
			um.mu.Unlock()
		} else {
			// Already complete — clean up its snapshot and the BoltDB entry.
			session.DiscardSnapshot()
			um.inodeCache.DeleteUploadSession(id)
		}
	}
}

// ──── Helpers ────

// byteReader implements io.Reader over a []byte without an extra copy.
type byteReader struct {
	data []byte
	pos  int
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
