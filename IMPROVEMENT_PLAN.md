# Plan de Mejoras - OneCloudRiver FUSE

## Resumen Ejecutivo

El código base del proyecto OneCloudRiver es **sólido y profesional**, con una arquitectura bien pensada y testing exhaustivo. Las mejoras identificadas son principalmente de carácter incremental y no requieren reescritura mayor.

### Estado Actual

- **Archivos Go**: 138 archivos en el proyecto
- **Archivos críticos analizados**: 5 archivos principales en `internal/fs/`
- **Líneas totales en archivos principales**: 3,645 líneas
- **Issues de golint**: 4 warnings menores (documentación)
- **Issues de gofmt**: 0 (código bien formateado)
- **Tests**: Cobertura exhaustiva (unitarios, integración, benchmarks, fuzzing)

---

## Tarea #1: Corregir Comentarios de Documentación (golint)

**Prioridad**: ALTA  
**Dificultad**: BAJA  
**Tiempo estimado**: 15-30 minutos  
**Ideal para**: Principiantes en Go

### Descripción

El linter `golint` reporta 4 warnings sobre documentación faltante o incorrecta en tipos y métodos exportados. En Go, cualquier identificador que comienza con mayúscula es exportado (público) y debe tener un comentario de documentación que comience con el nombre del identador.

### Issues Detectados

```
./internal/fs/cache.go:648:1: comment on exported method InodeCache.InsertChild should be of the form "InsertChild ..."
./internal/fs/content_cache.go:32:6: exported type ContentCache should have comment or be unexported
./internal/fs/delta.go:39:6: exported type DeltaSync should have comment or be unexported
./internal/fs/root.go:70:1: exported method OneCloudFS.Statfs should have comment or be unexported
```

### Archivos a Modificar

1. **`internal/fs/cache.go`** (línea ~648)
2. **`internal/fs/content_cache.go`** (línea ~32)
3. **`internal/fs/delta.go`** (línea ~39)
4. **`internal/fs/root.go`** (línea ~70)

### Pasos Detallados

#### Paso 1: Ejecutar golint para ver los warnings actuales

```bash
# Instalar golint si no está disponible
go install golang.org/x/lint/golint@latest

# Ejecutar golint en los archivos afectados
export PATH=$PATH:$(go env GOPATH)/bin
golint ./internal/fs/cache.go ./internal/fs/content_cache.go ./internal/fs/delta.go ./internal/fs/root.go
```

#### Paso 2: Corregir `content_cache.go` - Añadir comentario al tipo `ContentCache`

**Ubicación**: Línea 32 de `internal/fs/content_cache.go`

**Código actual**:
```go
type ContentCache struct {
```

**Código corregido**:
```go
// ContentCache stores file content on disk as normal files,
// allowing zero-copy reads from FUSE. It implements age-based
// eviction when maxSize is exceeded (Phase 4b).
type ContentCache struct {
```

**Por qué**: Los tipos exportados deben tener un comentario que explique su propósito. El comentario debe comenzar con el nombre del tipo.

#### Paso 3: Corregir `delta.go` - Añadir comentario al tipo `DeltaSync`

**Ubicación**: Línea 39 de `internal/fs/delta.go`

**Código actual**:
```go
type DeltaSync struct {
```

**Código corregido**:
```go
// DeltaSync synchronizes remote changes (created, modified, deleted from
// other clients) with the local InodeCache tree using the Microsoft Graph
// delta endpoint with periodic polling.
type DeltaSync struct {
```

**Nota**: Ya existe un comentario antes del tipo, pero está separado por líneas en blanco. Golint requiere que el comentario esté inmediatamente antes del tipo.

#### Paso 4: Corregir `cache.go` - Reformatear comentario de `InsertChild`

**Ubicación**: Línea ~648 de `internal/fs/cache.go`

**Primero, ver el contexto actual**:
```bash
sed -n '645,655p' internal/fs/cache.go
```

**Código actual probable**:
```go
// Inserta un hijo en la caché
func (c *InodeCache) InsertChild(parentID, childID string) {
```

**Código corregido** (debe comenzar con el nombre del método):
```go
// InsertChild adds a child inode to the parent's children list in the cache.
// It is safe for concurrent use and updates the dirty set for persistence.
func (c *InodeCache) InsertChild(parentID, childID string) {
```

**Por qué**: Los comentarios de métodos exportados deben comenzar con el nombre del método en presente ("InsertChild adds..." no "Inserta...").

#### Paso 5: Corregir `root.go` - Añadir comentario a `Statfs`

**Ubicación**: Línea ~70 de `internal/fs/root.go`

**Código actual**:
```go
func (r *OneCloudFS) Statfs(_ context.Context, out *fuse.StatfsOut) syscall.Errno {
```

**Código corregido**:
```go
// Statfs returns filesystem statistics for the FUSE mount.
// It provides dummy values for blocks, files, and name length.
func (r *OneCloudFS) Statfs(_ context.Context, out *fuse.StatfsOut) syscall.Errno {
```

#### Paso 6: Verificar que se corrigieron todos los warnings

```bash
golint ./internal/fs/*.go 2>&1 | grep -v "_test.go"
```

**Resultado esperado**: Sin output (cero warnings)

### Criterios de Aceptación

- [ ] `golint ./internal/fs/*.go` no reporta warnings en archivos de producción (solo _test.go)
- [ ] Todos los comentarios comienzan con el nombre del identificador
- [ ] Los comentarios están en inglés (convención del proyecto)
- [ ] `gofmt -d ./internal/fs/` no muestra cambios (el formato no se alteró)
- [ ] `go build ./...` compila sin errores
- [ ] `go test ./internal/fs/...` pasa todos los tests

### Recursos Educativos

- [Effective Go - Comments](https://golang.org/doc/effective_go#comments)
- [Go Doc Comments](https://go.dev/doc/comment)
- [Golint README](https://github.com/golang/lint)

### Notas

- Esta tarea es un "quick win": mejora inmediata con mínimo riesgo
- Los tests existentes garantizan que los cambios son solo cosméticos
- Es una buena oportunidad para familiarizarse con la estructura del código

**Nota importante**: El método `InsertChild` en `cache.go` ya tiene documentación, pero golint requiere que el comentario comience exactamente con "InsertChild" (el nombre del método). El comentario actual comienza con "Insert", por eso el warning.

---

## Tarea #2: Refactorizar Función Mount() en Funciones Más Pequeñas

**Prioridad**: ALTA  
**Dificultad**: MEDIA  
**Tiempo estimado**: 2-3 horas  
**Ideal para**: Desarrolladores intermedios en Go

### Descripción

La función `Mount()` en `internal/fs/mount.go` tiene 499 líneas y múltiples responsabilidades. Según el principio de responsabilidad única (SRP), debe dividirse en funciones más pequeñas y cohesivas. Esto mejorará:
- **Legibilidad**: Cada función tendrá un propósito claro
- **Testabilidad**: Funciones pequeñas son más fáciles de testear unitariamente
- **Mantenibilidad**: Cambios futuros afectan áreas específicas

### Análisis Actual de Mount()

La función actual realiza estas operaciones (líneas aproximadas):
1. Validación del mountpoint (305-309)
2. Creación del cliente Graph (312-320)
3. Health check (324-326)
4. Inicialización de ContentCache (328-331)
5. Inicialización de InodeCache (334-353)
6. Inicialización de UploadManager (356)
7. Inicialización de DeltaSync (359-370)
8. Setup del debug server (375-389)
9. Pre-warming de caché (395-417)
10. Creación de OneCloudFS (419)
11. Configuración de opciones FUSE (421-435)
12. Montaje FUSE (437-442)
13. Setup del signal handler (447-494)
14. Retorno de CacheHandles (498)

### Archivos a Modificar

- **`internal/fs/mount.go`**: Dividir la función `Mount()`

### Pasos Detallados

#### Paso 1: Identificar las funciones a extraer

Crear las siguientes funciones privadas:

```go
// initializeCaches crea y configura ContentCache, InodeCache, UploadManager y DeltaSync
func initializeCaches(config MountConfig, account *auth.Account, graphClient *graph.Client) (*ContentCache, *InodeCache, *UploadManager, *DeltaSync, context.CancelFunc, error)

// startDebugServer inicia el servidor HTTP de debug si está configurado
func startDebugServer(addr string, inodeCache *InodeCache, contentCache *ContentCache, uploadManager *UploadManager, deltaSync *DeltaSync)

// startPreWarm inicia el pre-warming asíncrono de metadatos
func startPreWarm(ctx context.Context, inodeCache *InodeCache, graphClient *graph.Client, account *auth.Account, depth int)

// setupSignalHandler configura el manejo de señales para unmount limpio
func setupSignalHandler(server *fs.Server, cancelDelta context.CancelFunc, deltaSync *DeltaSync, uploadManager *UploadManager, inodeCache *InodeCache, contentCache *ContentCache, mountpoint string)
```

#### Paso 2: Extraer inicialización de caches

**Código a extraer** (líneas 328-370):

```go
func initializeCaches(config MountConfig, account *auth.Account, graphClient *graph.Client) (
    *ContentCache, *InodeCache, *UploadManager, *DeltaSync, context.CancelFunc, error) {
    
    // ContentCache
    contentCache, err := NewContentCache(filepath.Join(config.CacheDir, "content"))
    if err != nil {
        return nil, nil, nil, nil, nil, fmt.Errorf("error creating ContentCache: %w", err)
    }

    // InodeCache
    inodeCache := NewInodeCache()
    inodeCache.SetBaseTTL(config.CacheTTL)
    inodeCache.SetMaxEntries(config.CacheMaxEntries)
    contentCache.SetMaxSize(config.CacheMaxSize)
    inodeCache.StartSweep()

    // BoltDB initialization
    boltDBPath := filepath.Join(config.CacheDir, "inodes.db")
    if err := inodeCache.InitBoltDB(boltDBPath); err != nil {
        log.Printf("%s Could not initialize BoltDB at %s: %v", printer.Warning, boltDBPath, err)
    }

    // UploadManager
    uploadManager := NewUploadManager(graphClient, account, inodeCache, contentCache, 
        config.MaxUploadsInFlight, config.MaxUploadRetries)

    // DeltaSync
    deltaInterval := config.DeltaInterval
    if deltaInterval <= 0 {
        deltaInterval = 5 * time.Minute
    }
    ctx, cancelDelta := context.WithCancel(context.Background())
    deltaSync := NewDeltaSync(graphClient, account, inodeCache, contentCache)
    deltaSync.SetUploadQuery(uploadManager)
    deltaSync.Start(ctx, deltaInterval)

    uploadManager.Start()

    return contentCache, inodeCache, uploadManager, deltaSync, cancelDelta, nil
}
```

#### Paso 3: Extraer setup del debug server

**Código a extraer** (líneas 375-389):

```go
func startDebugServer(addr string, inodeCache *InodeCache, contentCache *ContentCache, 
    uploadManager *UploadManager, deltaSync *DeltaSync) {
    
    obs.Register("cache_hits", func() any { return inodeCache.Stats().Hits })
    obs.Register("cache_misses", func() any { return inodeCache.Stats().Misses })
    obs.Register("cache_evictions", func() any { return inodeCache.Stats().Evictions })
    obs.Register("inode_count", func() any { return inodeCache.Stats().InodeCount })
    obs.Register("content_cache_total_size", func() any { return contentCache.TotalSize() })
    obs.Register("uploads_in_flight", func() any { return uploadManager.InFlight() })
    obs.Register("uploads_completed", func() any { c, _ := uploadManager.Metrics(); return c })
    obs.Register("uploads_failed", func() any { _, f := uploadManager.Metrics(); return f })
    obs.Register("delta_sync_count", func() any { c, _ := deltaSync.Counters(); return c })
    obs.Register("delta_error_count", func() any { _, e := deltaSync.Counters(); return e })
    
    if _, _, err := obs.StartDebugServer(addr); err != nil {
        zlog.Warn().Err(err).Str("addr", addr).Msg("Debug server: could not start; continuing without it")
    }
}
```

#### Paso 4: Extraer pre-warming

**Código a extraer** (líneas 395-417):

```go
func startPreWarm(ctx context.Context, inodeCache *InodeCache, graphClient *graph.Client, 
    account *auth.Account, depth int) {
    
    go func() {
        preWarmCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
        defer cancel()

        fetcher := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
            if parentID == "root" || parentID == "" {
                return graphClient.ListDriveRoot(ctx, account)
            }
            return graphClient.ListChildren(ctx, account, graph.ItemID(parentID))
        }

        if err := preWarm(preWarmCtx, inodeCache, fetcher, depth); err != nil {
            zlog.Debug().Err(err).Int("depth", depth).Msg("preWarm: async metadata pre-warm completed with error")
        } else {
            zlog.Debug().Int("depth", depth).Msg("preWarm: async metadata pre-warm completed successfully")
        }
    }()
}
```

#### Paso 5: Extraer signal handler

**Código a extraer** (líneas 447-494):

```go
func setupSignalHandler(server *fs.Server, cancelDelta context.CancelFunc, deltaSync *DeltaSync, 
    uploadManager *UploadManager, inodeCache *InodeCache, contentCache *ContentCache, mountpoint string) {
    
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

    go func() {
        <-sigChan
        log.Println("\n" + printer.Stop + " Interrupt signal received. Unmounting filesystem...")

        cancelDelta()
        deltaSync.Stop()
        uploadManager.Stop()

        // Persist dirty inodes
        if err := inodeCache.SerializeDirty(); err != nil {
            log.Printf("%s Error persisting dirty inode cache: %v", printer.Warning, err)
        }
        if err := inodeCache.SerializeAll(); err != nil {
            log.Printf("%s Error persisting inode cache: %v", printer.Warning, err)
        }

        // Unmount
        unmounted := false
        if err := server.Unmount(); err == nil {
            log.Println(printer.Success, "Filesystem unmounted successfully.")
            unmounted = true
        } else {
            log.Println(printer.Warning, "Normal unmount failed (file explorer open?). Trying lazy-unmount...")
            if err := exec.Command("fusermount3", "-u", "-z", mountpoint).Run(); err != nil {
                log.Printf("%s Lazy-unmount also failed: %v. Forcing exit...", printer.Error, err)
            } else {
                log.Println(printer.Success, "Lazy-unmount executed. The kernel will unmount when the resource is released.")
                unmounted = true
            }
        }

        contentCache.CloseAll()
        if err := inodeCache.Close(); err != nil {
            log.Printf("%s Error closing BoltDB: %v", printer.Warning, err)
        }

        if unmounted {
            log.Println("Goodbye!")
        }

        time.Sleep(100 * time.Millisecond)
        os.Exit(0)
    }()
}
```

#### Paso 6: Refactorizar Mount() para usar las nuevas funciones

**Nueva implementación de Mount()**:

```go
func Mount(mountpoint string, account *auth.Account, config MountConfig) (*CacheHandles, error) {
    // Validate mountpoint
    if info, err := os.Stat(mountpoint); err != nil {
        return nil, fmt.Errorf("mount point '%s' does not exist", mountpoint)
    } else if !info.IsDir() {
        return nil, fmt.Errorf("mount point '%s' is not a directory", mountpoint)
    }

    // Build Graph client
    var graphOpts []graph.Option
    if config.HTTPTimeout > 0 {
        graphOpts = append(graphOpts, graph.WithTimeout(config.HTTPTimeout))
    }
    graphClient := graph.NewClient(graphOpts...)
    if config.GraphRetries > 0 {
        graphClient.HTTPClient = graph.NewRetryDoer(graphClient.HTTPClient, config.GraphRetries)
    }

    // Health check
    if err := healthCheck(context.Background(), account, graphClient); err != nil {
        return nil, err
    }

    // Initialize caches
    contentCache, inodeCache, uploadManager, deltaSync, cancelDelta, err := 
        initializeCaches(config, account, graphClient)
    if err != nil {
        return nil, err
    }

    // Register deferred cleanup
    defer func() {
        if err := inodeCache.Close(); err != nil {
            log.Printf("%s Error closing inode cache during cleanup: %v", printer.Warning, err)
        }
    }()

    // Start debug server if enabled
    if config.DebugAddr != "" {
        startDebugServer(config.DebugAddr, inodeCache, contentCache, uploadManager, deltaSync)
    }

    // Start pre-warming
    if config.PreWarmDepth > 0 {
        startPreWarm(context.Background(), inodeCache, graphClient, account, config.PreWarmDepth)
    }

    // Create FUSE root
    root := NewOneCloudFS(graphClient, account, inodeCache, contentCache, uploadManager)

    // Mount FUSE
    opts := &fs.Options{
        MountOptions: fuse.MountOptions{
            DirectMountStrict: false,
            Options:           []string{"rw"},
            PanicHandler:      handleFSPanic,
            MaxInflightRequestBytes: 16 << 20,
        },
    }

    server, err := fs.Mount(mountpoint, root, opts)
    if err != nil {
        cancelDelta()
        deltaSync.Stop()
        return nil, fmt.Errorf("error mounting FUSE: %w", err)
    }

    log.Printf("%s Filesystem mounted successfully at: %s", printer.Success, mountpoint)
    log.Println("Press Ctrl+C to unmount and exit safely.")

    // Setup signal handler
    setupSignalHandler(server, cancelDelta, deltaSync, uploadManager, inodeCache, contentCache, mountpoint)

    server.Wait()

    return &CacheHandles{Metadata: inodeCache, Content: contentCache, Delta: deltaSync, Uploads: uploadManager}, nil
}
```

#### Paso 7: Verificar compilación y tests

```bash
# Compilar
go build ./...

# Ejecutar tests
go test ./internal/fs/... -v

# Verificar formato
gofmt -d ./internal/fs/mount.go
```

### Criterios de Aceptación

- [ ] La función `Mount()` tiene menos de 150 líneas (actualmente 499)
- [ ] Cada función extraída tiene un único propósito claro
- [ ] Todas las funciones tienen comentarios de documentación
- [ ] `go build ./...` compila sin errores
- [ ] `go test ./internal/fs/...` pasa todos los tests
- [ ] No hay regresiones en la funcionalidad de mount/unmount
- [ ] El comportamiento de signal handling es idéntico al original

### Recursos Educativos

- [Clean Code - Functions](https://blog.cleancoder.com/)
- [Go Best Practices - Functions](https://github.com/golang/go/wiki/CodeReviewComments#functions)
- [Refactoring Guru - Extract Function](https://refactoring.guru/extract-function)

### Riesgos y Mitigaciones

**Riesgo**: Errores en el manejo de señales durante unmount  
**Mitigación**: Los tests de integración `mount_test.go` verifican el comportamiento

**Riesgo**: Regresión en la inicialización de componentes  
**Mitigación**: Tests existentes de `cache_test.go`, `upload_manager_test.go`, `delta_test.go`

---

## Tarea #3: Dividir cache.go en Múltiples Archivos por Responsabilidad

**Prioridad**: MEDIA  
**Dificultad**: ALTA  
**Tiempo estimado**: 4-5 horas  
**Ideal para**: Desarrolladores avanzados en Go

### Descripción

El archivo `internal/fs/cache.go` tiene 1,304 líneas y maneja múltiples responsabilidades. Debe dividirse en archivos más pequeños organizados por funcionalidad para mejorar la navegación y el mantenimiento.

### Estructura Propuesta

```
internal/fs/
├── cache.go              → Estructura principal y métodos core (~300 líneas)
├── cache_fetch.go        → Fetching de children y delta link (~250 líneas)
├── cache_persistence.go  → BoltDB: init, serialize, deserialize (~350 líneas)
├── cache_eviction.go     → TTL, LFU, sweep background (~250 líneas)
└── cache_helpers.go      → Funciones auxiliares y stats (~150 líneas)
```

### Archivos a Modificar

- **Crear**: `internal/fs/cache_fetch.go`
- **Crear**: `internal/fs/cache_persistence.go`
- **Crear**: `internal/fs/cache_eviction.go`
- **Crear**: `internal/fs/cache_helpers.go`
- **Modificar**: `internal/fs/cache.go` (reducir a ~300 líneas)

### Pasos Detallados

#### Paso 1: Identificar grupos de funciones

**cache.go actual contiene:**
- Líneas 1-130: Constantes, tipos, struct InodeCache
- Líneas 131-300: Métodos Get, GetChildren, AttachChild
- Líneas 301-450: Children fetching y delta link
- Líneas 451-700: BoltDB persistence (InitBoltDB, SerializeAll, etc.)
- Líneas 701-900: Eviction (effectiveTTL, sweep, StartSweep)
- Líneas 901-1304: Helpers, stats, lifecycle

#### Paso 2: Extraer a cache_fetch.go

Mover las siguientes funciones:
- `GetChildren()` - Obtiene children con fetch lazy
- `PrefetchChildren()` - Precarga sin marcar dirty
- `fetchChildrenFromGraph()` - Llama al Graph API
- `deserializeDriveItem()` - Convierte DriveItem a Inode
- `getDeltaLink()` / `setDeltaLink()` - Persistencia del delta link

#### Paso 3: Extraer a cache_persistence.go

Mover las siguientes funciones:
- `InitBoltDB()` - Inicializa base de datos
- `SerializeAll()` - Serializa todo el árbol
- `SerializeDirty()` - Serializa solo nodos modificados
- `loadFromBoltDB()` - Carga desde BoltDB
- `saveToBoltDB()` - Guarda en BoltDB
- `Close()` - Cierra BoltDB
- `markDirty()`, `markClean()` - Tracking de dirty nodes

#### Paso 4: Extraer a cache_eviction.go

Mover las siguientes funciones:
- `effectiveTTL()` - Calcula TTL basado en frecuencia
- `StartSweep()` - Inicia goroutine de eviction
- `sweep()` - Ejecuta una pasada de eviction
- `evictStaleNodes()` - Elimina nodos expirados
- `SetBaseTTL()`, `SetMaxEntries()` - Configuración

#### Paso 5: Extraer a cache_helpers.go

Mover las siguientes funciones:
- `Stats()` - Retorna estadísticas
- `Count()` - Cuenta inodos
- `Get()` - Getter simple
- `attachChild()`, `detachChild()` - Helpers de árbol
- `isLocalID()` - Verifica si es ID local

#### Paso 6: Mantener en cache.go

Quedan en cache.go:
- Constantes (`freqGrowthRate`, `freqMultiplierMax`, `sweepInterval`)
- Tipos (`InodeCache`, `ChildrenFetcher`)
- Constructor `NewInodeCache()`
- Métodos core mínimos

#### Paso 7: Verificar compilación y tests

```bash
go build ./...
go test ./internal/fs/... -v
gofmt -d ./internal/fs/cache*.go
```

### Criterios de Aceptación

- [ ] `cache.go` tiene menos de 400 líneas
- [ ] Cada archivo nuevo tiene <400 líneas
- [ ] Todos los tests pasan sin modificaciones
- [ ] No hay código duplicado entre archivos
- [ ] Cada archivo tiene un comentario de paquete

### Riesgos y Mitigaciones

**Riesgo**: Errores de importación circular  
**Mitigación**: Mantener todas las funciones en el mismo package `fs`

---

## Tarea #4: Extraer Constantes de DefaultMountConfig

**Prioridad**: BAJA  
**Dificultad**: BAJA  
**Tiempo estimado**: 30 minutos  
**Ideal para**: Principiantes en Go

### Descripción

La función `DefaultMountConfig()` contiene magic numbers que deben convertirse en constantes exportadas para mejorar la legibilidad y permitir su reuso.

### Magic Numbers Identificados

```go
cfg := MountConfig{
    CacheDir:           filepath.Join(os.Getenv("HOME"), ".cache", "onecloudriver"),
    CacheTTL:           60 * time.Second,      // → DefaultCacheTTL
    CacheMaxEntries:    2000,                   // → DefaultCacheMaxEntries
    CacheMaxSize:       0,                      // → DefaultCacheMaxSize (0 = no limit)
    DeltaInterval:      5 * time.Minute,        // → DefaultDeltaInterval
    MaxUploadsInFlight: 5,                      // → DefaultMaxUploadsInFlight
    MaxUploadRetries:   5,                      // → DefaultMaxUploadRetries
    GraphRetries:       3,                      // → DefaultGraphRetries
    HTTPTimeout:        15 * time.Second,       // → DefaultHTTPTimeout
    PreWarmDepth:       2,                      // → DefaultPreWarmDepth
}
```

### Archivos a Modificar

- **`internal/fs/mount.go`**: Añadir constantes y usarlas en `DefaultMountConfig()`

### Pasos Detallados

#### Paso 1: Definir constantes al inicio del archivo

```go
// Default cache configuration constants
const (
    DefaultCacheTTL           = 60 * time.Second
    DefaultCacheMaxEntries    = 2000
    DefaultCacheMaxSize       = int64(0) // 0 = no limit
    DefaultDeltaInterval      = 5 * time.Minute
    DefaultMaxUploadsInFlight = 5
    DefaultMaxUploadRetries   = 5
    DefaultGraphRetries       = 3
    DefaultHTTPTimeout        = 15 * time.Second
    DefaultPreWarmDepth       = 2
)

// Default cache directory base path
const DefaultCacheBaseDir = "~/.cache/onecloudriver"
```

#### Paso 2: Actualizar DefaultMountConfig()

```go
func DefaultMountConfig(accountName string, persisted *auth.AccountPersistedConfig) MountConfig {
    cacheBase := filepath.Join(os.Getenv("HOME"), ".cache", "onecloudriver")

    cfg := MountConfig{
        CacheDir:           filepath.Join(cacheBase, accountName),
        CacheTTL:           DefaultCacheTTL,
        CacheMaxEntries:    DefaultCacheMaxEntries,
        CacheMaxSize:       DefaultCacheMaxSize,
        DeltaInterval:      DefaultDeltaInterval,
        MaxUploadsInFlight: DefaultMaxUploadsInFlight,
        MaxUploadRetries:   DefaultMaxUploadRetries,
        GraphRetries:       DefaultGraphRetries,
        HTTPTimeout:        DefaultHTTPTimeout,
        PreWarmDepth:       DefaultPreWarmDepth,
    }
    // ... resto de la función
}
```

#### Paso 3: Exportar constantes en documentación

Añadir comentarios de documentación a cada constante para que aparezcan en `go doc`.

### Criterios de Aceptación

- [ ] Todas las constantes están definidas y documentadas
- [ ] `DefaultMountConfig()` usa las constantes
- [ ] `go doc fs.DefaultCacheTTL` muestra documentación
- [ ] Tests pasan sin cambios

---

## Tarea #5: Crear Tipo Struct para Parámetros de Configuración Avanzada

**Prioridad**: BAJA  
**Dificultad**: MEDIA  
**Tiempo estimado**: 1 hora  
**Ideal para**: Desarrolladores intermedios

### Descripción

Los parámetros avanzados de configuración están dispersos en `MountConfig`. Crear un struct anidado `AdvancedConfig` mejora la organización.

### Cambios Propuestos

```go
// AdvancedConfig holds advanced tuning parameters typically loaded from
// AccountPersistedConfig. Most users don't need to modify these.
type AdvancedConfig struct {
    DeltaInterval      time.Duration // Polling interval for /delta endpoint
    MaxUploadsInFlight int         // Concurrent uploads limit
    MaxUploadRetries   int         // Retries per upload
    GraphRetries       int         // HTTP retries on 429/503
    HTTPTimeout        time.Duration // HTTP client timeout
    PreWarmDepth       int         // BFS depth for metadata pre-warming
}

// MountConfig groups the cache configuration...
type MountConfig struct {
    CacheDir        string
    CacheTTL        time.Duration
    CacheMaxEntries int
    CacheMaxSize    int64
    Advanced        AdvancedConfig // ← Nuevo campo anidado
    DebugAddr       string
}
```

### Archivos a Modificar

- **`internal/fs/mount.go`**: Añadir tipo `AdvancedConfig` y refactorizar `MountConfig`

### Criterios de Aceptación

- [ ] Nuevo tipo `AdvancedConfig` definido y documentado
- [ ] `MountConfig` usa `AdvancedConfig` como campo anidado
- [ ] Código que accede a estos campos actualizado (ej: `config.Advanced.DeltaInterval`)
- [ ] Tests actualizados y pasando

---

## Tarea #6: Mejorar Manejo de Errores en InitBoltDB

**Prioridad**: BAJA  
**Dificultad**: BAJA  
**Tiempo estimado**: 30 minutos  

### Descripción

Actualmente el error de `InitBoltDB()` se loguea pero se ignora. Debe evaluarse si debe ser fatal o manejado explícitamente.

### Código Actual

```go
if err := inodeCache.InitBoltDB(boltDBPath); err != nil {
    log.Printf("%s Could not initialize BoltDB at %s: %v. The cache will not persist across restarts.", printer.Warning, boltDBPath, err)
}
```

### Opciones de Mejora

**Opción A**: Hacerlo configurable (flag `--require-persistence`)

**Opción B**: Retornar warning en CacheHandles para que UI lo muestre

**Opción C**: Documentar explícitamente que es non-fatal

### Recomendación

Implementar Opción C: Añadir comentario explicativo y posiblemente un campo `PersistenceAvailable bool` en `CacheHandles`.

---

## Tarea #7: Añadir Tests Unitarios para Funciones Extraídas de Mount()

**Prioridad**: MEDIA  
**Dificultad**: MEDIA  
**Tiempo estimado**: 2 horas  

### Descripción

Después de refactorizar `Mount()` (Tarea #2), añadir tests unitarios para las nuevas funciones extraídas.

### Funciones a Testear

1. `initializeCaches()` - Mockear account y graphClient
2. `startDebugServer()` - Verificar registro en obs
3. `startPreWarm()` - Verificar llamada a preWarm
4. `setupSignalHandler()` - Simular señal SIGINT

### Archivos a Crear

- **`internal/fs/mount_helpers_test.go`**: Tests para funciones auxiliares

### Criterios de Aceptación

- [ ] Cada función extraída tiene al menos un test
- [ ] Cobertura de `mount.go` > 80%
- [ ] Tests no dependen de FUSE real (mocks)

---

## Resumen de Prioridades

| Tarea | Prioridad | Dificultad | Tiempo | Impacto |
|-------|-----------|------------|--------|---------|
| #1: Corregir golint | ALTA | BAJA | 15 min | Legibilidad |
| #2: Refactorizar Mount() | ALTA | MEDIA | 2-3h | Mantenibilidad |
| #3: Dividir cache.go | MEDIA | ALTA | 4-5h | Navegabilidad |
| #4: Extraer constantes | BAJA | BAJA | 30 min | Legibilidad |
| #5: AdvancedConfig struct | BAJA | MEDIA | 1h | Organización |
| #6: Error handling BoltDB | BAJA | BAJA | 30 min | Robustez |
| #7: Tests funciones Mount() | MEDIA | MEDIA | 2h | Confiabilidad |

## Roadmap Recomendado

**Semana 1**: Tareas #1, #4 (quick wins)  
**Semana 2**: Tarea #2 (refactorización mayor)  
**Semana 3**: Tarea #7 (tests)  
**Semana 4-5**: Tarea #3 (división de cache.go)  
**Backlog**: Tareas #5, #6 (mejoras opcionales)

---

## Conclusión

El código base de OneCloudRiver es **sólido y profesional**. Las mejoras propuestas son incrementales y no requieren reescritura mayor. El testing exhaustivo existente proporciona una red de seguridad para realizar estas refactorizaciones con confianza.

Cada tarea está diseñada para ser:
- ✅ **Autocontenida**: Puede realizarse independientemente
- ✅ **Educativa**: Ideal para aprender patrones de Go
- ✅ **Verificable**: Con criterios claros de aceptación
- ✅ **Segura**: Con tests existentes que protegen contra regresiones
