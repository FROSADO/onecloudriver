package fs

import (
	"context"
	"io"
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
// If the file is empty, it is not enqueued (nothing to upload).
func (um *UploadManager) QueueUpload(id, parentID, name string) {
	// Read content snapshot
	data := um.contentCache.ReadAll(id)
	if len(data) == 0 {
		// Distinguish "empty file" (OK) from "read error" (warning).
		// ReadAll returns nil both for nonexistent files and for
		// I/O errors. Check if the file exists in cache.
		if um.contentCache.HasContent(id) {
			log.Warn().Str("id", id).Str("name", name).Msg("UploadManager: ReadAll returned empty for existing file — possible I/O error")
		} else {
			log.Debug().Str("id", id).Str("name", name).Msg("UploadManager: empty or not found file, not enqueued")
		}
		return
	}

	session, err := NewUploadSession(id, parentID, name, data)
	if err != nil {
		log.Warn().Err(err).Str("id", id).Msg("UploadManager: error creating UploadSession")
		return
	}

	um.queue <- session
}

// CancelUpload removes any pending or in-flight upload for the given ID.
// It is called when a file is deleted (Unlink) while it was queued.
func (um *UploadManager) CancelUpload(id string) {
	um.deletionQueue <- id
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
	}

	um.sessions[session.ID] = session

	// Persist to BoltDB to survive restarts
	if data, err := session.AsJSON(); err == nil {
		um.inodeCache.SaveUploadSession(session.ID, data)
	}
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
func (um *UploadManager) executeUpload(session *UploadSession) {
	session.setState(uploadUploading, nil)

	id := session.ID
	isLocal := isLocalID(id)
	parentID := session.ParentID
	name := session.Name
	data := session.Data
	size := int64(len(data))

	ctx := context.Background()

	var resource graph.Resource
	if isLocal {
		// New file: upload to parent/name (creates the item)
		if parentID == "root" || parentID == "" {
			resource = graph.ItemPath("/")
		} else {
			resource = graph.ItemID(parentID)
		}
	} else {
		// Existing file: overwrite by ID (not by parent/name,
		// which would create a duplicate)
		resource = graph.ItemID(id)
	}

	var item *graph.DriveItem
	var err error

	if size <= 4*1024*1024 {
		// Small file: simple PUT
		item, err = um.graphClient.UploadItem(ctx, um.tokenProvider, resource, name, &byteReader{data: data})
	} else {
		// Large file: upload session with chunks
		item, err = um.graphClient.UploadItemStream(ctx, um.tokenProvider, resource, name, &byteReader{data: data}, size)
	}

	if err != nil {
		session.setState(uploadErrored, err)
		session.setTransient(isNetworkError(err))
		return
	}

	// Successful upload: update the inode in cache
	if isLocal {
		// First upload: swap local ID for the real ID
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
		// Overwrite of an existing file
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

	session.setState(uploadComplete, nil)
}

// finishSession cleans up a completed/cancelled session from memory and disk.
func (um *UploadManager) finishSession(id string) {
	um.mu.Lock()
	defer um.mu.Unlock()

	if _, exists := um.sessions[id]; !exists {
		return
	}

	delete(um.sessions, id)
	if um.inFlight > 0 {
		um.inFlight--
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
			// Already complete — just clean up from disk
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
