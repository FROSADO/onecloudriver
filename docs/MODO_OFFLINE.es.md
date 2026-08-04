# Modo Offline en onecloudriver

> Documentación del funcionamiento del sistema de archivos sin conexión a internet.

---

## ¿Qué es el modo offline?

El modo offline permite seguir usando el sistema de archivos montado aunque no
haya conexión a internet. Los metadatos y el contenido previamente cacheados se
sirven desde el almacenamiento local (memoria + BoltDB + disco):

- **Metadatos** (nombres, tamaños, fechas) → desde el árbol de inodos
  persistido en BoltDB (`inodes.db`).
- **Contenido de archivos** → desde `ContentCache` en disco
  (`<CacheDir>/content/`).

El objetivo es que un `ls` o `cat` de un archivo ya visitado **funcione sin
red**, y que las operaciones sobre contenido nunca cacheado fallen de forma
limpia (EIO) sin crashear.

### Piezas implicadas

| Pieza | Archivo | Rol en offline |
|---|---|---|
| `isNetworkError` | `internal/fs/root.go` | Decide si un error es de red (transitorio) o no |
| `fetchChildrenWithOffline` | `internal/fs/fs_ops.go` | Fetcher compartido raíz + subcarpetas con fallback a caché |
| `ItemsByParent` | `internal/fs/cache.go` | Reconstruye la lista de hijos tras evicción del padre |
| `InodeCache.SetOffline/IsOffline` | `internal/fs/cache.go` | Flag thread-safe de estado offline |
| `InodeCache.SerializeAll/DeserializeFromDisk` | `internal/fs/cache.go` | Persistencia del árbol de inodos (BoltDB) |
| `ContentCache` | `internal/fs/content_cache.go` | Contenido de archivos en disco |
| Access token en keyring | `internal/auth/account.go` | Arrancar sin red sin fallar por auth |

---

## ¿Cómo se activa?

**Automáticamente** cuando una operación de red falla con un error transitorio:

1. `fetchChildrenWithOffline()` intenta consultar Graph API
2. Si `isNetworkError(err)` es `true`:
   - `inodeCache.SetOffline(true)`
   - Se sirven los datos desde la caché local
3. En la siguiente operación exitosa:
   - `inodeCache.SetOffline(false)` automáticamente

### `isNetworkError` — detalle

El modo offline solo se activa ante errores **de red** (transitorios), no ante
errores HTTP de aplicación (401, 429, 500), que requieren intervención.

```go
func isNetworkError(err error) bool {
    // Tier 1: Timeout()/Temporary() (DNSError, context deadline, ...)
    var netErr interface{ Timeout() bool; Temporary() bool }
    if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
        return true
    }
    // Tier 2: errnos de conexión via errors.Is
    // (ECONNREFUSED, ECONNRESET, EHOSTUNREACH, ENETUNREACH, ETIMEDOUT)
    for _, target := range []error{
        syscall.ECONNREFUSED, syscall.ECONNRESET,
        syscall.EHOSTUNREACH, syscall.ENETUNREACH, syscall.ETIMEDOUT,
    } {
        if errors.Is(err, target) { return true }
    }
    return false
}
```

> ⚠️ **Por qué el Tier 2 es necesario:** `net.OpError.Temporary()` devuelve
> `false` para `connection refused` — el error exacto que produce una red
> cortada o un proxy caído. Sin el Tier 2 con `errors.Is`, el fallback nunca
> se activaba ante un corte real de red.

---

## ¿Qué funciona en modo offline?

| Operación | ¿Funciona? | Notas |
|---|---|---|
| **Leer archivos** | ✅ | Solo archivos previamente abiertos (en `ContentCache`) |
| **Listar carpetas** | ✅ | Metadatos cacheados en `InodeCache` + BoltDB |
| **Stat / Getattr** | ✅ | Metadatos en memoria |
| **Navegar el árbol** | ✅ | Estructura persistida en BoltDB |
| **Escribir archivos** | ❌ | Rechazado (`EROFS`) para evitar pérdida de datos |
| **Crear archivos/carpetas** | ❌ | Rechazado (`EROFS`) |
| **Eliminar** | ❌ | Rechazado (`EROFS`) |
| **Renombrar** | ❌ | Rechazado (`EROFS`) |
| **Renovar access token expirado** | ❌ | Auth no es recuperable sin red |

---

## Flujo del fallback

```
fetchChildrenWithOffline(ctx, parentID)
    │
    ├─ fetchChildrenFromGraph(...)   ← HTTP a Graph
    │
    ├─ ¿error de red? (isNetworkError)
    │     ├─ SetOffline(true)
    │     ├─ ¿el padre tiene children en memoria? (IsChildrenFetched)
    │     │     └─ servir children cacheados  ✅
    │     ├─ ¿hay inodos con ParentID == parentID? (ItemsByParent)
    │     │     └─ reconstruir la lista desde ahí  ✅
    │     └─ si no hay nada en caché → propagar el error (EIO)
    │
    └─ éxito → SetOffline(false) si estaba offline
```

El fallback vive en **un único punto**: `nodeDeps.fetchChildrenWithOffline`.
Tanto la raíz (`OneCloudFS`) como las subcarpetas (`DriveItemNode`) delegan en
él. Antes de esta centralización, solo la raíz tenía fallback — navegar a una
**subcarpeta** offline devolvía EIO aunque los metadatos estuvieran cacheados.

### Fallback de emergencia: `ItemsByParent`

El sweep de evicción TTL pone `children = nil` en carpetas inactivas para
liberar memoria — pero **no borra los inodos hijos** del `sync.Map`. Cada hijo
conserva su `ParentID`, así que `ItemsByParent(parentID)` escanea el `sync.Map`
y reconstruye la lista aunque el padre hubiera sido evictado.

### `ParentID` — por qué es crítico

Microsoft Graph `ListChildren` **no devuelve `parentReference`** sin
`$expand`. El código asigna explícitamente:

```go
if inode.ParentID() == "" {
    inode.DriveItem.Parent = &graph.DriveItemParent{ID: parentID}
}
```

Sin esta asignación, los inodos hijos quedan huérfanos (`ParentID=""`) y:
- `ItemsByParent` no encuentra nada → fallback offline roto en subcarpetas
- `SerializeAll` no los persiste → el subárbol no sobrevive al round-trip

---

## Persistencia entre reinicios

Al desmontar limpiamente (Ctrl+C, SIGTERM, SIGHUP):

1. `inodeCache.SerializeAll()` — vuelca todos los metadatos a BoltDB
2. Los archivos en `ContentCache` permanecen en disco
3. En el siguiente montaje:
   - `inodeCache.InitBoltDB()` + `DeserializeFromDisk()` restauran metadatos
   - `ContentCache` reutiliza los archivos existentes en disco

Para que un montaje sin red arranque correctamente, el **access token** debe
estar disponible. Se guarda en el keyring del SO:

| Token | Dónde se guarda | Clave keyring |
|---|---|---|
| Refresh token | keyring | `onecloudriver:<cuenta>` |
| Access token | keyring | `onecloudriver:access:<cuenta>` |
| JSON cuenta | disco `~/.config/onecloudriver/` | — (sin tokens) |

Esto permite que un montaje posterior **sin conexión** arranque directamente en
modo offline con todos los datos de la sesión anterior.

---

## Frescura TTL

Los metadatos cacheados expiran con el TTL base (por defecto 60s, ajustable con
`--cache-ttl`). Al navegar:

- **Con red:** si los children están stale, se refetchean de Graph.
- **Sin red:** si los children están stale, el refetch falla por red → el
  fallback sirve la caché igualmente.

El fallback **no martillea la red**: tras servir la caché, `childrenCachedAt`
se resetea, así que el siguiente `GetChildren` es un cache hit hasta que el TTL
vuelva a expirar.

---

## Cómo probar el modo offline

### Método 1: Proxy roto (recomendado)

Usar `HTTPS_PROXY` apuntando a un puerto cerrado simula un corte de red sin
afectar a otras conexiones del sistema.

```bash
# 1. Montar normal (con red) y poblar la caché
./onecloudriver mount /tmp/onedrive -a user@outlook.com &
ls /tmp/onedrive/                       # poblar raíz
cat /tmp/onedrive/.xdg-volume-info      # cachear contenido
sleep 2 && fusermount3 -u /tmp/onedrive

# 2. Remontar con proxy roto
setsid env HTTPS_PROXY=http://127.0.0.1:1 HTTP_PROXY=http://127.0.0.1:1 \
    nohup ./onecloudriver mount /tmp/onedrive -a user@outlook.com \
    > /tmp/offline.log 2>&1 < /dev/null & disown

# 3. Verificar
ls /tmp/onedrive/                       # ✅ desde caché
cat /tmp/onedrive/.xdg-volume-info      # ✅ contenido cacheado
head -c 10 /tmp/onedrive/archivo_nuevo  # ❌ EIO (nunca cacheado)
```

> ⚠️ `unshare -Urn` **NO funciona** para tests FUSE: `fusermount3` no monta
> dentro de user namespaces. El proxy roto es la técnica correcta.

### Método 2: Cortar la red

```bash
sudo ip link set wlan0 down   # o desconectar WiFi
ls /tmp/onedrive/             # debería funcionar desde caché
sudo ip link set wlan0 up     # restaurar
```

### Método 3: Tests automáticos

```bash
# Tests mock
go test ./internal/fs/... -run 'TestNetworkError|TestOffline' -v -race

# Test de integración con montaje real
go test -tags=integration ./internal/fs/... -run TestIntegration_Offline -v
```

---

## Recuperación de conexión

Al recuperar la conexión:

1. La siguiente operación de red exitosa llama a `SetOffline(false)`
2. El delta sync se reactiva y aplica los cambios acumulados
3. Las operaciones de escritura vuelven a permitirse

No se requiere intervención manual. La transición offline→online es transparente.

---

## Seguridad

- Los tokens de acceso/refresh **no** se persisten en texto plano en disco
- El refresh token se guarda en el keyring del SO (cifrado)
- El access token se guarda en el keyring (para permitir modo offline tras reinicio)
- Los archivos cacheados tienen permisos `0600`
- El archivo JSON de cuenta tiene permisos `0600`
- BoltDB tiene permisos `0600`

---

## Tests relacionados

| Test | Qué verifica |
|---|---|
| `TestIsNetworkError_RealProxyError` | Error real de proxy roto → `isNetworkError` true |
| `TestIsNetworkError_ConnectionRefused_RealScenario` | ECONNREFUSED envuelto en url.Error → true |
| `TestIsNetworkError_OtherConnErrnos` | ECONNRESET, EHOSTUNREACH, ENETUNREACH, ETIMEDOUT → true |
| `TestOneCloudFS_FetchChildren_OfflineFallback_StaleData` | children stale + red caída → sirve caché |
| `TestOneCloudFS_FetchChildren_OfflineFallback_EvictedParent` | padre evictado + red caída → ItemsByParent |
| `TestDriveItemNode_Readdir_OfflineFallback_Subfolder` | Subcarpeta offline con fallback |
| `TestInodeCache_GetChildren_SetsParentID` | Graph no devuelve parentReference → se asigna explícitamente |
| `TestInodeCache_SerializeAll_PersistsChildFiles` | Archivos sin children sobreviven round-trip |
| `TestInodeCache_SerializeAll_PersistsEvictedSubtree` | Subárbol evictado se reconstruye offline |
| `TestInodeCache_RestoredFromDisk_RefetchesStaleChildren` | Sesión anterior → refetch con red, caché offline |
