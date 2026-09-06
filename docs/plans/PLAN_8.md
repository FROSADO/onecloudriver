# PLAN_8 — Issue #147: canal de control del proceso montado sobre socket Unix (API HTTP/JSON `/v1`) — endpoint `/v1/info`

> Plan de implementación para la issue #147 (`feat(control): local control channel over a Unix socket (HTTP/JSON /v1) for the mount process — /v1/info`).
> Es una feature **independiente** (el usuario la desacopla de #146). Objetivo: definir y construir el **transporte + primer endpoint** de un canal local proceso-montado ↔ CLI/aplicaciones futuras (UI), que servirá de base estable para operaciones posteriores (forzar sync, refresh de caché, re-descarga, estadísticas…). Primera entrega: solo `GET /v1/info`.

---

## 1. Contexto y motivación

El proceso de `mount` posee en exclusiva la caché de la cuenta: lock de `inodes.db` (bbolt single-writer), caches en memoria/ContentCache, el bucle `DeltaSync` y el `UploadManager`. Hoy **ningún otro proceso** puede consultarlo:

- `sync` choca con el lock de bbolt (#146).
- El server HTTP de debug (`internal/obs`, #74) es opt-in, de solo-lectura (expvar/pprof) y en loopback TCP.
- Las señales gestionadas solo desmontan (SIGINT/SIGTERM/SIGHUP).

Una UI futura y el "sync del proceso en marcha" necesitan un **canal de control**: protocolo/API local para interrogar el estado y, más adelante, operar sobre el mount y su caché.

## 2. Evaluación de opciones de transporte (resuelto)

| Opción | Veredicto |
|---|---|
| **HTTP sobre socket Unix** (`net.Listen("unix", path)` + `http.Server.Serve(ln)`) | **Elegida**. Reutiliza `net/http` (routing, JSON, timeouts, cierre); clientes triviales: Go (`http.Transport{DialContext}` unix) o `curl --unix-socket`. Patrón canónico confirmado en la stdlib de Go (`net.Listen`+`Serve`; `ListenConfig.Control` para endurecer permisos del socket en bind) |
| Protocolo raw (JSON-lines / length-prefixed) sobre `net.Unix` | Rechazada: reinventa framing/timeouts/routing sin beneficio |
| gRPC sobre UDS | Rechazada: codegen + dependencia pesada para una API JSON local pequeña |
| TCP loopback (reusar el server `--debug`) | Rechazada por **seguridad**: un puerto TCP en loopback es alcanzable por *cualquier usuario local* (no solo el dueño) y exigiría autenticación por token. Un socket Unix restringido por permisos de fichero (dir `0700`, socket `0600`) limita el acceso al usuario propietario |

**Referencias consultadas**
- context7 (`/golang/go`): `net.Listen("unix", …)` + `http.Server.Serve(listener)` y `http.Client` con `DialContext` unix; `ListenConfig.Control` permite fijar permisos/owner en el bind.
- Microsoft Learn (IPC): corrobora el modelo — para UDS la seguridad se apoya en los **permisos de fichero del SO** y en validar la identidad/propiedad del server; evita puertos TCP; patrón equivalente en .NET (named pipes sobre UDS en Linux). No aplica nada específico de Go/onecloudriver en MS Learn, pero valida el diseño de seguridad.

## 3. Decisión de diseño

### 3.1 Arquitectura

```
┌─────────────────────────────── proceso mount (foreground o systemd) ────┐
│ fs.Mount                                                               │
│   ├─ DeltaSync / UploadManager / caches                                │
│   └─ internal/control.Server  ── escucha ──► <cacheDir>/control.sock   │
│         rutas:  GET /v1/info                                           │
└──────────────────────────────────────────────────────────────────────────┘
                 ▲  HTTP/JSON sobre socket Unix (solo mismo usuario)
                 │
  internal/control.Client  (CLI, futura UI, curl --unix-socket)
```

- **Transporte**: `net.Listen("unix", sockPath)` + `http.Server` (mismo patrón que `obs.StartDebugServer`, pero sobre socket de fichero y **siempre activo**, no opt-in).
- **Path del socket**: `<cacheDir>/control.sock`, derivado de la config del mount (cada cuenta/caché tiene el suyo). Campo nuevo `MountConfig.ControlSocket` (default `filepath.Join(CacheDir, "control.sock")`); vacío = desactivado (por si un entorno lo necesita).
- **API versionada**: prefijo `/v1/`. Respuestas JSON con `Content-Type: application/json`. Contrato estable para la UI futura.
- **Ciclo de vida del socket**: bind tras abrir bbolt (el proceso ya "existe"); borrar `control.sock` *stale* antes de escuchar (unlink + retry si el connect falla); `Remove` al parar (en el path de unmount/salida de `mount.go`, junto al `inodeCache.Close()`). Reintento/`ListenConfig.Control` para forzar `0600` en bind.
- **Seguridad**:
  - El socket vive en un dir `0700` del usuario (la caché ya se crea así). Permisos de fichero = frontera same-user. Verificación opcional de peer con `SO_PEERCRED` (uid == euid del mount) en el `Control` del `ListenConfig`/conn si se quiere defensa en profundidad.
  - Endpoints **whitelist**; `GET` de solo lectura en esta entrega. Sin body grande, `ReadHeaderTimeout`, límites por request. Nunca exponer tokens ni permitir fetch arbitrario por otra cuenta (aquí solo hay datos de estado).
  - No abrir puertos TCP (nada de `--debug` como base).
- **Rendimiento**: un listener + un fd en idle (~0). Handlers en goroutines de `net/http`; el único dato es estado leído de campos ya en memoria (sin locks globales con el hot path de FUSE). Coste por request: microsegundos; irrelevante frente al tráfico FUSE. Las operaciones *pesadas* futuras (sync, re-descarga) se serializarán sobre su subsistema (p. ej. `DeltaSync.RequestPoll()` coalescente de PLAN_7) y serán asíncronas + estado consultable.
- **Versionado del binario**: el `version` de `Info` viene de la variable de build `main.version`; como `fs` no puede importar `main`, se pasa desde `cmd/onecloudriver/mount.go` vía un campo del provider (o se omite si no está disponible).

### 3.2 Contrato `GET /v1/info`

Respuesta `200` (ejemplo):

```json
{
  "api":      { "name": "onecloudriver-control", "version": 1 },
  "daemon":   { "pid": 1234, "startedAt": "2026-09-05T10:00:00Z", "goVersion": "go1.26.7", "binaryVersion": "v0.1.5" },
  "account":  { "name": "user@outlook.com" },
  "mount":    { "state": "running", "mountpoint": "/home/user/OneDrive/user@outlook.com", "cacheDir": "/home/user/.cache/onecloudriver/user@outlook.com", "configFile": "/home/user/.config/onecloudriver/user@outlook.com.json" },
  "config":   { "cacheTTL": "60s", "cacheMaxEntries": 2000, "cacheMaxSize": "0", "deltaInterval": "5m0s", "maxUploadsInFlight": 5, "maxUploadRetries": 5, "graphRetries": 3, "httpTimeout": "15s", "preWarmDepth": 2, "debugAddr": "" }
}
```

- Duración con `time.Duration` serializada como string (`"60s"`) para JSON estable (igual que en YAML de configs); o en nanosegundos si se prefiere — **decisión de la fase 1**, documentada en el tipo.
- Errores: `404` rutas desconocidas; `405` métodos no-GET; `500` con `{"error": {...}}` (JSON problem-style). El cliente tipa los códigos.
- `mount.state`: `"running"` mientras `server.Wait()` no ha retornado; futuras fases pueden añadir `"unmounting"`.
- `configFile`: path del JSON de cuenta (`~/.config/onecloudriver/<account>.json`). Se pasa desde `cmd` al provider (nuevo campo opcional en `MountConfig` o en el struct del provider); si ausente → omitido.

## 4. Fases de implementación (TDD)

### Fase 0 — Preparación
- Issue #147 creada; rama `issue-147-control-socket-info` desde `main`.
- `internal/control` es paquete **nuevo** → no hay ciclos de import si NO importa `internal/fs` (define sus propios tipos + interfaz de provider).

### Fase 1 — Red (tests unitarios de `internal/control`, con socket en temp dir)
1. `server_test.go`:
   - Arranca server sobre `t.TempDir()/control.sock`; `GET /v1/info` → `200` + JSON con los campos esperados del provider fake.
   - Ruta desconocida → `404`; método `POST` → `405`; request sin `Accept` JSON sigue funcionando.
   - Permisos del socket: `stat` → `0600` (o `& 0o077 == 0`). Socket *stale* preexistente: se limpia y el server arranca.
   - `Close()` → el fichero `control.sock` ya no existe; doble `Close()` no pánico.
2. `client_test.go`: `Client.Info(ctx)` contra server fake → estructura parseada; contra socket inexistente → error tipificado (`ErrNotRunning`/`os.IsNotExist`); timeouts.
3. `info_test.go`: serialización del `Info` (duraciones como string, omitempty, orden estable de claves no requerido).

Todos los tests **fallan** en esta fase (el paquete no existe).

### Fase 2 — Green: implementar `internal/control`
- `control.go`: tipos `Info`, `DaemonInfo`, `MountInfo`, `ConfigInfo`, `APIInfo`.
- `server.go`: `Server{ln, httpSrv, sockPath}`; `NewServer(sockPath string, provider InfoProvider) (*Server, error)` (bind + stale cleanup + `ListenConfig.Control` con `0600`); `Serve()` en goroutine; `Close()` (cierra listener/server y `os.Remove(sockPath)`); `Addr()` no aplica (path).
- `client.go`: `Client{sockPath}`; `NewClient(sockPath)`; `Info(ctx)` con `http.Client{Transport: unixTransport}`.
- Mux con `http.ServeMux` (Go 1.22+ soporta `"GET /v1/info"`), `ReadHeaderTimeout`, `w.Header().Set("Content-Type","application/json")`.

### Fase 3 — Integración en `fs.Mount` (provider + ciclo de vida)
- `internal/fs/mount.go`: tras `inodeCache.InitBoltDB` y antes de `server.Wait()`, construir el provider (pid=`os.Getpid()`, account, mountpoint, cacheDir, config efectiva, startedAt) y `control.NewServer(config.ControlSocket, provider)`; arrancar en goroutine; `defer`/cleanup en el path de unmount (junto a `inodeCache.Close()`) y en el `defer` de error de `fs.Mount`.
- `MountConfig` gana `ControlSocket string` (default en `DefaultMountConfig`: `<cacheDir>/control.sock`).
- `cmd/onecloudriver/mount.go`: (opcional) rellenar `configFile`/`binaryVersion` del provider si se decide exponerlos (pasar vía campo en `MountConfig` o un struct `ControlInfoExtra`).
- Tests de integración ligera: en un test de `internal/fs` que NO requiera FUSE, construir un provider real y verificar `client.Info` (o confiar en los tests de `internal/control` + test manual). El arranque real con FUSE se valida en el job de integración/manual.

### Fase 4 — Cobertura (no bajar el %)
- Nuevo paquete con tests de todas las ramas (bind, stale, 404/405, close, cliente con timeout/socket ausente, serialización). Meta: `internal/control` ≥ 85–90% statements (objetivo a fijar midiendo la primera pasada).
- `internal/fs` y `cmd/onecloudriver`: los campos/ramas nuevos (start/stop del server, provider) se cubren con tests unitarios donde no requieran FUSE.
- Medir: `go test ./internal/control/... ./internal/fs/... ./cmd/onecloudriver/... -coverprofile=... && go tool cover -func=... | tail -1`.

### Fase 5 — Verificación local equivalente a CI
```bash
make build && go vet ./...
go test ./internal/fs/... -count=1 -race -short              # job Tests unit+race
go test ./internal/auth/... ./internal/graph/... -count=1 -race -short -timeout 120s
# Paquetes que el job unit+race NO cubre (solo el job Coverage los ejecuta):
go test ./internal/control/... ./cmd/onecloudriver/... ./internal/service/... -count=1 -race -short
golangci-lint run --timeout=5m ./...                          # job Lint
gosec -quiet -severity=high ./... && govulncheck ./...        # job Security Audit
golangci-lint run --timeout=5m -c .golangci-security.yml --new-from-rev=HEAD~1 ./...
```
> ⚠️ **Matiz CI** (`.github/workflows/ci.yml`): el job *Tests (unit + race)* solo ejecuta `internal/fs`, `internal/auth` e `internal/graph`. `internal/control` (nuevo), `cmd` y `internal/service` solo corren en el job *Coverage Report*. **Recomendación**: añadir `go test ./internal/control/... -count=1 -race -short -timeout 60s` a la lista explícita del job *Tests (unit + race)* (cambio pequeño en `ci.yml`) para que el canal de control tenga cobertura unitaria en un check bloqueante. Alternativa aceptable: depender del job de cobertura (no bloqueante) — se decide en la implementación.

### Fase 6 — Verificación manual
```bash
onecloudriver mount ~/OneDrive/<account> -a <account> &
curl --unix-socket ~/.cache/onecloudriver/<account>/control.sock http://localhost/v1/info
# -> 200 JSON con daemon/mount/config
curl --unix-socket <sock> http://localhost/v1/nope          # -> 404
ls -l <cacheDir>/control.sock                                # -> 0600, dueño = usuario
# Parar el mount (Ctrl+C o service stop) -> control.sock ya no existe
```

### Fase 7 — Commit + PR + CI
- Commit: `feat(control): add local Unix-socket HTTP/JSON control channel with /v1/info (Closes #147)`.
- PR a `main` con plantilla + `Closes #147`; esperar checks y cobertura sin caída; merge squash.

### Fase 8 — Roadmap (issues futuras sobre este canal, NO en #147)
- `POST /v1/sync` (trigger del delta en el proceso en marcha) — depende de la detección de #146; sustituye la idea de señal de PLAN_7.
- `POST /v1/refresh` (invalidar children de un folder) y `POST /v1/redownload` (evictar content de un item/path) — para la UI.
- `GET /v1/stats`, listado/cancelación de uploads en vuelo, eventos/streaming (long-poll) para watchers de UI.
- Consumidor CLI de `/v1/info` (p. ej. un futuro `onecloudriver status`) — la primera iteración se valida con `curl` + unit tests.

## 5. Ficheros involucrados

| Fichero | Cambio |
|---|---|
| `internal/control/control.go` (nuevo) | Tipos del modelo (`Info`, `DaemonInfo`, `MountInfo`, `ConfigInfo`) + interfaz `InfoProvider` |
| `internal/control/server.go` (nuevo) | Server HTTP sobre socket Unix: bind, stale cleanup, `0600`, rutas `/v1/*`, `Close()` + unlink |
| `internal/control/client.go` (nuevo) | Cliente con transporte unix + `Info(ctx)` tipado |
| `internal/control/server_test.go`, `client_test.go`, `info_test.go` (nuevos) | Tests TDD (temp dir, sin FUSE) |
| `internal/fs/mount.go` | Campo `MountConfig.ControlSocket` + default; arranque/parada del server; provider |
| `cmd/onecloudriver/mount.go` | Rellenar datos extra del provider (configFile/version) si se exponen |
| `.github/workflows/ci.yml` | (Recomendado) añadir `./internal/control/...` al job unit+race |
| `docs/plans/PLAN_8.md` | Este plan |
| `docs/MANUAL.*` | (Solo si se documenta el socket para usuarios avanzados) |

## 6. Riesgos y notas

- **Socket siempre activo**: al vivir dentro del cache dir `0700` del usuario y ser un fichero de socket local, no abre superficie de red. Si algún entorno lo necesita, `ControlSocket=""` desactiva.
- **Seguridad**: frontera = permisos de fichero (same-user). Endpoints solo-GET en esta entrega; las operaciones mutantes futuras llevarán validación/confirmación y rate-limit, y nunca expondrán tokens.
- **Interferencia con FUSE**: nula si el server corre en su propia goroutine y los handlers solo leen estado en memoria. No compartir locks con el hot path.
- **Regla de emojis**: toda salida de consola/logs usa `internal/printer`; el JSON del canal es dato, no salida de terminal.
