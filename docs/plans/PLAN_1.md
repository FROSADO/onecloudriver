# PLAN_1 — Mejora de Seguridad (Integridad de Datos) y Rendimiento

> **Estado**: Propuesto — pendiente de análisis manual.
> **Fecha**: 2026-08-14
> **Proyecto**: onecloudriver (FUSE filesystem for OneDrive on Linux)

---

## 1. Resumen Ejecutivo

**Objetivo**: Fortalecer la integridad de datos (cero corrupción en lectura/escritura)
y reducir la latencia percibida hasta que el usuario sienta que trabaja en local,
manteniendo la arquitectura actual.

**Alcance**: 4 fases, ~40 tareas atómicas, 0 breaking changes en la API pública.

**Principios rectores**:
- Seguridad por defecto (`NoSync=false`, validaciones estrictas de rutas).
- Rendimiento medible: benchmarks reproducibles antes/después (baseline).
- Cambios incrementales: cada commit verificable con `make test-unit-short`.

**Decisiones ya tomadas** (con el usuario):

| Tema | Decisión |
|------|----------|
| COW vs Lock por inode | **Lock por inode** — simple, memoria O(1), suficiente para uso desktop |
| BoltDB `NoSync` | **Mantener `false`** — integridad > throughput; optimizar con dirty tracking |
| DeltaSync per-file | **Intervalo global configurable** + comando de sync manual (más simple, mismo control) |
| Pre-warm | **Default `2`** niveles, configurable (`0` = desactivado) |
| DeltaSync streaming | **Sí** — mejora latencia de sync, riesgo bajo (idempotente) |
| Observabilidad | **Ligera** — logs JSON estructurados + `expvar` opcional. Sin Prometheus |
| Baseline benchmarks | Reproducibles en la máquina del usuario + CI (artifact) |
| Crash simulation | **CI primero** (GitHub Actions), fallback local |
| Fuzz time | **10s en CI, 60s local** |
| Pre-warm default | **2** (configurable `0=off, 1, 2, 3...`) |

---

## 2. Fase 1 — Integridad de Datos (Crítico)

> Objetivo: eliminar cualquier ventana de corrupción en ContentCache, UploadManager,
> DeltaSync y BoltDB.

### 2.1 ContentCache: Hardenning TOCTOU + Path Traversal

| Tarea | Archivo | Detalle |
|-------|---------|---------|
| 1.1.1 | `content_cache.go` | Auditoría `evictMu` cubre **todas** las rutas: `Open`, `Insert`, `InsertStream`, `WriteAt`, `Close`, `Delete`, `evictBySize` |
| 1.1.2 | `content_cache_test.go` | Test de carrera: `go test -race -count=50 -run TestContentCache_ConcurrentEviction` |
| 1.1.3 | `content_cache_fuzz_test.go` | Fuzz `contentPath(id)` con: `../../../etc/passwd`, `\x00`, Unicode normalization, case-folding, paths > 255 chars |
| 1.1.4 | `content_cache.go:74-95` | Añadir `filepath.IsLocal` **antes** de `hex.EncodeToString`; log `Warn` con `id` original para auditoría |
| 1.1.5 | `content_cache.go` | `Size(id)` usa `fd.Stat()` si está abierto, si no `os.Stat` — añadir `syncMu` para evitar race Stat vs WriteAt |

**Criterios de aceptación**:
- `go test -race -count=100 ./internal/fs/...` → 0 warnings.
- Fuzz `contentPath` 60s local / 10s CI → 0 crashes, 0 escapes del directorio cache.
- Cobertura de `contentPath` con casos adversariales documentada en el fuzz test.

### 2.2 UploadManager: Atomicidad del Snapshot

| Tarea | Archivo | Detalle |
|-------|---------|---------|
| 1.2.1 | `upload_manager.go:121-134` | `QueueUpload`: lock por inode (`inode.Lock()`) durante `ReadAll` + creación de sesión |
| 1.2.2 | `upload_manager.go` | Nuevo campo `uploadMu sync.Map[id]*sync.Mutex` para serializar `QueueUpload` vs `WriteAt` del mismo archivo |
| 1.2.3 | `upload_manager_test.go` | Test: write concurrente + Fsync + QueueUpload → snapshot consistente (hash previo == hash upload) |

**Criterios de aceptación**:
- El snapshot de contenido es siempre un punto consistente (nunca mitad de un write).
- Test de hash añadido y verde bajo `-race -count=50`.

### 2.3 DeltaSync: Prevención de Data Loss

| Tarea | Archivo | Detalle |
|-------|---------|---------|
| 1.3.1 | `delta.go:281-320` | Verificar ventana: `HasChanges()` true entre `Fsync` (local) y `QueueUpload` (remoto) — delta no debe pisar el cambio local |
| 1.3.2 | `delta.go` | Añadir `local.Uploading` flag (atómico) seteado en `QueueUpload`, limpiado al completar `executeUpload` |
| 1.3.3 | `delta_test.go` | Test de integración: write local → Fsync → delta llega antes del upload → verificar que la versión local gana |

**Criterios de aceptación**:
- Ninguna modificación local pendiente de upload se pierde por un delta remoto.
- Cobertura del caso "delta entre Fsync y upload" en tests.

### 2.4 BoltDB: Durabilidad + Consistencia

| Tarea | Archivo | Detalle |
|-------|---------|---------|
| 1.4.1 | `cache.go:736` | Confirmar `bolt.Options{NoSync: false, Timeout: 1}` explícito y documentar el porqué |
| 1.4.2 | `cache.go:783-832` | `SerializeAll`: dividir en batches de 500 inodes/transacción; log de progreso |
| 1.4.3 | `cache.go` | Añadir `dirtyInodes sync.Map[id]bool` — marcar dirty en `Insert`, `Delete`, `MoveChild`, `Invalidate`, `InsertChild`, `RemoveChild`, `MoveID` |
| 1.4.4 | `cache.go` | `SerializeDirty()`: solo persiste inodes marcados dirty; limpia flag tras commit exitoso |
| 1.4.5 | `cache_test.go` | Test crash: `kill -9` durante `SerializeAll` → remount → `DeserializeFromDisk` OK, árbol consistente |

**Criterios de aceptación**:
- 100 iteraciones de crash simulation → 0 errores BoltDB, 0 data loss.
- `SerializeDirty()` reduce los bytes escritos por delta sync ≥ 90% respecto al baseline.

---

## 3. Fase 2 — Rendimiento: Latencia Local

> Objetivo: que las operaciones FUSE (Lookup, Readdir, Read, Write) se sientan locales.

### 3.1 ContentCache: Eviction O(log n) con Heap

| Tarea | Archivo | Detalle |
|-------|---------|---------|
| 2.1.1 | `content_cache.go` | Nuevo `type evictEntry{id string; modTime time.Time; size int64}` + heap (implementa `container/heap`) |
| 2.1.2 | `content_cache.go:97-124` | `Open`/`Insert`/`InsertStream`/`WriteAt` → `heap.Push` del entry (si nuevo o modTime cambió) |
| 2.1.3 | `content_cache.go:345-438` | `evictBySize`: `for heap.Len()>0 && total>maxSize { pop → delete }` — elimina el `ReadDir` full-scan |
| 2.1.4 | `content_cache.go` | `Close`/`Delete` → `heap.Remove` (o lazy deletion con flag `deleted`) |
| 2.1.5 | `content_cache_bench_test.go` | Benchmark `BenchmarkEviction_10kFiles` — target < 1ms p99 |

**Criterios de aceptación**:
- Benchmark eviction con 10k ficheros < 1ms p99.
- Sin degradación en operaciones de escritura concurrentes.

### 3.2 InodeCache: Time-Bucketed TTL Sweep

| Tarea | Archivo | Detalle |
|-------|---------|---------|
| 2.2.1 | `cache.go` | Nuevo `ttlBuckets [][]string` — 60 buckets (1 por segundo); el inode va al bucket `now+TTL` |
| 2.2.2 | `cache.go:374-397` | `StartSweep`: ticker 1s → procesa solo el bucket actual → O(buckets expirados) vs O(todos los inodes) |
| 2.2.3 | `cache.go:416-445` | `evictExpiredChildren`: itera solo bucket(s) expirados; decay accessCount al mover de bucket |
| 2.2.4 | `cache.go:453-511` | `evictChildrenBySizeLimit`: mantener heap por score (como ContentCache) |
| 2.2.5 | `cache_bench_test.go` | Benchmark `BenchmarkSweep_50kInodes` — target < 5ms por tick |

**Criterios de aceptación**:
- Benchmark sweep con 50k inodes < 5ms por tick.
- Comportamiento de eviction idéntico al actual (mismo resultado funcional, distinto coste).

### 3.3 BoltDB: Dirty Tracking

| Tarea | Archivo | Detalle |
|-------|---------|---------|
| 2.3.1 | `cache.go` | `dirtyInodes sync.Map[id]bool` — set en `Insert`, `Delete`, `MoveChild`, `Invalidate`, `InsertChild`, `RemoveChild`, `MoveID` |
| 2.3.2 | `cache.go` | `SerializeDirty()`: itera solo dirty → batch 500 → limpia flags |
| 2.3.3 | `delta.go:134-136` | Reemplazar `SerializeAll()` por `SerializeDirty()` en el delta loop |
| 2.3.4 | `mount.go:275-277` | Unmount: `SerializeDirty()` + `SerializeAll()` final (garantía) |
| 2.3.5 | `cache_bench_test.go` | Benchmark: bytes escritos por delta sync — target reducción > 90% |

**Criterios de aceptación**:
- Los bytes escritos por delta sync < 10% del baseline.
- La persistencia final en unmount sigue garantizando el árbol completo.

### 3.4 UploadManager: Zero-Copy Snapshot

| Tarea | Archivo | Detalle |
|-------|---------|---------|
| 2.4.1 | `upload_manager.go:121-134` | `QueueUpload`: si `contentCache.IsOpen(id)` → `fd.Seek(0)` + `io.Copy` directo (evita el `ReadAll` alloc) |
| 2.4.2 | `upload_manager.go` | Si no está abierto → usar `InsertStream` existente (ya es streaming); mantener |
| 2.4.3 | `upload_manager_bench_test.go` | Benchmark: upload 100MB — target overhead < 100ms vs raw HTTP |

**Criterios de aceptación**:
- Upload de 100MB con overhead de CPU/memoria < 100ms.
- Sin copias adicionales de datos en el hot path de lectura.

### 3.5 DeltaSync: Streaming Apply

| Tarea | Archivo | Detalle |
|-------|---------|---------|
| 2.5.1 | `delta.go:153-200` | `pollAndApply`: procesa página → `applyDelta` inmediato → no acumula el map `allItems` en memoria |
| 2.5.2 | `delta.go` | Manejar dependencias: carpeta antes que hijos (Graph delta ya ordena) |
| 2.5.3 | `delta_test.go` | Test: drive 100k items → memoria acotada (< 50MB heap durante sync) |

**Criterios de aceptación**:
- Sync de 100k items con heap < 50MB.
- Fallo parcial: los items ya aplicados persisten; el siguiente poll completa lo restante (idempotente).

### 3.6 Graph Client: Connection Pool Tuning

| Tarea | Archivo | Detalle |
|-------|---------|---------|
| 2.6.1 | `client.go:189-192` | `NewClient`: `Transport` con `MaxIdleConns: 100`, `MaxIdleConnsPerHost: 20`, `IdleConnTimeout: 90s`, `DisableCompression: false` |
| 2.6.2 | `client.go` | Exponer `WithTransport` option para tests/overrides |
| 2.6.3 | `mount.go:183-191` | Pasar `HTTPTimeout` al transport (`ResponseHeaderTimeout`) |

**Criterios de aceptación**:
- Menos handshakes TLS en operaciones frecuentes (medible con `netstat`/`ss` o test).
- Los tests existentes con `httptest` siguen verdes.

### 3.7 Pre-Warm Configurable

| Tarea | Archivo | Detalle |
|-------|---------|---------|
| 2.7.1 | `mount.go:33-68` | `MountConfig.PreWarmDepth int` (default `2`) |
| 2.7.2 | `mount.go:193-200` | `preWarm(ctx, depth)`: BFS desde root, `GetChildren` async, respeta `CacheTTL` |
| 2.7.3 | `cmd/onecloudriver/mount.go:132-142` | Flag `--pre-warm-depth` (`0`=off, `1`=root, `2`=root+1, etc.) |
| 2.7.4 | `mount_test.go` | Test: pre-warm depth=2 → `Readdir("/Documents")` < 50ms (cache hit) |

> **Nota**: el pre-warm solo descarga **metadata** (Lookup/Readdir vía Graph API `children`),
> **nunca contenido de ficheros**. `0` desactiva, `2` por defecto.

**Criterios de aceptación**:
- Con pre-warm depth=2, primer `Readdir` de una subcarpeta < 50ms.
- Configurable por flag y persistido en `AccountPersistedConfig`.

---

## 4. Fase 3 — Validación Automatizada

> Objetivo: hacer que la seguridad y el rendimiento sean verificables de forma
> reproducible y en CI.

### 4.1 Suite de Benchmarks

```makefile
# Makefile — nuevo target:
bench:
	go test -bench=. -benchmem -benchtime=10s ./internal/fs/... -run=^$ \
		-benchoutput=bench_$(shell date +%s).txt
```

| Benchmark | Target |
|-----------|--------|
| `BenchmarkLookup` | p50 < 1ms, p99 < 5ms (cache hit) |
| `BenchmarkRead_4KB` | throughput > 500 MB/s (local cache) |
| `BenchmarkWrite_4KB` | latencia p99 < 2ms (ContentCache) |
| `BenchmarkReaddir_1k` | < 10ms (cache hit) |
| `BenchmarkEviction_10k` | < 1ms |
| `BenchmarkSweep_50k` | < 5ms |
| `BenchmarkDeltaSync_10k` | mem < 50MB, time < 2s |
| `BenchmarkUpload_100MB` | overhead < 100ms vs raw HTTP |

**Baseline reproducible**:
- Script `scripts/bench_baseline.sh` que registra el baseline y los guarda como artifact
  de CI para comparar futuras regresiones.
- Ejecución en la máquina del usuario (documentada) + en CI.

### 4.2 Tests de Estrés + Race

| Test | Comando |
|------|---------|
| Race detector 100x | `go test -race -count=100 ./internal/fs/...` |
| Fuzz ContentCache | `go test -fuzz=FuzzContentPath -fuzztime=60s ./internal/fs/...` (10s en CI) |
| Crash simulation | Script: mount → write → `kill -9` → remount → verify (100 iteraciones) |
| Concurrent write+delta | `go test -race -count=50 -run TestConcurrentWriteAndDelta ./internal/fs/...` |
| Memory leak | `go test -memprofile=mem.prof -run=XXX ./internal/fs/...` → `go tool pprof` |

### 4.3 Integración en CI

| Workflow | Cambio |
|----------|--------|
| `.github/workflows/ci.yml` | Job `benchmarks` (manual dispatch): corre benchmarks y compara contra baseline |
| `.github/workflows/ci.yml` | Job `stress`: `go test -race -count=10 -timeout=10m ./internal/fs/...` |
| `.github/workflows/ci.yml` | Job `crash-simulation` (intento primero; fallback local si el runner no permite FUSE) |

**Fuzz time**: 10s en CI, 60s local (configurable vía variable/flag).

---

## 5. Fase 4 — Rollout y Publicación

| Paso | Acción | Validación |
|------|--------|------------|
| 1 | Feature branch `issue-N-perf-security` | — |
| 2 | Commits atómicos por tarea (1 tarea = 1 commit) | `make test-unit-short` cada commit |
| 3 | PR con benchmarks before/after en la descripción | CI verde + benchmarks adjuntos |
| 4 | Merge squash a `main` | Tag `v0.1.3` (patch) |
| 5 | Release binario + .deb/.rpm | `make release-check && make release` |

> **Nota de proceso** (ver `AGENTS.md` / `CONTRIBUTING.md`): cada cambio requiere una
> issue GitHub previa. Este plan se convertirá en **un issue por fase** con tareas
> atómicas, checklists y criterios de aceptación.

---

## 6. Archivos Afectados (Resumen)

### Modificaciones principales

```
internal/fs/content_cache.go       # Heap eviction, path hardening, zero-copy
internal/fs/cache.go               # Time-bucket TTL, dirty tracking, heap size eviction
internal/fs/upload_manager.go      # Lock por inode, zero-copy snapshot
internal/fs/delta.go               # Streaming apply, flag uploading
internal/fs/mount.go               # Pre-warm, SerializeDirty, transport tuning
internal/graph/client.go           # Connection pool tuning
cmd/onecloudriver/mount.go         # Flag --pre-warm-depth
```

### Tests nuevos / ampliados

```
internal/fs/content_cache_fuzz_test.go     # Fuzzing path traversal
internal/fs/content_cache_bench_test.go    # Benchmarks eviction
internal/fs/cache_bench_test.go            # Benchmarks sweep
internal/fs/upload_manager_bench_test.go   # Benchmarks upload
internal/fs/delta_bench_test.go            # Benchmarks delta
internal/fs/stress_test.go                 # Race + crash tests (build tag)
```

### Config / CI / scripts

```
Makefile                           # Target bench
.github/workflows/ci.yml           # Jobs benchmarks + stress + crash-simulation
scripts/bench_baseline.sh          # Baseline reproducible
```

---

## 7. Criterios de Aceptación Globales (Definition of Done)

| Criterio | Verificación |
|----------|--------------|
| **Cero corrupción** | 100 iteraciones crash simulation → 0 errores BoltDB, 0 data loss |
| **Race-free** | `go test -race -count=100 ./internal/fs/...` → 0 warnings |
| **Latencia local** | Lookup p99 < 5ms, Read 4KB p99 < 2ms, Write 4KB p99 < 2ms |
| **Memoria acotada** | Delta sync 100k items < 50MB heap; mount 50k inodes < 100MB RSS |
| **Eviction O(log n)** | Benchmark eviction 10k ficheros < 1ms p99 |
| **Sweep O(1) amortizado** | Benchmark sweep 50k inodes < 5ms/tick |
| **BoltDB writes -90%** | Dirty tracking: bytes por delta sync < 10% del baseline |
| **Pre-warm funcional** | Depth=2 → primer Readdir de subcarpeta < 50ms |
| **Tests pasan** | `make test-unit-short && make test-integration-short` → verde |
| **Lint limpio** | `make lint-all` → 0 issues nuevos |
| **Baseline reproducible** | `scripts/bench_baseline.sh` documentado y funcionando en CI |

---

## 8. Justificaciones Técnicas (Decidido con el Usuario)

### 8.1 Por qué Lock por inode y no Copy-on-Write

| Criterio | Lock por inode | Copy-on-Write |
|----------|----------------|----------------|
| Memoria extra | **O(1)** (solo buffer del write) | **O(n × writers)** — cada writer concurrente tiene copia completa del archivo en memoria |
| Ejemplo 100MB, 4 writers | ~0 extra | **+400MB** heap durante los writes |
| Latencia | Serializa writes al mismo archivo | Paralela, pero GC pressure alto |
| Complejidad | Baja | Alta |

Para uso desktop (un usuario, un archivo editado a la vez), COW añade memoria y
complejidad sin beneficio real. **Veredicto: Lock por inode.**

### 8.2 Por qué mantener `NoSync=false` en BoltDB

| Aspecto | `NoSync=false` (actual) | `NoSync=true` |
|---------|------------------------|---------------|
| Durabilidad | `fsync()` en cada `Commit()` | Solo `write()`, el OS hace flush cuando quiere (30s típico) |
| Throughput | ~1-5k commits/seg | ~50-200k commits/seg |
| Riesgo en crash | **Cero** — tx completa o no existe | **Corrupción posible** — páginas parciales, freelist inconsistente, meta pages rotas |
| Recuperación | BoltDB abre limpio | `bolt.Open` puede fallar ("invalid meta page", "freelist corrupt") |

`SerializeAll`/`SerializeDirty` solo se ejecutan en delta sync (cada 5min) y unmount.
Con `NoSync=true` + crash → metadatos de cache corruptos → remount con carpetas vacías
u offline. El bottleneck real no es BoltDB (pocas escrituras), es la red (Graph API).
**Veredicto: mantener `false`; optimizar con dirty tracking (reduce bytes escritos 90%+).**

### 8.3 DeltaSync Streaming — Impacto de fallo parcial en el usuario

- **Actual (batch)**: descarga todas las páginas → aplica todo → si falla en el item
  50 de 100, NO persiste delta link → el siguiente poll (5min) reintenta todo.
  Usuario ve retraso de hasta 5min en cambios remotos ante error transitorio.
- **Streaming (propuesto)**: aplica por página, persiste delta link por página →
  si falla en la página 3, las páginas 1-2 ya están aplicadas. Usuario ve cambios
  remotos en **segundos**; solo la página fallida retrasa 5min.
- **Riesgo**: estado parcial visible momentáneamente (carpeta sin hijos). Pero Graph
  delta es **idempotente** y **ordenado** (padres antes que hijos); el siguiente poll
  completa. **No hay data loss.**

### 8.4 Observabilidad ligera (sin Prometheus)

Herramienta desktop de un solo usuario → Prometheus es exceso de recursos. Alternativa:

1. **Logs JSON estructurados** a `~/.cache/onecloudriver/logs/` (zerolog, rotados).
2. **`expvar` + pprof opcionales** vía flag `--debug` (localhost:6060), solo para troubleshooting.
3. **`onecloudriver stats`** (opcional futuro): lee contadores de cache/upload/delta.

Métricas a exponer: cache `hits/misses/evictions`, upload `queue/in_flight/completed/failed`,
delta `sync_count/error_count/last_sync_duration_ms`. **Cero dependencias externas.**

### 8.5 Pre-warm — Aclaración

`PreWarmDepth` configura el **número de niveles de metadata** a precargar tras el mount
(BFS desde root usando `GetChildren` vía Graph API). **Solo metadata** (nombre, tamaño,
tipo), **nunca el contenido de los ficheros**. Default `2`, configurable (`0` desactiva).

---

## 9. Riesgos y Mitigaciones

| Riesgo | Mitigación |
|--------|------------|
| Heap eviction rompe invariantes de ContentCache | Tests de paridad funcional antes/después + fuzz |
| Time-bucket sweep cambia el orden de eviction | Test que compara el conjunto evicted con la lógica actual |
| Dirty tracking pierde inodes en crash | `SerializeAll` final en unmount; crash simulation en CI |
| Streaming delta deja estado parcial | Graph idempotente; test de reinicio tras fallo parcial |
| CI sin FUSE para crash simulation | Fallback a la máquina del usuario (documentado) |
| Benchmarks no comparables entre máquinas | Baseline en CI (artifacts) + script reproducible |

---

## 10. Pendientes / Preguntas Abiertas

1. **Benchmark baseline**: ejecutar la suite actual en la máquina del usuario **antes**
   de tocar código para fijar el baseline real.
2. **Crash simulation**: confirmar si el runner de CI permite FUSE; si no, fallback local.
3. **Fuzz time**: 10s CI / 60s local (configurable) — **confirmado**.
4. **Pre-warm default**: **2** (configurable, `0` desactiva) — **confirmado**.
5. **DeltaSync streaming con fallo parcial**: impacto < 5min, sin data loss — **confirmado**.

---

*Este documento es la referencia única del plan. Las fases se convertirán en issues
GitHub (una por fase) con tareas atómicas, siguiendo `CONTRIBUTING.md`.*