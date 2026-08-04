# Flujos de comunicación en onecloudriver

> Diagramas de secuencia detallados de las operaciones principales del sistema de archivos.

---

## 1. Flujo de lectura (Read)

```mermaid
sequenceDiagram
    participant K as Kernel FUSE
    participant DIN as DriveItemNode
    participant IC as InodeCache
    participant CC as ContentCache
    participant G as Graph API

    K->>DIN: read(fd, buf, 0)
    DIN->>CC: Open(id)
    CC-->>DIN: *os.File
    Note over DIN,CC: ReadResultFd(fd.Fd(), offset)<br/>zero-copy desde page cache
    DIN-->>K: datos
```

### Apertura del archivo (Open) — detalle

```mermaid
flowchart TD
    OPEN["DriveItemNode.Open(flags)"]
    OPEN --> DIR{"¿Es carpeta?"}
    DIR -->|Sí| EISDIR["return EISDIR"]
    DIR -->|No| CC_OPEN["contentCache.Open(id) → *os.File"]
    CC_OPEN --> RDMODE{"¿flags de acceso?"}

    RDMODE -->|O_RDONLY| LOCAL{"¿isLocalID(id)?"}
    LOCAL -->|Sí| HAS{"¿hasContentOnDisk?"}
    LOCAL -->|No| HAS
    HAS -->|No| DOWNLOAD["downloadContent()<br/>io.Pipe() conecta<br/>GetItemContentStream → InsertStream<br/>streaming HTTP con Range (10MB chunks)"]
    HAS -->|Sí| RETURN_RO["return (self, FOPEN_KEEP_CACHE)"]
    DOWNLOAD --> RETURN_RO

    RDMODE -->|O_WRONLY / O_RDWR| REMOTE{"¿remoto sin datos?"}
    REMOTE -->|Sí| DOWNLOAD2["downloadContent()<br/>(descargar primero, luego editar)"]
    REMOTE -->|No| TRUNC{"¿O_TRUNC?"}
    DOWNLOAD2 --> TRUNC
    TRUNC -->|Sí| TRUNCATE["fd.Truncate(0), Size=0<br/>hasChanges=true"]
    TRUNC -->|No| RETURN_RW["return (self, FOPEN_KEEP_CACHE)"]
    TRUNCATE --> RETURN_RW

    RETURN_RO --> CLOSE["Flush → Fsync → QueueUpload<br/>UploadManager (al cerrar FD)"]
```

---

## 2. Flujo de escritura (Write → Flush → Upload)

```mermaid
sequenceDiagram
    participant APP as Aplicación
    participant DIN as DriveItemNode
    participant CC as ContentCache
    participant UM as UploadManager
    participant G as Graph API

    APP->>DIN: write(fd, data, off)
    DIN->>CC: WriteAt(id, data, off)
    CC-->>DIN: bytes escritos
    Note over DIN: inode.Size = max(Size, off+n)<br/>inode.hasChanges = true
    DIN-->>APP: n bytes

    APP->>DIN: close(fd)
    DIN->>DIN: Flush()
    Note over DIN: Fsync()<br/>hasChanges=false<br/>QueueUpload(id, ...)

    DIN->>UM: encolar sesión<br/>(snapshot de ContentCache)

    Note over UM: uploadLoop (background)<br/>processSessions()

    loop Para cada sesión
        UM->>UM: executeUpload()
        alt ≤ 4MB
            UM->>G: PUT /content
        else > 4MB
            UM->>G: POST /createUploadSession
            UM->>G: PUT chunks
        end
        G-->>UM: DriveItem
        Note over UM: swap ID local → remoto
    end
    UM->>UM: complete
```

---

## 3. Flujo de creación de archivo (Create)

```mermaid
sequenceDiagram
    participant APP as Aplicación
    participant K as Kernel FUSE
    participant DIN as DriveItemNode
    participant ND as nodeDeps
    participant G as Graph API

    APP->>K: open("nuevo.txt", O_CREAT|O_RDWR)
    K->>DIN: Create()
    DIN->>ND: fuseCreate()

    ND->>ND: isNameRestricted? → EINVAL
    ND->>ND: doCreate()

    alt Nombre ya existe
        ND->>ND: GetChildren(cache)<br/>→ truncar
    else Nombre no existe
        ND->>ND: NewInodeLocal(name, mode)
        ND->>ND: InsertChild(parentID, name, inode)
        ND->>ND: contentCache.Open(localID)
    end

    ND-->>DIN: fuseInode + childNode (FileHandle)
    DIN-->>K: child
    K-->>APP: fd
```

---

## 4. Flujo de Delta Sync (sincronización remota)

```mermaid
sequenceDiagram
    participant DS as DeltaSync
    participant IC as InodeCache
    participant CC as ContentCache
    participant G as Graph API

    loop cada 5 min (ticker)
        DS->>DS: pollAndApply()
        DS->>G: GET /delta (con ?token=)
        G-->>DS: items[] + nextDeltaLink

        loop Para cada item delta
            alt Deleted ≠ nil
                DS->>IC: Delete(id)
                DS->>CC: Delete(id)
            else Item nuevo
                DS->>IC: Insert(item)
            else Item existente con ETag cambiado
                DS->>CC: Delete(id) (forzar redescarga)
                DS->>IC: Insert(item)
            else Parent cambiado (move)
                DS->>IC: MoveID(oldParent, newParent)
            end
        end

        DS->>IC: SetDeltaLink(nextLink)
        DS->>IC: SerializeAll()

        opt Error de red
            DS->>DS: SetOffline(true)<br/>backoff exponencial
        end
    end
```

---

## 5. Flujo de autenticación y refresh de tokens

```mermaid
sequenceDiagram
    participant ACC as Account
    participant MGR as Manager
    participant AF as Auth Flow
    participant KR as Keyring (SO)
    participant MS as Microsoft

    ACC->>MGR: GetAccessToken()
    MGR->>AF: Refresh()

    alt Token en memoria aún válido
        AF-->>MGR: access_token (cache hit)
    else Necesita refresh
        AF->>MS: POST /token<br/>refresh_token + client_id
        MS-->>AF: new access_token<br/>(+ refresh_token opcional)

        alt 4xx (invalid_grant)
            AF->>AF: purgeInvalidRefreshToken()
            AF->>KR: Delete()
            AF-->>MGR: error: requiere reautenticación
        else OK
            AF->>KR: Set(refresh_token)
            AF->>AF: os.WriteFile(JSON, 0600)
            AF-->>MGR: access_token
        end
    end
```

---

## 6. Flujo de montaje y desmontaje

```mermaid
sequenceDiagram
    participant CLI
    participant MT as Mount()
    participant IC as InodeCache
    participant CC as ContentCache
    participant FS as FUSE kernel

    CLI->>MT: mount
    MT->>IC: NewInodeCache()
    MT->>CC: NewContentCache(cacheDir)
    MT->>IC: InitBoltDB()
    IC-->>MT: carga metadatos anteriores
    MT->>IC: StartSweep()
    MT->>MT: DeltaSync.Start()
    MT->>MT: UploadManager.Start()
    MT->>FS: fs.Mount(mountpoint, root)
    FS-->>MT: server (bloqueante)
    MT-->>CLI: PID

    Note over CLI: Ctrl+C / SIGTERM / SIGHUP

    CLI->>MT: signal handler
    MT->>MT: cancelDelta() + DeltaSync.Stop()
    MT->>MT: UploadManager.Stop()
    MT->>IC: SerializeAll() → BoltDB
    MT->>FS: server.Unmount()

    alt Desmontaje OK
        FS-->>MT: ✓
    else Device or resource busy
        MT->>MT: fusermount3 -uz (lazy)
    end

    MT->>CC: CloseAll()
    MT->>IC: Close()
```

---

## 7. Flujo de borrado de cuenta con caché

```mermaid
sequenceDiagram
    participant CLI
    participant ARC as accountRemoveCmd
    participant MGR as Manager
    participant FS as Filesystem

    CLI->>ARC: account remove user@outlook.com
    ARC->>MGR: RemoveAccount(name)
    MGR->>MGR: keyring.Delete()
    MGR->>MGR: os.Remove(JSON)
    MGR-->>ARC: ok

    alt --purge
        ARC->>FS: os.RemoveAll(cachedir)
    else --keep
        Note over ARC: conservar caché
    else default
        ARC->>CLI: ¿Borrar caché? [s/N]
        CLI-->>ARC: respuesta
        opt Sí
            ARC->>FS: os.RemoveAll(cachedir)
        end
    end

    ARC-->>CLI: Cuenta eliminada
```

---

## 8. Ciclo de vida de un archivo local (creado offline → subido)

```mermaid
flowchart TD
    CREATED["Creado<br/>ID: local_uuid<br/>ContentCache: vacío (0 bytes)<br/>InodeCache: InsertChild → children del padre"]
    CREATED --> WRITTEN["Escrito<br/>ID: local_uuid<br/>ContentCache: WriteAt → datos<br/>InodeCache: hasChanges=true, Size actualizado"]
    WRITTEN --> FLUSHED["Flush/Fsync<br/>ID: local_uuid<br/>ContentCache: snapshot leída por QueueUpload<br/>InodeCache: hasChanges=false"]
    FLUSHED --> QUEUED["Encolado<br/>ID: local_uuid<br/>ContentCache: (snapshot)<br/>InodeCache: sesión en UploadManager.sessions"]
    QUEUED --> UPLOADING["Subiendo<br/>ID: local_uuid<br/>ContentCache: (snapshot)<br/>InodeCache: sesión en estado 'uploading'"]
    UPLOADING --> COMPLETED["Completado<br/>ID: id_remoto<br/>ContentCache: (sin cambios)<br/>InodeCache: MoveID(localID → remoteID)<br/>invalidate(parentID)"]

    style CREATED fill:#AB1AAf,stroke:#333
    style COMPLETED fill:#AB1AAF,stroke:#333
```

**Swap de ID:** tras la primera subida exitosa, el `Inode` cambia su ID de `local_<uuid>` al ID real de OneDrive. Las referencias en `children` del padre se actualizan con `MoveID()`.
