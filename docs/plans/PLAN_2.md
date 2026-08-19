# PLAN_2 — Revisión de PLAN_1: coherencia con el estado actual y correcciones

> **Estado**: Revisado — sirve de guía para la creación de issues GitHub.
> **Fecha**: 2026-08-14
> **Base**: `PLAN_1.md`, revisado contra el código en HEAD `b777fb9` (rama `no-issue-update-make-doc`).

---

## 1. Resumen de la revisión

Se contrastó cada tarea de `PLAN_1.md` con el código actual y con las issues
de GitHub (abiertas y cerradas). Conclusiones:

| Resultado | Cantidad | Detalle |
|---|---|---|
| ✅ **Ya implementada** (eliminar de PLAN_1) | 6 | Tareas 1.1.1, 1.1.2, 1.1.3, 1.1.4, 1.3.1, 1.3.2 |
| 🟡 **Parcial** (solo falta documentar) | 1 | Tarea 1.4.1 (`NoSync=false` implícito, sin comentario) |
| 🔗 **Solapa con issues abiertas** (mantener con dependencia) | 2 bloques | Eviction/sweep (con #50); upload atomicity e integridad (relación con #16/#32) |
| 🆕 **Válidas y no duplicadas** → issues creados | 11 | Ver §4 |

**Ninguna tarea de PLAN_1 es un cambio rompedor** y las referencias
archivo:línea del plan son aproximadamente correctas (algunas líneas han
cambiado ligeramente; se indican las actuales).

---

## 2. Tareas ya implementadas en el código actual (ELIMINAR de PLAN_1)

| Tarea PLAN_1 | Estado actual | Evidencia |
|---|---|---|
| **1.1.1** Auditoría `evictMu` en ContentCache | ✅ Hecho | `internal/fs/content_cache.go`: `evictMu sync.Mutex` serializa `Open()` (creación de fichero) con `evictBySize()`, cerrando la ventana TOCTOU documentada en el struct y en el comentario de `Open()`. |
| **1.1.2** Test de carrera eviction vs Open | ✅ Hecho | `internal/fs/content_cache_race_test.go`: `TestContentCache_EvictionDoesNotRaceWithOpen` (canary) y `TestContentCache_EvictionPreservesOpenFiles`. |
| **1.1.3** Fuzz `contentPath(id)` | ✅ Hecho | `internal/fs/content_cache_fuzz_test.go`: `FuzzContentPath` con corpus adversarial (`../../../etc/passwd`, `\x00`, backslashes, rutas absolutas, `.`/`..`). |
| **1.1.4** `filepath.IsLocal` antes del hex | ✅ Hecho | `internal/fs/content_cache.go` `contentPath()`: saneado de null bytes + `filepath.IsLocal` + `hex.EncodeToString` de fallback, con `log.Warn` del ID original. |
| **1.3.1** Ventana Fsync→QueueUpload en delta | ✅ Hecho | `internal/fs/delta.go` `applyDelta` (caso 4): guard con `local.HasChanges()` y `d.uploads.HasPendingUpload(id)` — la versión local gana si hay trabajo pendiente. Resuelto por la issue #15 y el commit `fix(delta): preserve local changes when a remote delta arrives (#58)`. |
| **1.3.2** Flag `Uploading` atómico | ✅ Hecho | `internal/fs/upload_manager.go` `HasPendingUpload(id)` consulta las sesiones activas; cableado en `mount.go` vía `deltaSync.SetUploadQuery(uploadManager)`. |

**Acción**: eliminar 1.1.x y 1.3.x del plan. Los criterios de aceptación de
esas tareas ya se cumplen (`go test -race` verde, fuzz presente, guard anti
data-loss presente). Queda pendiente únicamente el **test de integración
1.3.3** (delta entre Fsync y upload) como refuerzo de la issue #15 ya cerrada;
se ha incluido en la issue #68 (streaming delta) como caso de regresión.

### 1.4.1 — Parcial

`cache.go` `InitBoltDB()` usa `bolt.Open(dbPath, 0600, &bolt.Options{Timeout: 1})`.
`NoSync` **no se pasa explícitamente**, pero `false` es el valor por defecto de
bbolt → el comportamiento ya es el correcto. Solo falta **documentar el porqué**
(comentario + justificación del §8.2 de PLAN_1). Se añade como sub-tarea de la
issue #67 (dirty tracking).

---

## 3. Solapamientos con issues de GitHub existentes

| Bloque PLAN_1 | Issue(s) existente(s) | Relación | Acción |
|---|---|---|---|
| **Fase 2 eviction** (2.1 heap en ContentCache, 2.2 time-bucket en InodeCache, 2.2.4 heap por score) | **#50** (OPEN) `refactor(fs): consolidate duplicated eviction/sweep logic between the metadata cache and the content cache` | Solapan en las mismas funciones (`evictBySize`, `evictExpiredChildren`, `evictChildrenBySizeLimit`) | **No duplicar**: las issues de rendimiento creadas se marcan `Related to #50` y se recomienda secuenciar #50 primero (refactor) y luego el cambio de estructura de datos. |
| **1.2** Snapshot atómico en `QueueUpload` | **#16** (OPEN) `feat(graph): concurrency control (If-Match/ETag) in UploadItem/UploadItemStream` | Distinto ámbito: #16 es concurrencia **remota** (optimistic concurrency contra Graph); 1.2 es atomicidad del snapshot **en memoria** frente a escrituras FUSE concurrentes | No duplicado → se crea la issue **#64** con referencia a #16. |
| **Fase 1 "integridad"** (objetivo global) | **#32** (OPEN) `feat(graph): QuickXorHash computation and content-integrity verification` | #32 cubre verificación de integridad del contenido descargado; PLAN_1 no incluye QuickXorHash | Complementarios, no duplicados. |
| **1.4.5 / 4.2 crash simulation** | Ninguna issue abierta | — | Se crea dentro de la issue **#72** (suite de tests/CI). |
| **2.3.4 SerializeDirty en unmount** | Ninguna | — | Incluido en la issue **#67** (dirty tracking). |
| **Decisión "comando de sync manual"** | Ninguna | — | Se crea la issue CLI **#73**. |
| **8.4 Observabilidad ligera** | Ninguna | — | Se crea la issue **#74**. |

> ⚠️ Las decisiones de §8 (lock por inode, NoSync=false, streaming idempotente,
> observabilidad ligera, pre-warm metadata-only) se consideran **correctas y
> coherentes** con la arquitectura actual y se mantienen sin cambios.

---

## 4. Tareas válidas → issues GitHub creados

Cada issue sigue el template de `CONTRIBUTING.md` (Problem / Goal / Proposed
changes / Files involved) e incluye propuesta de código.

| Issue | Título | Tareas PLAN_1 | Label |
|---|---|---|---|
| **#64** | `feat(fs): atomic content snapshot in UploadManager.QueueUpload (per-inode lock)` | 1.2.1, 1.2.2, 1.2.3 | `enhancement` |
| **#65** | `perf(fs): O(log n) heap-based eviction in ContentCache` | 2.1.1–2.1.5 | `enhancement` (Related to #50) |
| **#66** | `perf(fs): time-bucketed TTL sweep for InodeCache metadata eviction` | 2.2.1–2.2.5 | `enhancement` (Related to #50) |
| **#67** | `feat(fs): dirty inode tracking → SerializeDirty (cut BoltDB writes ≥90%)` | 1.4.2, 1.4.3, 1.4.4, 2.3.1–2.3.5, 1.4.1 (doc) | `enhancement` |
| **#68** | `perf(fs): streaming delta apply (bounded memory + per-page persistence)` | 2.5.1–2.5.3, 1.3.3 (test) | `enhancement` |
| **#69** | `perf(fs): zero-copy upload snapshot (avoid ReadAll allocation)` | 2.4.1–2.4.3 | `enhancement` |
| **#70** | `perf(graph): tune HTTP transport (connection pooling) + WithTransport option` | 2.6.1–2.6.3 | `enhancement` |
| **#71** | `feat(fs): configurable metadata pre-warm depth after mount (--pre-warm-depth)` | 2.7.1–2.7.4 | `enhancement` |
| **#72** | `test(ci): benchmark suite + stress/race + crash-simulation baseline` | 4.1, 4.2, 4.3, 1.4.5 | `enhancement` |
| **#73** | `feat(cli): add manual sync command (onecloudriver sync)` | Decisión §1 (DeltaSync) | `enhancement` |
| **#74** | `feat(obs): lightweight observability (structured JSON logs + --debug expvar/pprof)` | 8.4 | `enhancement` |

> **Nota de proceso**: conforme a `CONTRIBUTING.md`, cada issue es la referencia
> obligatoria de su rama/PR (`issue-N-slug`). Las issues #65 y #66 dependen del
> refactor #50; el resto son independientes entre sí.

---

## 5. Recomendaciones de secuenciación

1. **#50 primero** (refactor eviction/sweep) → después #65 y #66 (estructuras
   de datos O(log n)) para no pisarse.
2. **#67 (dirty tracking)** y **#68 (streaming delta)** tocan ambas `delta.go`
   y `cache.go` → revisar juntas en la misma iteración para evitar conflictos
   de ramas.
3. **#64 (snapshot atómico)** es el único cambio de **Fase 1** restante y es
   independiente — alta prioridad por riesgo de integridad.
4. #70, #71, #73, #74 son aditivas y de bajo riesgo (flags/opciones nuevas,
   sin cambiar comportamiento por defecto salvo el transport HTTP).
5. #72 (benchmarks) debe crearse **antes** de tocar rendimiento para fijar el
   baseline real (requisito del §10 de PLAN_1: "fijar el baseline antes de
   tocar código").

---

## 6. Notas adicionales y riesgos

- **Baseline benchmarks**: PLAN_1 pedía ejecutar la suite actual en la máquina
  del usuario antes de optimizar; la issue #72 incluye el script
  `scripts/bench_baseline.sh` para hacerlo reproducible.
- **Crash simulation en CI**: el runner de GitHub Actions aloja FUSE en el host
  (job `test-integration` ya corre fuera del contenedor), así que la simulación
  de crash es viable en CI; el fallback local queda documentado.
- **Fuzz time**: 10s CI / 60s local — confirmado y sin cambios.
- **Pre-warm default 2** — confirmado; solo metadata, nunca contenido.
- **Riesgo eviction heap**: se mitiga con tests de paridad funcional
  (mismo conjunto evicted antes/después) incluidos en las issues #65 y #66.
- **Riesgo dirty tracking**: la garantía final es `SerializeAll` en unmount
  (ya existente en `mount.go`); la issue #67 lo mantiene explícitamente.

---

*PLAN_2 reemplaza a PLAN_1 como referencia de trabajo. Las issues de §4 son el
vehículo de planificación en GitHub; este documento se archiva en
`docs/plans/` como registro de la revisión.*
