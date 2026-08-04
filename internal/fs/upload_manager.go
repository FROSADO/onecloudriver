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

// maxUploadsInFlight limits the number of concurrent uploads to avoid
// server throttling and bandwidth saturation.
const maxUploadsInFlight = 5

// uploadTickerInterval es el intervalo del ticker que procesa la cola y
// lanza nuevas subidas. Copia del onedriver original (2s).
const uploadTickerInterval = 2 * time.Second

// UploadManager orquesta subidas pendientes en background con reintentos.
// Desacopla la escritura FUSE (Fsync) de la subida HTTP: Fsync solo marca
// hasChanges=false y encola; UploadManager se encarga del resto.
//
// Faithful to onedriver's design (docs/onedriverCode/fs/upload_manager.go),
// adaptado a nuestra arquitectura modular sin *Filesystem global.
type UploadManager struct {
	// Communication channels
	queue         chan *UploadSession // nuevas subidas
	deletionQueue chan string         // cancelaciones por delete

	// Estado interno
	mu       sync.Mutex
	sessions map[string]*UploadSession // id → active session
	inFlight uint8                     // subidas en curso (cap = maxUploadsInFlight)

	// Dependencias
	graphClient   *graph.Client
	tokenProvider types.TokenProvider
	inodeCache    *InodeCache   // para MoveID tras completar subida
	contentCache  *ContentCache // para leer/escribir contenido

	// Ciclo de vida
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewUploadManager crea un nuevo UploadManager y restaura sesiones
// incompletas desde BoltDB (si existen). Las sesiones restauradas que
// estaban en curso se cancelan (non-resumable, igual que onedriver).
func NewUploadManager(
	graphClient *graph.Client,
	tokenProvider types.TokenProvider,
	inodeCache *InodeCache,
	contentCache *ContentCache,
) *UploadManager {
	um := &UploadManager{
		queue:         make(chan *UploadSession, 100),
		deletionQueue: make(chan string, 100),
		sessions:      make(map[string]*UploadSession),
		graphClient:   graphClient,
		tokenProvider: tokenProvider,
		inodeCache:    inodeCache,
		contentCache:  contentCache,
		stopCh:        make(chan struct{}),
	}

	// Restaurar sesiones incompletas del disco (cierre abrupto previo)
	um.restoreIncompleteSessions()

	return um
}

// Start inicia el bucle de procesamiento en background.
func (um *UploadManager) Start() {
	um.wg.Add(1)
	go um.uploadLoop()
}

// Stop detiene gracefulmente el UploadManager. Espera a que termine el
// bucle y limpia las sesiones en memoria.
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
		log.Warn().Err(err).Str("id", id).Msg("UploadManager: error creando UploadSession")
		return
	}

	um.queue <- session
}

// CancelUpload elimina cualquier subida pendiente o en curso para el ID dado.
// Se llama cuando un archivo se elimina (Unlink) mientras estaba en cola.
func (um *UploadManager) CancelUpload(id string) {
	um.deletionQueue <- id
}

// ──── Bucle principal ────

// uploadLoop procesa la cola de subidas, maneja reintentos y completa/cancela
// sesiones. Los canales queue y deletionQueue se drenan antes de que el
// ticker lance nuevas subidas.
func (um *UploadManager) uploadLoop() {
	defer um.wg.Done()

	ticker := time.NewTicker(uploadTickerInterval)
	defer ticker.Stop()

	log.Info().Msg("UploadManager: bucle iniciado")

	for {
		select {
		case <-um.stopCh:
			log.Info().Msg("UploadManager: detenido")
			return

		case session := <-um.queue:
			// Drenar sesiones nuevas: deduplicar y persistir
			um.enqueueSession(session)

		case cancelID := <-um.deletionQueue:
			// Drenar cancelaciones
			um.finishSession(cancelID)

		case <-ticker.C:
			// Procesar sesiones activas: lanzar nuevas, reintentar fallidas,
			// completar exitosas
			um.processSessions()
		}
	}
}

// enqueueSession registers a new session, deduplicating if one already exists
// para el mismo ID, y la persiste en BoltDB.
func (um *UploadManager) enqueueSession(session *UploadSession) {
	um.mu.Lock()
	defer um.mu.Unlock()

	// Deduplicate: if there's already a session for this ID, cancel it first
	if old, exists := um.sessions[session.ID]; exists {
		log.Debug().Str("id", session.ID).Msg("UploadManager: deduplicating existing session")
		old.setState(uploadComplete, nil) // marcar como completa para que se limpie
	}

	um.sessions[session.ID] = session

	// Persistir a BoltDB para sobrevivir a reinicios
	if data, err := session.AsJSON(); err == nil {
		um.inodeCache.SaveUploadSession(session.ID, data)
	}
}

// processSessions iterates over all active sessions and decides what to do
// based on their state.
func (um *UploadManager) processSessions() {
	um.mu.Lock()
	// Copia local para no bloquear el mutex durante las subidas
	sessions := make(map[string]*UploadSession, len(um.sessions))
	for id, s := range um.sessions {
		sessions[id] = s
	}
	um.mu.Unlock()

	for _, session := range sessions {
		switch session.getState() {
		case uploadPending:
			// Is there capacity to launch a new upload?
			um.mu.Lock()
			if um.inFlight < maxUploadsInFlight {
				um.inFlight++
				um.mu.Unlock()
				go um.executeUpload(session)
			} else {
				um.mu.Unlock()
			}

		case uploadErrored:
			session.Retries++
			if session.Retries > maxRetries {
				log.Error().
					Str("id", session.ID).
					Str("name", session.Name).
					Int("retries", session.Retries).
					Str("lastErr", session.LastErr).
					Msg("UploadManager: demasiados reintentos, abandonando subida")
				um.finishSession(session.ID)
			} else {
				log.Warn().
					Str("id", session.ID).
					Str("name", session.Name).
					Int("retries", session.Retries).
					Msg("UploadManager: reintentando subida")
				session.setState(uploadPending, nil)

				// Intentar lanzar ahora si hay cupo
				um.mu.Lock()
				if um.inFlight < maxUploadsInFlight {
					um.inFlight++
					um.mu.Unlock()
					go um.executeUpload(session)
				} else {
					um.mu.Unlock()
				}
			}

		case uploadComplete:
			um.finishSession(session.ID)
		}
	}
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
		// Archivo nuevo: subir a parent/name (crea el item)
		if parentID == "root" || parentID == "" {
			resource = graph.ItemPath("/")
		} else {
			resource = graph.ItemID(parentID)
		}
	} else {
		// Archivo existente: sobrescribir por ID (no por parent/name,
		// which would create a duplicate)
		resource = graph.ItemID(id)
	}

	var item *graph.DriveItem
	var err error

	if size <= 4*1024*1024 {
		// Small file: simple PUT
		item, err = um.graphClient.UploadItem(ctx, um.tokenProvider, resource, name, &byteReader{data: data})
	} else {
		// Archivo grande: upload session con chunks
		item, err = um.graphClient.UploadItemStream(ctx, um.tokenProvider, resource, name, &byteReader{data: data}, size)
	}

	if err != nil {
		session.setState(uploadErrored, err)
		return
	}

	// Successful upload: update the inode in cache
	if isLocal {
		// Primera subida: intercambiar ID local por ID real
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
		// Sobrescritura de archivo existente
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

	// Limpiar de BoltDB
	um.inodeCache.DeleteUploadSession(id)
}

// restoreIncompleteSessions carga sesiones incompletas desde BoltDB y las
// vuelve a encolar. Las sesiones que estaban en curso (uploading/errored)
// se marcan como pending para reintento.
func (um *UploadManager) restoreIncompleteSessions() {
	raw := um.inodeCache.LoadUploadSessions()
	if len(raw) == 0 {
		return
	}

	log.Info().Int("count", len(raw)).Msg("UploadManager: restaurando sesiones incompletas desde disco")

	for id, data := range raw {
		session, err := NewUploadSessionJSON(data)
		if err != nil {
			log.Warn().Err(err).Str("id", id).Msg("UploadManager: error deserializing session, skipping")
			um.inodeCache.DeleteUploadSession(id)
			continue
		}

		// Las sesiones no completadas se vuelven a poner en cola
		state := session.getState()
		if state != uploadComplete {
			log.Info().Str("id", id).Str("name", session.Name).Msg("UploadManager: re-enqueueing incomplete session")
			session.setState(uploadPending, nil)
			um.mu.Lock()
			um.sessions[id] = session
			um.mu.Unlock()
		} else {
			// Ya estaba completa — solo limpiar del disco
			um.inodeCache.DeleteUploadSession(id)
		}
	}
}

// ──── Helpers ────

// byteReader implementa io.Reader sobre un []byte sin copia adicional.
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
