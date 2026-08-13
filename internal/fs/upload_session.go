package fs

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// uploadState represents the state of a background upload.
type uploadState int

const (
	uploadPending   uploadState = iota // waiting to be processed
	uploadUploading                    // uploading actively
	uploadComplete                     // upload completed successfully
	uploadErrored                      // failed, pending retry
)

// UploadSession contains a snapshot of the data to upload and the upload
// state. It is persisted in BoltDB to survive restarts.
//
// The content snapshot is taken when enqueuing the upload (QueueUpload), not at
// execute it. This avoids concurrent modifications of the file
// corrupting the ongoing upload.
type UploadSession struct {
	mu sync.Mutex

	// File data (immutable after creation)
	ID       string `json:"id"`       // current inode ID (may be local-xxx)
	ParentID string `json:"parentID"` // parent ID
	Name     string `json:"name"`     // file name

	// Content snapshot (taken when enqueuing)
	Data []byte `json:"data,omitempty"` // content to upload

	// Upload state
	State   uploadState `json:"-"` // not serialized directly; getState/setState are used
	Retries int         `json:"retries"`
	LastErr string      `json:"lastErr,omitempty"` // last error (for diagnostics)

	// Transient reports whether the last upload error was a transient network
	// error (timeout, connection refused/reset, ...). A transient error never
	// abandons the session: it is retried (with backoff) until the connection
	// returns. Permanent errors (HTTP 4xx, ...) still count towards retries.
	Transient bool `json:"transient,omitempty"`

	// nextRetry is an in-memory backoff timestamp (not persisted). Zero means
	// "retry immediately".
	nextRetry time.Time
}

// getState returns the current state in a thread-safe way.
func (us *UploadSession) getState() uploadState {
	us.mu.Lock()
	defer us.mu.Unlock()
	return us.State
}

// setState updates the state and optionally the last error.
func (us *UploadSession) setState(state uploadState, err error) {
	us.mu.Lock()
	us.State = state
	if err != nil {
		us.LastErr = err.Error()
	}
	us.mu.Unlock()
}

// isTransient reports whether the last error was a transient network error.
func (us *UploadSession) isTransient() bool {
	us.mu.Lock()
	defer us.mu.Unlock()
	return us.Transient
}

// setTransient marks the session as having failed with a transient network
// error (true) or a permanent error (false).
func (us *UploadSession) setTransient(v bool) {
	us.mu.Lock()
	us.Transient = v
	us.mu.Unlock()
}

// getNextRetryAt returns the in-memory backoff timestamp (zero = retry now).
func (us *UploadSession) getNextRetryAt() time.Time {
	us.mu.Lock()
	defer us.mu.Unlock()
	return us.nextRetry
}

// setNextRetryAt schedules the next retry attempt.
func (us *UploadSession) setNextRetryAt(t time.Time) {
	us.mu.Lock()
	us.nextRetry = t
	us.mu.Unlock()
}

// NewUploadSession creates an UploadSession from the inode data and
// a snapshot of the content. The snapshot is taken here so the upload
// is atomic with respect to concurrent writes.
func NewUploadSession(id, parentID, name string, data []byte) (*UploadSession, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("the data to upload cannot be empty (id=%s, name=%s)", id, name)
	}
	return &UploadSession{
		ID:       id,
		ParentID: parentID,
		Name:     name,
		Data:     data,
		State:    uploadPending,
	}, nil
}

// AsJSON serializes the session to JSON for BoltDB persistence.
func (us *UploadSession) AsJSON() ([]byte, error) {
	us.mu.Lock()
	defer us.mu.Unlock()
	type serializable UploadSession
	return json.Marshal((*serializable)(us))
}

// NewUploadSessionJSON rebuilds an UploadSession from JSON.
func NewUploadSessionJSON(data []byte) (*UploadSession, error) {
	var us UploadSession
	if err := json.Unmarshal(data, &us); err != nil {
		return nil, err
	}
	return &us, nil
}
