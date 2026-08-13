# onecloudriver Architecture

> Native file system for OneDrive on Linux using FUSE + Microsoft Graph API.

---

## Overview

onecloudriver mounts your OneDrive account as a local file system on Linux, allowing you to read, write, create, and delete files and folders directly from any application. It implements multi-level caching (memory + disk), bidirectional delta synchronization, offline mode, and asynchronous uploads with retries.

```mermaid
flowchart TD
    CLI["CLI (cmd/onecloudriver)<br/>account add | list | remove | mount | list | info | ..."]

    subgraph FS["OneCloudFS (internal/fs/)"]
        direction TB
        IC["InodeCache<br/>(memory + BoltDB)<br/>sync.Map + boltDB<br/>TTL+LFU Eviction"]
        CC["ContentCache<br/>(disk)<br/>~/.cache/onecloudriver/account/content/<br/>Age-based eviction"]
        DS["DeltaSync<br/>/me/drive/root/delta<br/>5 min polling<br/>applyDelta() → insert/update/delete"]
        UM["UploadManager<br/>Async upload queue<br/>Max 5 concurrent<br/>BoltDB persistence"]

        subgraph ND["nodeDeps (fs_ops.go)"]
            direction LR
            W["fuseLookup, fuseMkdir, fuseCreate<br/>fuseRmdir, fuseUnlink, fuseRename<br/>doMkdir, doCreate, doRename<br/>fetchChildrenWithOffline<br/>isNameRestricted"]
        end

        subgraph NODES["FUSE Nodes"]
            direction LR
            OCFS["OneCloudFS<br/>(root, parentID=root)"]
            DIN["DriveItemNode<br/>(files/folders)"]
        end
    end

    subgraph GRAPH["Microsoft Graph API (internal/graph/)"]
        direction LR
        CH["children / items<br/>GET/POST"]
        DL["download.go<br/>(range streaming)"]
        UP["upload.go<br/>(simple PUT + sessions)"]
        DT["delta.go<br/>(GET /delta polling)"]
    end

    AUTH["Auth (internal/auth/)<br/>OAuth2 PKCE → token → OS keyring<br/>Client ID: a074b377-e82d-41a0-b7b3-88c57c011510<br/>Redirect: http://localhost:9090/callback"]

    CLI --> FS
    FS --> GRAPH
    GRAPH --> AUTH

    NODES --> ND
    OCFS --> ND
    DIN --> ND
```

---

## Data Layer: InodeCache

### Tree Structure

```mermaid
graph TD
    root["root (Inode, ID=root)"]
    root --> folder1["folder1 (Inode, IsDir=true)<br/>childrenCachedAt: 2026-08-04<br/>childrenLastAccess: 2026-08-04<br/>childrenAccessCount: 3"]
    root --> file1["file1 (Inode, Size=1024)"]
    folder1 --> file2["file2 (Inode, ParentID=folder1)"]
    folder1 --> folder2["folder2 (Inode, ParentID=folder1, IsDir=true)"]
```

Each `Inode` is a thread-safe wrapper around `graph.DriveItem` that adds:

| Field | Type | Purpose |
|---|---|---|
| `children` | `[]string` | Child IDs (nil = not initialized) |
| `childrenCachedAt` | `time.Time` | When children were populated (for freshness) |
| `childrenLastAccess` | `time.Time` | Last access to children (for eviction) |
| `childrenAccessCount` | `uint64` | Counter with decay (for LFU scoring) |
| `hasChanges` | `bool` | Unuploaded local changes (write-back) |
| `subdir` | `uint32` | Subdirectories (for NLink = 2 + subdir) |
| `mode` | `uint32` | UNIX permissions (0644 files, 0755 folders) |

### Persistence (BoltDB)

```
~/.cache/onecloudriver/<account>/inodes.db
├── bucket "metadata": id → JSON(SerializeableInode)
└── bucket "delta":    "link" → deltaURL
```

- **SerializeAll()**: dumps `sync.Map` → BoltDB on unmount
- **DeserializeFromDisk()**: loads BoltDB → `sync.Map` on mount
- **SaveUploadSession()**: persists incomplete upload sessions

### Smart Eviction (TTL+LFU)

```mermaid
flowchart TD
    HIT["On each HIT (GetChildren):"] --> AC["accessCount++"]
    AC --> MUL["multiplier = 1.0 + accessCount × 0.5"]
    MUL --> EFF["effectiveTTL = baseTTL × multiplier"]

    SWEEP["On each SWEEP (every 60s):"] --> DECAY["accessCount >>= 1<br/>(decay: inactive folders cool down)"]
    DECAY --> CHECK{"accessCount == 0<br/>and TTL expired?"}
    CHECK -->|Yes| FREE["children = nil<br/>(free memory)"]
    CHECK -->|No| KEEP["keep"]
    KEEP --> LIMIT{"folder count w/ children<br/>> maxEntries?"}
    LIMIT -->|Yes| EVICT["evict lowest score"]
    LIMIT -->|No| DONE["done"]
```

**Freshness vs. Eviction:** Freshness uses `childrenCachedAt` (anchored) to decide refetch. Eviction uses `childrenLastAccess` (sliding) to decide what to free. This prevents stale data from being served indefinitely.

---

## Data Layer: ContentCache

### Structure

```
~/.cache/onecloudriver/<account>/content/
├── <remote_id_1>    ← file downloaded from OneDrive
├── <remote_id_2>
├── local_<uuid>     ← locally created file (not yet uploaded)
└── ...
```

| Field | Type | Purpose |
|---|---|---|
| `directory` | `string` | Path on disk |
| `fds` | `sync.Map` | id → `*os.File` (reusable FDs) |
| `meta` | `sync.Map` | id → `*contentMeta` (accessCount, lastAccess) |
| `maxSize` | `int64` | Limit in bytes (0 = unlimited) |
| `currentSize` | `atomic.Int64` | Current size on disk |
| `evictMu` | `sync.Mutex` | Serializes eviction vs. file creation |

### Zero-copy reads

```go
fd, _ := contentCache.Open(id)
return fuse.ReadResultFd(fd.Fd(), offset, size)
```

The FD is reused between reads. Data is not copied to user space.

### Age-based eviction

- Sweeps files by `mtime` (age on disk)
- **Never** evicts files with `IsOpen(id) == true`
- `evictMu` serializes check + deletion (prevents TOCTOU race with `Open()`)
- Score = `accessCount / (minutesSinceLastAccess + 1)`

---

## Communication with Microsoft Graph

### Authentication Flow

```mermaid
sequenceDiagram
    participant U as User
    participant OCR as onecloudriver
    participant MS as Microsoft

    U->>OCR: account add
    OCR->>MS: GET /authorize
    MS-->>OCR: redirect_uri + code
    OCR-->>U: Opens browser
    U->>OCR: authorizes in browser
    OCR->>MS: POST /token (code)
    MS-->>OCR: access + refresh token
    OCR->>OCR: Stores refresh in OS keyring
```

### Used Endpoints

| Operation | Method | Endpoint |
|---|---|---|
| List children | GET | `/me/drive/items/{id}/children` |
| Item metadata | GET | `/me/drive/items/{id}` |
| Search by path | GET | `/me/drive/root:/path` |
| Download content | GET | `/me/drive/items/{id}/content` (with Range) |
| Upload file (≤4MB) | PUT | `/me/drive/items/{parentId}:/{name}:/content` |
| Upload file (>4MB) | POST | `/me/drive/items/{parentId}:/{name}:/createUploadSession` |
| Create folder | POST | `/me/drive/items/{parentId}/children` |
| Delete | DELETE | `/me/drive/items/{id}` |
| Rename | PATCH | `/me/drive/items/{id}` |
| Move | PATCH | `/me/drive/items/{id}` (with parentReference) |
| Delta | GET | `/me/drive/root/delta` |

---

## Offline Mode

When there is no internet connection:

1. `fetchChildrenWithOffline()` detects `isNetworkError(err)` → `SetOffline(true)`
2. **Read:** serves metadata from `InodeCache` (memory + BoltDB)
3. **Content:** serves files from `ContentCache` (local disk)
4. **Write:** buffered locally (write-back); the `UploadManager` uploads it in the background once the connection returns (structural mutations such as create/delete still fail with `EIO` because they require Graph)
5. Upon regaining connection: `SetOffline(false)` automatically

---

## CLI Configuration

```
onecloudriver mount /mnt/onedrive -a user@outlook.com \
  --cache-dir=/path/to/cache \
  --cache-ttl=120s \
  --cache-max-entries=5000 \
  --cache-max-size=2GB
```

| Flag | Default | Description |
|---|---|---|
| `--cache-dir` | `~/.cache/onecloudriver/<account>` | Cache tree root |
| `--cache-ttl` | `60s` | Metadata base TTL |
| `--cache-max-entries` | `2000` | Max folders with cached children |
| `--cache-max-size` | `0` (unlimited) | ContentCache maximum size |
