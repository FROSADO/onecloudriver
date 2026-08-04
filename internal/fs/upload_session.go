package fs

import (
	"encoding/json"
	"fmt"
	"sync"
)

// uploadState representa el estado de una subida en segundo plano.
type uploadState int

const (
	uploadPending   uploadState = iota // esperando ser procesada
	uploadUploading                    // subiendo activamente
	uploadComplete                     // subida exitosa
	uploadErrored                      // failed, pending retry
)

// maxRetries is the maximum number of retries before abandoning the upload.
const maxRetries = 5

// UploadSession contiene una snapshot de los datos a subir y el estado de la
// subida. Se persiste en BoltDB para sobrevivir a reinicios.
//
// La snapshot de contenido se toma al encolar la subida (QueueUpload), no al
// ejecutarla. Esto evita que modificaciones concurrentes del archivo
// corrompan la subida en curso.
type UploadSession struct {
	mu sync.Mutex

	// File data (immutable after creation)
	ID       string `json:"id"`       // ID actual del inode (puede ser local-xxx)
	ParentID string `json:"parentID"` // ID del padre
	Name     string `json:"name"`     // nombre del archivo

	// Snapshot del contenido (tomado al encolar)
	Data []byte `json:"data,omitempty"` // contenido a subir

	// Estado de la subida
	State   uploadState `json:"-"` // no se serializa directamente; se usa getState/setState
	Retries int         `json:"retries"`
	LastErr string      `json:"lastErr,omitempty"` // last error (for diagnostics)
}

// getState devuelve el estado actual de forma thread-safe.
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

// NewUploadSession crea una UploadSession a partir de los datos del inode y
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

// NewUploadSessionJSON reconstruye una UploadSession desde JSON.
func NewUploadSessionJSON(data []byte) (*UploadSession, error) {
	var us UploadSession
	if err := json.Unmarshal(data, &us); err != nil {
		return nil, err
	}
	return &us, nil
}
