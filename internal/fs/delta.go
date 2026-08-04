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

// DeltaSync sincroniza cambios remotos (creados, modificados, eliminados desde
// other clients) with the local InodeCache tree. Uses the Microsoft Graph
// delta endpoint with periodic polling.
//
// Fiel al DeltaLoop de onedriver, con estas diferencias:
//   - Injected as an independent service (DeltaSync) instead of a method
//     directo del filesystem, para mantener OneCloudFS enfocado en FUSE.
//   - Reconciliation of local items (isLocalID) uses InodeCache.MoveID
//     en vez del MoveID del onedriver original.
//   - The delta link is persisted via InodeCache.SetDeltaLink (BoltDB).
type DeltaSync struct {
	graphClient   *graph.Client
	tokenProvider types.TokenProvider
	inodeCache    *InodeCache
	contentCache  *ContentCache

	// Ciclo de vida
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

// Start inicia el polling delta en background con el intervalo especificado.
// Debe llamarse una sola vez. Para detenerlo, llamar a Stop().
func (d *DeltaSync) Start(ctx context.Context, interval time.Duration) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		log.Info().Dur("interval", interval).Msg("DeltaSync: iniciado")
		d.deltaLoop(ctx, interval)
	}()
}

// Stop detiene el polling delta y espera a que la goroutine termine.
func (d *DeltaSync) Stop() {
	select {
	case <-d.stopCh:
		// ya cerrado
	default:
		close(d.stopCh)
	}
	d.wg.Wait()
	log.Info().Msg("DeltaSync: detenido")
}

// deltaLoop es el bucle principal de polling delta.
// Inspirado en DeltaLoop de onedriver (docs/onedriverCode/fs/delta.go).
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
			log.Error().Err(err).Msg("DeltaSync: error durante delta fetch, entrando en modo offline")
			d.inodeCache.SetOffline(true)

			// Espera corta antes de reintentar en modo offline
			select {
			case <-d.stopCh:
				return
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}

		// Éxito: modo online, persistir
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
	pollSuccess := false

	// Pagination: continue while there is @odata.nextLink
	for {
		items, nextLink, cont, err := d.graphClient.PollDelta(ctx, d.tokenProvider, link)
		if err != nil {
			// Error de red: activar modo offline (no es un error fatal)
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
			log.Debug().Int("count", len(allItems)).Msg("DeltaSync: ciclo delta completado")
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

// applyDelta aplica un cambio remoto (delta) al estado local.
// Inspirado en applyDelta de onedriver.
//
// Casos que maneja:
//  1. Item eliminado → RemoveChild + Delete de ContentCache
//  2. Item nuevo (no existe localmente) → InsertChild
//  3. Item movido/renombrado → MoveChild + actualizar Name
//  4. Contenido modificado remotamente → invalidar ContentCache, actualizar metadatos
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

	// ──── Caso 1: Item eliminado ────
	if delta.Deleted != nil {
		if delta.IsFolder() && local != nil && local.HasChildren() {
			logger.Warn().Msg("DeltaSync: rejecting deletion of non-empty folder")
			return fmt.Errorf("directory is non-empty")
		}
		logger.Info().Msg("DeltaSync: applying remote deletion")
		d.inodeCache.RemoveChild(parentID, id)
		if err := d.contentCache.Delete(id); err != nil {
			logger.Warn().Err(err).Msg("DeltaSync: error limpiando ContentCache tras delete")
		}
		return nil
	}

	// ──── Caso 2: Item nuevo ────
	if local == nil {
		// Does it already exist locally with another ID? (cache-only, no HTTP call)
		existing, _ := d.inodeCache.GetChild(context.Background(), parentID, name, nil)
		if existing != nil && isLocalID(existing.ID()) {
			logger.Info().Str("localID", existing.ID()).Msg("DeltaSync: reconciliando item local con ID remoto")
			d.inodeCache.MoveID(existing.ID(), id)
			return nil
		}

		logger.Info().Msg("DeltaSync: creando inode desde delta")
		childInode := NewInodeDriveItem(&delta.DriveItem)
		d.inodeCache.InsertChild(parentID, name, childInode)
		return nil
	}

	// ──── Caso 3: Item movido/renombrado ────
	localName := local.Name()
	if local.ParentID() != parentID || local.Name() != name {
		logger.Info().
			Str("oldParent", local.ParentID()).
			Str("oldName", localName).
			Msg("DeltaSync: aplicando rename/move remoto")

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

	// ──── Caso 4: Contenido modificado remotamente ────
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
				logger.Warn().Err(err).Msg("DeltaSync: error limpiando ContentCache tras cambio remoto")
			}
		}
	}

	return nil
}
