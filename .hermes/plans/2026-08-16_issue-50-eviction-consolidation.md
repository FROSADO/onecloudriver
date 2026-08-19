# Issue #50: Consolidate Eviction Logic Implementation Plan

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task.

**Goal:** Unificar la lógica de eviction duplicada entre InodeCache (metadata) y ContentCache (content) en un módulo compartido `eviction.go`, eliminando código repetido mientras se preserva el comportamiento existente.

**Architecture:** Extraer el patrón común (goroutine lifecycle + dedup flag + mutex serialization) en un tipo `EvictionController` reutilizable. Cada cache mantiene su política de eviction (callbacks) pero delega el plumbing al controller.

**Tech Stack:** Go 1.21+, sync/atomic, sync.Mutex, zerolog

---

## Contexto Actual

### Duplicación Identificada

**InodeCache (cache.go):**
- `sweepMu sync.Mutex` + `stopCh chan struct{}` + `wg sync.WaitGroup`
- `evictions atomic.Uint64` (contador)
- `StartSweep()` → goroutine con ticker cada 30s
- `sweep()` → lock + ejecuta `evictExpiredChildren()` + `evictChildrenBySizeLimit()`
- Policy: TTL con decay + size limit con scoring (accessCount/minutos)

**ContentCache (content_cache.go):**
- `evictMu sync.Mutex` + `evicting atomic.Bool`
- `maxSize int64` + `totalSize atomic.Int64`
- `maybeEvict()` → goroutine con dedup flag
- `evictBySize()` → lock + elimina archivos por modTime (oldest first)
- TOCTOU: mutex protege entre file creation (Open) y eviction

### Diferencias Clave
1. **Trigger**: InodeCache usa ticker periódico; ContentCache se dispara tras cada write
2. **Policy**: InodeCache tiene 2 tiers (TTL + size); ContentCache solo size
3. **TOCTOU**: ContentCache necesita mutex compartido con Open(); InodeCache no
4. **Tracking**: InodeCache cuenta entries; ContentCache mide bytes

---

## Plan de Implementación

### Task 1: Crear EvictionController con lifecycle management

**Objective:** Extraer el patrón goroutine + stop channel + WaitGroup en un tipo reutilizable.

**Files:**
- Create: `internal/fs/eviction.go`
- Test: `internal/fs/eviction_test.go`

**Step 1: Write failing test for EvictionController**

```go
package fs

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestEvictionController_StartStop(t *testing.T) {
	var count atomic.Int64
	ctrl := NewEvictionController(10*time.Millisecond, func() {
		count.Add(1)
	})
	
	ctrl.Start()
	time.Sleep(25 * time.Millisecond)
	ctrl.Stop()
	
	// Debería haber ejecutado al menos 2 veces
	if count.Load() < 2 {
		t.Errorf("Expected at least 2 executions, got %d", count.Load())
	}
}

func TestEvictionController_StopIdempotent(t *testing.T) {
	ctrl := NewEvictionController(time.Second, func() {})
	ctrl.Start()
	ctrl.Stop()
	ctrl.Stop() // No debe panic
}
```

**Step 2: Run test to verify failure**

Run: `cd /home/fernando/workspace/onecloudriver && go test ./internal/fs -run TestEvictionController -v`
Expected: FAIL — `undefined: NewEvictionController`

**Step 3: Implement EvictionController**

```go
package fs

import (
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// EvictionController gestiona el lifecycle de un goroutine de eviction
// periódico con stop seguro y WaitGroup para shutdown graceful.
type EvictionController struct {
	interval time.Duration
	sweep    func()
	
	stopCh chan struct{}
	wg     sync.WaitGroup
	once   sync.Once // protege contra múltiples Start()
}

// NewEvictionController crea un controller con el intervalo y callback dados.
func NewEvictionController(interval time.Duration, sweep func()) *EvictionController {
	return &EvictionController{
		interval: interval,
		sweep:    sweep,
	}
}

// Start inicia el goroutine de sweep periódico. Idempotente (múltiples llamadas
// son seguras gracias a sync.Once).
func (c *EvictionController) Start() {
	c.once.Do(func() {
		c.stopCh = make(chan struct{})
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			ticker := time.NewTicker(c.interval)
			defer ticker.Stop()
			log.Trace().Dur("interval", c.interval).Msg("EvictionController: started")
			for {
				select {
				case <-c.stopCh:
					log.Trace().Msg("EvictionController: stopped")
					return
				case <-ticker.C:
					c.sweep()
				}
			}
		}()
	})
}

// Stop detiene el goroutine y espera a que termine. Idempotente.
func (c *EvictionController) Stop() {
	if c.stopCh != nil {
		select {
		case <-c.stopCh:
			// Ya cerrado
		default:
			close(c.stopCh)
			c.wg.Wait()
		}
	}
}
```

**Step 4: Run test to verify pass**

Run: `cd /home/fernando/workspace/onecloudriver && go test ./internal/fs -run TestEvictionController -v`
Expected: PASS — 2 tests

**Step 5: Commit**

```bash
git add internal/fs/eviction.go internal/fs/eviction_test.go
git commit -m "feat(fs): add EvictionController for shared goroutine lifecycle"
```

---

### Task 2: Agregar mutex serialization y dedup flag al EvictionController

**Objective:** Extender EvictionController con métodos para serializar eviction y prevenir ejecuciones concurrentes.

**Files:**
- Modify: `internal/fs/eviction.go`
- Modify: `internal/fs/eviction_test.go`

**Step 1: Write failing test for RunOnce (dedup)**

```go
func TestEvictionController_RunOnce_Dedup(t *testing.T) {
	var running atomic.Int64
	var maxConcurrent atomic.Int64
	
	ctrl := NewEvictionController(time.Hour, func() {})
	
	// Simula 10 goroutines intentando ejecutar concurrently
	for i := 0; i < 10; i++ {
		go func() {
			ctrl.RunOnce(func() {
				current := running.Add(1)
				// Track max concurrent executions
				for {
					old := maxConcurrent.Load()
					if current <= old {
						break
					}
					if maxConcurrent.CompareAndSwap(old, current) {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
				running.Add(-1)
			})
		}()
	}
	
	time.Sleep(50 * time.Millisecond)
	
	// Solo una ejecución debería haber corrido a la vez
	if maxConcurrent.Load() != 1 {
		t.Errorf("Expected max 1 concurrent execution, got %d", maxConcurrent.Load())
	}
}
```

**Step 2: Run test to verify failure**

Run: `cd /home/fernando/workspace/onecloudriver && go test ./internal/fs -run TestEvictionController_RunOnce -v`
Expected: FAIL — `ctrl.RunOnce undefined`

**Step 3: Add RunOnce method to EvictionController**

```go
// Añadir al struct EvictionController:
type EvictionController struct {
	// ... campos existentes ...
	
	// Para serialización y dedup
	mu      sync.Mutex
	running atomic.Bool
}

// RunOnce ejecuta fn con dedup flag: si ya hay una ejecución en vuelo,
// retorna inmediatamente sin ejecutar fn. Útil para maybeEvict() pattern.
func (c *EvictionController) RunOnce(fn func()) {
	if c.running.Swap(true) {
		return // Ya hay una ejecución en vuelo
	}
	defer c.running.Store(false)
	fn()
}

// RunSerialized ejecuta fn con mutex: serializa múltiples llamadas pero
// todas se ejecutan (a diferencia de RunOnce que descarta). Útil para
// sweep() periódico donde cada tick debe procesarse.
func (c *EvictionController) RunSerialized(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn()
}
```

**Step 4: Run test to verify pass**

Run: `cd /home/fernando/workspace/onecloudriver && go test ./internal/fs -run TestEvictionController_RunOnce -v`
Expected: PASS — 1 test

**Step 5: Commit**

```bash
git add internal/fs/eviction.go internal/fs/eviction_test.go
git commit -m "feat(fs): add RunOnce and RunSerialized to EvictionController"
```

---

### Task 3: Refactor InodeCache para usar EvictionController

**Objective:** Reemplazar los campos manuales de lifecycle en InodeCache con EvictionController.

**Files:**
- Modify: `internal/fs/cache.go:79-93` (struct fields)
- Modify: `internal/fs/cache.go:376-414` (StartSweep, sweep, ForceSweep)

**Step 1: Run existing tests to establish baseline**

Run: `cd /home/fernando/workspace/onecloudriver && go test ./internal/fs -run TestInodeCache -v`
Expected: PASS — todos los tests existentes

**Step 2: Refactor InodeCache struct**

```go
// En cache.go, reemplazar líneas 79-93:
type InodeCache struct {
	inodes sync.Map
	rootID string

	hits      atomic.Uint64
	misses    atomic.Uint64
	evictions atomic.Uint64

	db      *bolt.DB
	dbPath  string
	offline atomic.Bool

	maxEntries int
	baseTTL    time.Duration

	// Reemplazar sweepMu, stopCh, wg con:
	eviction *EvictionController

	closeMu sync.Mutex
}
```

**Step 3: Update NewInodeCache to create controller**

```go
func NewInodeCache() *InodeCache {
	c := &InodeCache{
		rootID:     "root",
		maxEntries: 2000,
		baseTTL:    60 * time.Second,
	}
	c.eviction = NewEvictionController(sweepInterval, c.sweep)
	return c
}
```

**Step 4: Refactor StartSweep, sweep, ForceSweep**

```go
func (c *InodeCache) StartSweep() {
	c.eviction.Start()
}

func (c *InodeCache) sweep() {
	c.eviction.RunSerialized(func() {
		c.evictExpiredChildren()
		c.evictChildrenBySizeLimit()
	})
}

func (c *InodeCache) ForceSweep() {
	c.sweep()
}
```

**Step 5: Update Close() to stop controller**

```go
// En el método Close(), añadir antes del close de stopCh:
c.eviction.Stop()
```

**Step 6: Run tests to verify behavior preserved**

Run: `cd /home/fernando/workspace/onecloudriver && go test ./internal/fs -run TestInodeCache -v`
Expected: PASS — todos los tests existentes (mismo comportamiento)

**Step 7: Commit**

```bash
git add internal/fs/cache.go
git commit -m "refactor(fs): InodeCache uses EvictionController for lifecycle"
```

---

### Task 4: Refactor ContentCache para usar EvictionController

**Objective:** Reemplazar los campos manuales de eviction en ContentCache con EvictionController.

**Files:**
- Modify: `internal/fs/content_cache.go:36-46` (struct fields)
- Modify: `internal/fs/content_cache.go:335-466` (evictBySize, maybeEvict, ForceEvict)

**Step 1: Run existing tests to establish baseline**

Run: `cd /home/fernando/workspace/onecloudriver && go test ./internal/fs -run TestContentCache -v`
Expected: PASS — todos los tests existentes

**Step 2: Refactor ContentCache struct**

```go
// En content_cache.go, reemplazar líneas 36-46:
type ContentCache struct {
	directory string
	fds       sync.Map

	maxSize   int64
	totalSize atomic.Int64

	// Reemplazar evictMu y evicting con:
	eviction *EvictionController

	closed atomic.Bool
}
```

**Step 3: Update NewContentCache to create controller**

```go
func NewContentCache(directory string) (*ContentCache, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	c := &ContentCache{directory: directory}
	// No usa ticker periódico, solo RunOnce tras writes
	c.eviction = NewEvictionController(0, nil)
	return c, nil
}
```

**Step 4: Refactor Open() para usar el mutex del controller**

```go
func (c *ContentCache) Open(id string) (*os.File, error) {
	if fd, ok := c.fds.Load(id); ok {
		f, _ := fd.(*os.File)
		return f, nil
	}

	// Usar el mutex del controller para TOCTOU protection
	c.eviction.mu.Lock()
	fd, err := os.OpenFile(c.contentPath(id), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		c.eviction.mu.Unlock()
		return nil, err
	}
	runtime.SetFinalizer(fd, nil)
	c.fds.Store(id, fd)
	c.eviction.mu.Unlock()

	return fd, nil
}
```

**Step 5: Refactor evictBySize, maybeEvict, ForceEvict**

```go
func (c *ContentCache) evictBySize() {
	c.eviction.RunSerialized(func() {
		// ... implementación existente sin cambios ...
		// (solo mover el cuerpo dentro del closure)
	})
}

func (c *ContentCache) maybeEvict() {
	if c.maxSize <= 0 {
		return
	}
	c.eviction.RunOnce(func() {
		c.evictBySize()
	})
}

func (c *ContentCache) ForceEvict() {
	c.eviction.RunSerialized(func() {
		c.evictBySize()
	})
}
```

**Step 6: Add cleanup to CloseAll**

```go
func (c *ContentCache) CloseAll() {
	c.closed.Store(true)
	c.eviction.Stop() // Por seguridad, aunque no usa ticker
	// ... resto del código existente ...
}
```

**Step 7: Run tests to verify behavior preserved**

Run: `cd /home/fernando/workspace/onecloudriver && go test ./internal/fs -run TestContentCache -v`
Expected: PASS — todos los tests existentes (mismo comportamiento)

**Step 8: Run race tests**

Run: `cd /home/fernando/workspace/onecloudriver && go test ./internal/fs -run TestContentCache -race -v`
Expected: PASS — sin race conditions

**Step 9: Commit**

```bash
git add internal/fs/content_cache.go
git commit -m "refactor(fs): ContentCache uses EvictionController for lifecycle"
```

---

### Task 5: Run full test suite with race detector

**Objective:** Verificar que todo el sistema funciona correctamente con -race.

**Files:** None (verification only)

**Step 1: Run all fs tests with race detector**

Run: `cd /home/fernando/workspace/onecloudriver && go test ./internal/fs -race -v`
Expected: PASS — todos los tests, sin race conditions

**Step 2: Run cache-specific tests**

Run: `cd /home/fernando/workspace/onecloudriver && go test ./internal/fs -run "TestInodeCache|TestContentCache|TestEvictionController" -race -v`
Expected: PASS — ~50+ tests

**Step 3: Run integration tests if they exist**

Run: `cd /home/fernando/workspace/onecloudriver && make test-unit`
Expected: PASS — full unit test suite

**Step 4: Commit (if any fixes needed)**

```bash
git add -A
git commit -m "test(fs): verify eviction consolidation with race detector"
```

---

### Task 6: Update documentation and comments

**Objective:** Documentar el nuevo EvictionController y actualizar comentarios en cache.go y content_cache.go.

**Files:**
- Modify: `internal/fs/eviction.go` (add package doc)
- Modify: `internal/fs/cache.go` (update struct comments)
- Modify: `internal/fs/content_cache.go` (update struct comments)

**Step 1: Add package documentation to eviction.go**

```go
// Package fs implements the FUSE filesystem for OneCloudDriver.
//
// EvictionController provides shared infrastructure for background eviction
// in both InodeCache (metadata) and ContentCache (content). It handles:
//   - Goroutine lifecycle (start/stop with WaitGroup)
//   - Mutex serialization (RunSerialized for periodic sweeps)
//   - Dedup flags (RunOnce for event-driven eviction)
//
// Each cache implements its own eviction policy (which items to evict, in what
// order) and delegates the plumbing to EvictionController.
package fs
```

**Step 2: Update InodeCache struct comment**

```go
// InodeCache is the global metadata cache. Stores *Inode in a sync.Map
// indexed by item ID. Each Inode knows its children via []string IDs.
//
// Faithful to onedriver's design (sync.Map + Inode tree), with this difference:
//   - BoltDB added in Phase 3 for persistence across restarts
//   - Background TTL+LFU eviction (Phase 4) via EvictionController
//   - ChildrenFetcher injected to decouple from graph.Client
//
// The key pattern is children nil = not initialized:
//   - nil  → never fetched → GetChildren calls the fetcher
//   - []string{} → fetched and empty → GetChildren returns empty without HTTP
//   - []string{"id1","id2"} → fetched with children → O(1) lookup
```

**Step 3: Update ContentCache struct comment**

```go
// ContentCache stores file content on disk as normal files,
// allowing zero-copy reads from FUSE.
//
// Uses EvictionController for shared eviction infrastructure (mutex, dedup).
// Eviction policy: age-based (oldest modTime first) when totalSize > maxSize.
//
// LoopbackCache in onedriver is a similar implementation, but it ignores
// errors from os.Mkdir and does not propagate them.
// This version explicitly propagates the error to the caller instead of ignoring it.
```

**Step 4: Commit**

```bash
git add internal/fs/eviction.go internal/fs/cache.go internal/fs/content_cache.go
git commit -m "docs(fs): document EvictionController and update cache comments"
```

---

### Task 7: Final verification and PR preparation

**Objective:** Ejecutar verificación completa y preparar el PR.

**Files:** None (verification only)

**Step 1: Run linter**

Run: `cd /home/fernando/workspace/onecloudriver && make lint-all`
Expected: PASS — sin nuevos warnings

**Step 2: Run build**

Run: `cd /home/fernando/workspace/onecloudriver && make build`
Expected: PASS — binario compila

**Step 3: Run full test suite**

Run: `cd /home/fernando/workspace/onecloudriver && make test-unit`
Expected: PASS — todos los tests unitarios

**Step 4: Create PR**

```bash
gh pr create --title "refactor(fs): consolidate eviction logic into shared EvictionController" \
  --body "Closes #50

## Summary
Extracted duplicated eviction infrastructure (goroutine lifecycle, mutex serialization, dedup flags) into a shared \`EvictionController\` type. Both \`InodeCache\` and \`ContentCache\` now use this controller while preserving their distinct eviction policies.

## Changes
- **New**: \`internal/fs/eviction.go\` — \`EvictionController\` with \`Start()\`, \`Stop()\`, \`RunSerialized()\`, \`RunOnce()\`
- **Refactored**: \`internal/fs/cache.go\` — \`InodeCache\` uses controller for periodic sweep
- **Refactored**: \`internal/fs/content_cache.go\` — \`ContentCache\` uses controller for event-driven eviction

## Testing
- All existing cache tests pass with \`-race\`
- No behavior changes: pure refactor
- Added tests for \`EvictionController\` lifecycle and dedup

## Benefits
- Eliminates ~40 lines of duplicated plumbing
- Single source of truth for goroutine lifecycle
- Consistent mutex/dedup patterns across both caches"
```

Expected: PR created successfully

---

## Risks & Tradeoffs

### Riesgos
1. **TOCTOU regression**: El mutex de ContentCache ahora está dentro de EvictionController. Si no se accede correctamente en Open(), se reintroduce la ventana TOCTOU.
2. **Behavioral drift**: Cambios sutiles en timing o locking podrían afectar performance.
3. **Test coverage**: Tests existentes deben cubrir todos los casos de eviction.

### Mitigaciones
- Task 4 Step 4: Acceso directo a `c.eviction.mu` en Open() mantiene la semántica TOCTOU
- Task 5: Race detector catcha cualquier regresión de concurrencia
- Refactor puramente mecánico: sin cambios de lógica, solo delegación

### Tradeoffs
- **Pro**: Código más limpio, un solo lugar para el patrón goroutine+mutex+dedup
- **Pro**: Más fácil añadir nuevas caches con eviction en el futuro
- **Contra**: Ligero overhead de indirección (despreciable en Go)
- **Contra**: Acoplamiento al EvictionController (pero es internal, no API pública)

---

## Verification Checklist

- [ ] Todos los tests existentes pasan sin modificación
- [ ] Race detector no reporta problemas
- [ ] Linter sin nuevos warnings
- [ ] Build exitoso
- [ ] Comentarios actualizados y claros
- [ ] PR description sigue template del proyecto
- [ ] Commit messages siguen Conventional Commits

---

## Open Questions

1. **¿Deberíamos exponer métricas de eviction?** El controller podría trackear cuántas veces se ejecutó cada método. Por ahora no, YAGNI.
2. **¿Configuración de intervalo?** InodeCache usa 30s fijo. ¿Debería ser configurable? Por ahora no, mantener simplicidad.
3. **¿Unificar políticas?** ¿Tiene sentido unificar TTL+LFU y age+size en una sola policy genérica? No, son dominios diferentes (metadata vs content).
