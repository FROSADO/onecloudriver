# Communication Flows in onecloudriver

> Detailed sequence diagrams of the main filesystem operations.

---

## 1. Read flow

```mermaid
sequenceDiagram
    participant K as FUSE Kernel
    participant DIN as DriveItemNode
    participant IC as InodeCache
    participant CC as ContentCache
    participant G as Graph API

    K->>DIN: read(fd, buf, 0)
    DIN->>CC: Open(id)
    CC-->>DIN: *os.File
    Note over DIN,CC: ReadResultFd(fd.Fd(), offset)<br/>zero-copy from page cache
    DIN-->>K: data
```

### File open (Open) — detail

```mermaid
flowchart TD
    OPEN["DriveItemNode.Open(flags)"]
    OPEN --> DIR{"Is folder?"}
    DIR -->|Yes| EISDIR["return EISDIR"]
    DIR -->|No| CC_OPEN["contentCache.Open(id) → *os.File"]
    CC_OPEN --> RDMODE{"Access flags?"}

    RDMODE -->|O_RDONLY| LOCAL{"isLocalID(id)?"}
    LOCAL -->|Yes| HAS{"hasContentOnDisk?"}
    LOCAL -->|No| HAS
    HAS -->|No| DOWNLOAD["downloadContent()<br/>io.Pipe() connects<br/>GetItemContentStream → InsertStream<br/>HTTP streaming with Range (10MB chunks)"]
    HAS -->|Yes| RETURN_RO["return (self, FOPEN_KEEP_CACHE)"]
    DOWNLOAD --> RETURN_RO

    RDMODE -->|O_WRONLY / O_RDWR| REMOTE{"Remote without data?"}
    REMOTE -->|Yes| DOWNLOAD2["downloadContent()<br/>(download first, then edit)"]
    REMOTE -->|No| TRUNC{"O_TRUNC?"}
    DOWNLOAD2 --> TRUNC
    TRUNC -->|Yes| TRUNCATE["fd.Truncate(0), Size=0<br/>hasChanges=true"]
    TRUNC -->|No| RETURN_RW["return (self, FOPEN_KEEP_CACHE)"]
    TRUNCATE --> RETURN_RW

    RETURN_RO --> CLOSE["Flush → Fsync → QueueUpload<br/>UploadManager (on FD close)"]
```

---

## 2. Write flow (Write → Flush → Upload)

```mermaid
sequenceDiagram
    participant APP as Application
    participant DIN as DriveItemNode
    participant CC as ContentCache
    participant UM as UploadManager
    participant G as Graph API

    APP->>DIN: write(fd, data, off)
    DIN->>CC: WriteAt(id, data, off)
    CC-->>DIN: bytes written
    Note over DIN: inode.Size = max(Size, off+n)<br/>inode.hasChanges = true
    DIN-->>APP: n bytes

    APP->>DIN: close(fd)
    DIN->>DIN: Flush()
    Note over DIN: Fsync()<br/>hasChanges=false<br/>QueueUpload(id, ...)

    DIN->>UM: enqueue session<br/>(ContentCache snapshot)

    Note over UM: uploadLoop (background)<br/>processSessions()

    loop For each session
        UM->>UM: executeUpload()
        alt ≤ 4MB
            UM->>G: PUT /content
        else > 4MB
            UM->>G: POST /createUploadSession
            UM->>G: PUT chunks
        end
        G-->>UM: DriveItem
        Note over UM: swap ID local → remote
    end
    UM->>UM: complete
```

---

## 3. File creation flow (Create)

```mermaid
sequenceDiagram
    participant APP as Application
    participant K as FUSE Kernel
    participant DIN as DriveItemNode
    participant ND as nodeDeps
    participant G as Graph API

    APP->>K: open("new.txt", O_CREAT|O_RDWR)
    K->>DIN: Create()
    DIN->>ND: fuseCreate()

    ND->>ND: isNameRestricted? → EINVAL
    ND->>ND: doCreate()

    alt Name already exists
        ND->>ND: GetChildren(cache)<br/>→ truncate
    else Name does not exist
        ND->>ND: NewInodeLocal(name, mode)
        ND->>ND: InsertChild(parentID, name, inode)
        ND->>ND: contentCache.Open(localID)
    end

    ND-->>DIN: fuseInode + childNode (FileHandle)
    DIN-->>K: child
    K-->>APP: fd
```

---

## 4. Delta Sync flow (remote synchronization)

```mermaid
sequenceDiagram
    participant DS as DeltaSync
    participant IC as InodeCache
    participant CC as ContentCache
    participant G as Graph API

    loop every 5 min (ticker)
        DS->>DS: pollAndApply()
        DS->>G: GET /delta (with ?token=)
        G-->>DS: items[] + nextDeltaLink

        loop For each delta item
            alt Deleted ≠ nil
                DS->>IC: Delete(id)
                DS->>CC: Delete(id)
            else New item
                DS->>IC: Insert(item)
            else Existing item with changed ETag
                DS->>CC: Delete(id) (force re-download)
                DS->>IC: Insert(item)
            else Parent changed (move)
                DS->>IC: MoveID(oldParent, newParent)
            end
        end

        DS->>IC: SetDeltaLink(nextLink)
        DS->>IC: SerializeAll()

        opt Network error
            DS->>DS: SetOffline(true)<br/>exponential backoff
        end
    end
```

---

## 5. Authentication and token refresh flow

```mermaid
sequenceDiagram
    participant ACC as Account
    participant MGR as Manager
    participant AF as Auth Flow
    participant KR as Keyring (OS)
    participant MS as Microsoft

    ACC->>MGR: GetAccessToken()
    MGR->>AF: Refresh()

    alt Token still valid in memory
        AF-->>MGR: access_token (cache hit)
    else Needs refresh
        AF->>MS: POST /token<br/>refresh_token + client_id
        MS-->>AF: new access_token<br/>(+ optional refresh_token)

        alt 4xx (invalid_grant)
            AF->>AF: purgeInvalidRefreshToken()
            AF->>KR: Delete()
            AF-->>MGR: error: re-authentication required
        else OK
            AF->>KR: Set(refresh_token)
            AF->>AF: os.WriteFile(JSON, 0600)
            AF-->>MGR: access_token
        end
    end
```

---

## 6. Mount and unmount flow

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
    IC-->>MT: loads previous metadata
    MT->>IC: StartSweep()
    MT->>MT: DeltaSync.Start()
    MT->>MT: UploadManager.Start()
    MT->>FS: fs.Mount(mountpoint, root)
    FS-->>MT: server (blocking)
    MT-->>CLI: PID

    Note over CLI: Ctrl+C / SIGTERM / SIGHUP

    CLI->>MT: signal handler
    MT->>MT: cancelDelta() + DeltaSync.Stop()
    MT->>MT: UploadManager.Stop()
    MT->>IC: SerializeAll() → BoltDB
    MT->>FS: server.Unmount()

    alt Unmount OK
        FS-->>MT: ✓
    else Device or resource busy
        MT->>MT: fusermount3 -uz (lazy)
    end

    MT->>CC: CloseAll()
    MT->>IC: Close()
```

---

## 7. Account deletion with cache flow

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
        Note over ARC: keep cache
    else default
        ARC->>CLI: Delete cache? [y/N]
        CLI-->>ARC: response
        opt Yes
            ARC->>FS: os.RemoveAll(cachedir)
        end
    end

    ARC-->>CLI: Account removed
```

---

## 8. Lifecycle of a local file (created offline → uploaded)

```mermaid
flowchart TD
    CREATED["Created<br/>ID: local_uuid<br/>ContentCache: empty (0 bytes)<br/>InodeCache: InsertChild → parent children"]
    CREATED --> WRITTEN["Written<br/>ID: local_uuid<br/>ContentCache: WriteAt → data<br/>InodeCache: hasChanges=true, Size updated"]
    WRITTEN --> FLUSHED["Flush/Fsync<br/>ID: local_uuid<br/>ContentCache: snapshot read by QueueUpload<br/>InodeCache: hasChanges=false"]
    FLUSHED --> QUEUED["Queued<br/>ID: local_uuid<br/>ContentCache: (snapshot)<br/>InodeCache: session in UploadManager.sessions"]
    QUEUED --> UPLOADING["Uploading<br/>ID: local_uuid<br/>ContentCache: (snapshot)<br/>InodeCache: session in 'uploading' state"]
    UPLOADING --> COMPLETED["Completed<br/>ID: remote_id<br/>ContentCache: (unchanged)<br/>InodeCache: MoveID(localID → remoteID)<br/>invalidate(parentID)"]

    style CREATED fill:#AB1AAf,stroke:#333
    style COMPLETED fill:#AB1AAF,stroke:#333
```

**ID swap:** after the first successful upload, the `Inode` changes its ID from `local_<uuid>` to the real OneDrive ID. References in the parent's `children` are updated with `MoveID()`.
