# PLAN_7 — Issue #146: sync detecta el montaje/servicio activo antes de ejecutar y muestra el mountpoint

> Plan de implementación para la issue #146 (`feat(sync): detect active mount/service for the account before syncing and report the mountpoint`).
> Cambio propuesto por el usuario tras el merge de #145: el error de `sync` con un montaje activo es correcto pero tardío y opaco; se pide detectarlo **antes** y dar un mensaje claro con el mountpoint. Además se pide **evaluar** si forzar el sync con la carpeta montada es viable.

> **Revisión (2026-09-06):** este plan se ha **adaptado al merge de #147** (canal de control HTTP/JSON sobre socket Unix en `<cacheDir>/control.sock`, `GET /v1/info`, paquete `internal/control` con el cliente y el centinela `ErrNotRunning`, commit `4229df0` en `main`). El canal de control **cambia el diseño de detección** (paso 1 = sonda del socket, la señal precisa de "misma caché") y **el mecanismo recomendado** para el disparo de sync en caliente (endpoint del canal de control, no señal). Los cambios respecto a la versión previa están marcados con ✅.

---

## 1. Evaluación del problema actual

Estado verificado en `main`:

| Hecho | Evidencia |
|---|---|
| `sync` solo detecta el conflicto al abrir BoltDB | `cmd/onecloudriver/sync.go:46-50`: `inodeCache.InitBoltDB(...)` devuelve error → imprime tip → `sync failed: %w` |
| Espera **5 s** antes de fallar | `internal/fs/cache.go:997`: `boltOpenTimeout = 5s`; `bolt.Open(..., Timeout: 5s)` espera a adquirir el flock exclusivo |
| El error muestra el **fichero DB**, no el **mountpoint** | `internal/fs/cache.go:1015`: mensaje "BoltDB at <dbPath> is locked..." (no sabe qué punto de montaje lo tiene) |
| El montaje tiene el lock exclusivo por diseño | `bbolt` = single-writer; mount abre `inodes.db` y lo mantiene abierto mientras vive (`internal/fs/mount.go`) |
| No distingue *servicio systemd running* vs *mount en foreground* | El holder puede ser cualquiera de los dos; hoy solo se ve el lock |
| ✅ El proceso montado ya expone un canal de control en su caché | Tras #147, cada `mount` (por defecto) escucha en `<cacheDir>/control.sock` (`/v1/info` → `pid`, `account`, `mountpoint`, `state`, `config`) con permisos `0600` y cliente `internal/control` |
| Ya existe el aviso `cmd.sync.tip_mounted` | `internal/i18n/locales/en.json` + `es.json` (clave `cmd.sync.tip_mounted`), impreso en la rama de error |

**Conclusión**: el comportamiento es *correcto* (un segundo escritor corrompería la caché; el bucle delta del holder ya sincroniza), pero la UX es mala: 5 s de espera, mensaje con la ruta de la DB y sin el mountpoint. Gracias a #147, el holder de una caché concreta ahora es *descubrible* y *identificable* (mountpoint + pid) por el socket de control, sin depender de systemd ni de la tabla de mounts.

---

## 2. Decisión de diseño

### 2.1 Detección temprana (objetivo 1) — ✅ adaptada al canal de control de #147

**Ya no hace falta escanear la tabla de mounts ni confiar solo en systemd.** Si
un `mount` sirve exactamente esta caché — el único caso que bloquea a `sync`
—, su socket de control vive en `<cacheDir>/control.sock` y responde. Es la
señal **precisa** de "misma caché" (el mount table solo sabría que *la cuenta*
está montada en algún sitio, quizá con otra caché y sin conflicto real).

Nuevo flujo de preflight (solo lectura, antes de `MkdirAll`/`InitBoltDB`/poll):

| Paso | Qué detecta | Datos para el mensaje | Pieza (en `main` tras #147) |
|---|---|---|---|
| **1. Sonda del socket de control** ✅ | Un mount con **esta caché** y control activado | `mountpoint`, `pid`, `account` reales | `control.NewClient(<cacheDir>/control.sock).Info(ctx)`; `ErrNotRunning` ⇒ no hay holder por socket |
| **2. Sonda de lock BoltDB** (~200 ms) | **Cualquier** holder de `inodes.db`, incluso con control desactivado (`--control-socket off`) o en otro path | Desconocido → variante genérica ("la caché <dir> está en uso") | `internal/fs.CacheDirInUse(cacheDir)` (nueva) sobre `initBoltDB` (privado, timeout parametrizable) |
| **3. Enriquecimiento systemd** (solo si 2 detecta holder y 1 falló) | El holder es el servicio de la cuenta → recupera su mountpoint | `mountpoint` del servicio | `internal/service.QueryUnitStatus(account)` → `UnitStatus{State, Mountpoint}` (ya exportado) |

Reglas:
- **Orden y coste**: 1 (socket, ~ms) → 2 (lock 200 ms) → 3 (systemd, solo para
  recuperar el mountpoint). En el caso común (servicio o mount de esta caché
  con control por defecto) se aborta en el paso 1 sin esperar nada.
- **Autoridad final**: aunque 1/2 fallen (carrera entre la sonda y el arranque
  del holder), el `InitBoltDB` actual con 5 s sigue siendo el guardián. La
  detección temprana es UX; el lock es la garantía. No se quita.
- **Lo que se ELIMINA frente al diseño original** ✅: el escaneo de la tabla de
  mounts (`/proc/self/mounts`) y el uso de `acc.Mount.DefaultMountpoint` como
  señal de detección. Daban falsos positivos cuando la cuenta está montada con
  **otra caché** (sin conflicto). El criterio correcto es "misma caché", que lo
  dan el socket (paso 1) y el flock (paso 2).
- **Control desactivado / path custom** (`mount --control-socket off|path`): el
  paso 1 no ve el socket y el paso 2 lo cubre; si además no hay servicio
  systemd, el mensaje no puede saber el mountpoint (muestra la caché). Caso
  raro, aceptado y documentado.
- **Cuenta distinta en una caché custom compartida**: el paso 1 compara
  `info.Account.Name == acc`; si no coincide, se cae al paso 2 (lock).
- **Qué NO se hace**: lockfile propio (BoltDB ya da el flock); abrir la DB en
  escritura y mantenerla (la sonda abre → comprueba → cierra).
- **Salida**: mensaje a **stderr** y salida con error (no es un resultado
  `sync`); `sync` no tiene modo estructurado (`-o`), no se toca el contrato
  stdout.
- **i18n** ✅: dos claves nuevas — `cmd.sync.mounted_active` con `{Account}`,
  `{Path}` y `{PID}` opcional (holder por socket o por servicio) y
  `cmd.sync.cache_in_use` (variante sin mountpoint, solo caché). El tip
  `cmd.sync.tip_mounted` se conserva como fallback del guardián final.

### 2.2 ¿Forzar el sync con la carpeta montada? (objetivo 2) → **NO construir un `--force` con escritor separado**

Análisis:

| Opción | ¿Viable? | Por qué |
|---|---|---|
| `sync --force` que abra la misma `inodes.db` | **No** | `bbolt` es single-writer con flock exclusivo por proceso. Dos procesos no pueden compartir la DB; "forzar" = corromper o ignorar el lock. |
| Duplicar la sincronización leyendo Graph sin tocar la DB del mount | Parcial, inútil | Aplicaría cambios a una **segunda** copia de la caché, no a la que sirve el mount. El usuario vería dos verdades. |
| Pedir al **proceso que ya corre** que haga `PollOnce` ya | ✅ **Sí (única vía sana)** | El holder ya tiene `DeltaSync` + la DB. Tras #147 el disparo es un **endpoint del canal de control** (p. ej. `POST /v1/sync`) que el handler resuelve con un trigger coalescente (`DeltaSync.RequestPoll()`), no una señal: el socket da petición/respuesta y no necesita pidfile ni `MainPID`. |
| Depender del bucle delta del holder | Correcto por defecto | Ya sincroniza cada `--delta-interval` (default 5 min); el `sync` manual solo aporta *inmediatez*, no funcionalidad. |

**Recomendación (actualizada)**: NO implementar `--force` en esta issue.
Documentar en el mensaje y en el `--help` de `sync` que con el montaje activo
el bucle delta ya sincroniza. El "sync inmediato con la carpeta montada" será
una **issue separada** que añada `POST /v1/sync` al canal de control (#147):
cuando el paso 1 de la detección confirme el holder vía socket, `sync` podrá
ofrecer `sync --notify`/`--trigger` que envía la petición por el socket y
muestra la respuesta, en vez de abortar. Este diseño **sustituye** la idea de
señal `SIGUSR2` + pidfile del borrador original de PLAN_7.

---

## 3. Fases de implementación (TDD)

Todas las fases asumen la issue #146 creada y rama `issue-146-sync-detect-active-mount` desde `main`.

### Fase 0 — Preparación
- Rama `issue-146-sync-detect-active-mount` desde `main` (main ya incluye #147).
- Catálogo de claves i18n afectadas: añadir `cmd.sync.mounted_active` y
  `cmd.sync.cache_in_use` a `en.json`/`es.json` en la fase verde.

### Fase 1 — Tests red (TDD) en las piezas nuevas
Se testean las piezas de detección con seams inyectables (sin cobra, sin socket
real, sin systemd real, sin mount real):

1. ✅ **Sonda del socket de control** (usa `internal/control` que ya existe):
   - `cmd/onecloudriver/sync.go` recibe el preflight un `controlInfo
     func() (*control.Info, error)` inyectado. Test con fake que devuelve un
     `Info` (account == acc, mountpoint y pid) → holder; fake que devuelve
     `control.ErrNotRunning` → sin holder; fake que devuelve otra cuenta →
     se cae a la sonda de lock.
2. `internal/fs` — sonda corta de lock:
   - `func CacheDirInUse(cacheDir string) (bool, error)` que abre `inodes.db`
     con `boltOpenTimeoutProbe` (~200 ms) y cierra. Test: mantener una DB
     abierta en un goroutine → `true`; sin holder → `false`.
3. `internal/service` — ✅ solo el enriquecimiento (se elimina el helper de
   tabla de mounts del borrador):
   - `RunningMountpoint(account string) (mp string, running bool)` envolviendo
     `QueryUnitStatus` — test con `systemdClient.run` fake devolviendo
     `ActiveState=active, SubState=running, ExecStart=... mount <mp> -a
     <account>` → `mp` expandido; unidad `inactive` → `running=false`.
4. ✅ `cmd/onecloudriver/sync.go` — función pura de preflight inyectable:
   - `func activeMount(accName string, cacheDir string, sockInfo func()(*control.Info,error), cacheInUse func()(bool,error), svcRunning func()(string,bool)) (holder, error)` donde `holder` = `{mountpoint, pid, kind}` ("" = libre). Tests con fakes para las combinaciones: socket OK (aborta con mountpoint+pid), socket ErrNotRunning + lock libre (sigue), lock ocupado + servicio running (mountpoint del servicio), lock ocupado + sin servicio (genérico, caché).

Todos los tests deben **fallar** en esta fase (las funciones no existen / no devuelven lo esperado).

### Fase 2 — Green: implementar detección en `sync.go`
- `cmd/onecloudriver/sync.go`: tras resolver cuenta y `fs.DefaultMountConfig`, ANTES del `MkdirAll`/`InitBoltDB`/poll:
  1. Paso 1: `control.NewClient(filepath.Join(cacheDir, "control.sock")).Info(ctx)` con timeout corto. Si OK y `info.Account.Name == acc` → mensaje `cmd.sync.mounted_active` con `{Account}`, `{Path}` (del `info.Mount.Mountpoint`) y `{PID}` → salida con error.
  2. Paso 2: si `ErrNotRunning` → `fs.CacheDirInUse(cacheDir)` (timeout 200 ms). Si ocupada → paso 3 para enriquecer el mountpoint.
  3. Paso 3: `service.RunningMountpoint(acc)` → si running, mensaje `cmd.sync.mounted_active` con su mountpoint; si no, `cmd.sync.cache_in_use` con `{CacheDir}`.
  4. Sin holder → continuar con `InitBoltDB` (guardián final, tip actual intacto).
- No imprimir "Syncing..." ni llamar a `PollOnce` cuando hay holder.

### Fase 3 — Cobertura (no bajar el %)
- Añadir tests que cubran **todas** las ramas nuevas: holder por socket (mountpoint+pid), socket con otra cuenta → lock probe, lock ocupado + servicio running, lock ocupado + sin servicio (variante caché), control `off` (socket muerto + lock ocupado), servicio `stopped`, y el camino feliz (sync libre).
- Medir antes/después por paquete afectado:
  ```bash
  go test ./internal/service/... ./internal/fs/... ./internal/control/... ./cmd/onecloudriver/... -count=1 -coverprofile=/tmp/c.out
  go tool cover -func=/tmp/c.out | tail -1
  ```
- Objetivo: no bajar el % de `cmd/onecloudriver`, `internal/service`, `internal/fs` ni `internal/control` respecto a `main`.

### Fase 4 — Verificación local equivalente a CI
```bash
make build && go vet ./...                          # job Build
go test ./internal/fs/... -count=1 -race -short     # job Tests unit+race (make test-unit-short)
go test ./internal/auth/... ./internal/graph/... -count=1 -race -short -timeout 120s
# cmd y internal/service NO corren en el job unit+race (solo en el job Coverage):
go test ./internal/service/... ./internal/control/... ./cmd/onecloudriver/... -count=1 -race -short
golangci-lint run --timeout=5m ./...                # make lint-all (job Lint)
gosec -quiet -severity=high ./...                   # job Security Audit
govulncheck ./...
golangci-lint run --timeout=5m -c .golangci-security.yml --new-from-rev=HEAD~1 ./...
```
> ⚠️ Matiz CI (verificado en `.github/workflows/ci.yml`): el job *Tests (unit+race)* solo ejecuta `internal/fs`, `internal/auth` e `internal/graph`. `cmd/onecloudriver`, `internal/service` e `internal/control` se ejecutan únicamente en el job *Coverage Report* (host con FUSE). Por eso la verificación local de Fase 4 debe incluirlos explícitamente, y la cobertura de las ramas nuevas de `sync.go` se vigila en ese job. ✅ Recomendación reforzada: añadir `./internal/control/...` a la lista explícita del job unit+race en `ci.yml` (quedó fuera en #147).

### Fase 5 — Verificación manual del repro
```bash
onecloudriver mount <mp> -a <account> --control-socket /tmp/ctrl.sock &   # o service start
onecloudriver sync -a <account>
# Esperado (inmediato, sin esperar 5 s):
#   ⚠️ El punto de montaje <mp> está activo para la cuenta <account> (pid N). ... (stderr)
#   Error: sync failed: ...
# Variante control off:
onecloudriver mount <mp> -a <account> --control-socket off &
onecloudriver sync -a <account>
#   ⚠️ La caché <cacheDir> está en uso por otra instancia. ... (o mountpoint si hay servicio)
onecloudriver service stop -a <account>
onecloudriver sync -a <account>                      # vuelve a funcionar
```

### Fase 6 — Commit + PR + CI
- Commit en inglés: `feat(sync): detect active mount/service before syncing and report the mountpoint (Closes #146)`.
- PR a `main` con plantilla y `Closes #146`; esperar los checks (Build, Tests unit+race, Tests integration FUSE, Lint, Security Audit + resto) y cobertura sin caída.
- Merge squash.

### Fase 7 — Follow-up (fuera de alcance, issue separada) ✅
- "Sync inmediato con la carpeta montada": issue nueva que añade al canal de
  control (#147) un endpoint de disparo, p. ej. `POST /v1/sync`, manejado en
  `internal/fs/mount.go` (registrado junto al `mux` del control server) y que
  llama a un trigger coalescente de `DeltaSync` (p. ej. `RequestPoll()` desde
  `deltaLoop`). `sync` usará la detección de esta issue (paso 1) para ofrecer
  `--notify`/`--trigger` por el socket con respuesta. **Ya no se plantea una
  señal `SIGUSR2` ni pidfile.**

---

## 4. Ficheros involucrados

| Fichero | Cambio |
|---|---|
| `cmd/onecloudriver/sync.go` | Preflight de detección (socket de control → lock → systemd) antes del poll + nueva rama de error |
| `cmd/onecloudriver/sync_test.go` (nuevo) | Tests unitarios del preflight con fakes (socket/lock/servicio) |
| `internal/control` | ✅ Sin cambios: ya en main (#147); `sync.go` consume `Client`/`ErrNotRunning` |
| `internal/fs/cache.go` | `CacheDirInUse(cacheDir)` (sonda corta sobre `initBoltDB`) + constante `boltOpenTimeoutProbe` |
| `internal/fs/cache_test.go` | Tests de la sonda (holder en goroutine) |
| `internal/service/status.go` | `RunningMountpoint(account)` (envuelve `QueryUnitStatus`; enriquecimiento del paso 3) |
| `internal/service/status_test.go` | Tests de `RunningMountpoint` con runner fake |
| `internal/i18n/locales/en.json` + `es.json` | Claves `cmd.sync.mounted_active` (con `{PID}` opcional) y `cmd.sync.cache_in_use` |
| `.github/workflows/ci.yml` | ✅ (Recomendado) añadir `./internal/control/...` al job *Tests (unit+race)* |

> Se ELIMINA del borrador: `MountpointActive` en `internal/service/systemd.go`
> (escaneo de la tabla de mounts) — ya no es necesario con la sonda del socket.

## 5. Riesgos y notas

- **TOCTOU**: la detección temprana no elimina el lock; el `InitBoltDB` final sigue siendo la garantía (no se toca `boltOpenTimeout`).
- ✅ **Control desactivado/custom**: el paso 1 no ve el socket; el paso 2 (lock) lo cubre. Sin servicio, el mensaje no tiene mountpoint (variante caché). Aceptado.
- ✅ **Falso positivo eliminado**: ya no se detecta "por montaje de la cuenta en cualquier sitio" (tabla de mounts), sino por "misma caché" (socket + flock).
- **Salida estructurada**: `sync` no soporta `-o`; no hay contrato machine-readable que romper. El mensaje va a stderr.
- **Emojis/símbolos**: usar `printer.Warning`/`printer.Error`, nunca emojis literales (regla del repo).
- **No tocar `internal/graph`** ni FUSE real: no hace falta `make test-integration`; el cambio es de capa de comando/estado (usa el cliente `internal/control` ya testeado).
