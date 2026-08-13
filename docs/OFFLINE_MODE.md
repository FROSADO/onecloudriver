# Offline Mode in onecloudriver

> Documentation of the filesystem's operation without an internet connection.

---

## What is offline mode?

Offline mode allows continued use of the mounted filesystem even when there
is no internet connection. Previously cached metadata and content are served
from local storage (memory + BoltDB + disk):

- **Metadata** (names, sizes, dates) → from the inode tree persisted in
  BoltDB (`inodes.db`).
- **File content** → from `ContentCache` on disk (`<CacheDir>/content/`).

The goal is that an `ls` or `cat` of an already-visited file **works without
the network**, and operations on never-cached content fail cleanly (EIO)
without crashing.

### Components involved

| Component | File | Role in offline |
|---|---|---|
| `isNetworkError` | `internal/fs/root.go` | Decides whether an error is network-related (transient) |
| `fetchChildrenWithOffline` | `internal/fs/fs_ops.go` | Shared fetcher for root + subfolders with cache fallback |
| `ItemsByParent` | `internal/fs/cache.go` | Rebuilds child list after parent eviction |
| `InodeCache.SetOffline/IsOffline` | `internal/fs/cache.go` | Thread-safe offline state flag |
| `InodeCache.SerializeAll/DeserializeFromDisk` | `internal/fs/cache.go` | Inode tree persistence (BoltDB) |
| `ContentCache` | `internal/fs/content_cache.go` | File content on disk |
| Access token in keyring | `internal/auth/account.go` | Start without network without auth failure |

---

## How is it activated?

**Automatically** when a network operation fails with a transient error:

1. `fetchChildrenWithOffline()` attempts to query the Graph API
2. If `isNetworkError(err)` is `true`:
   - `inodeCache.SetOffline(true)`
   - Data is served from the local cache
3. On the next successful operation:
   - `inodeCache.SetOffline(false)` automatically

### `isNetworkError` — detail

Offline mode only activates on **network** errors (transient), not on
application-level HTTP errors (401, 429, 500), which require intervention.

```go
func isNetworkError(err error) bool {
    // Tier 1: Timeout()/Temporary() (DNSError, context deadline, ...)
    var netErr interface{ Timeout() bool; Temporary() bool }
    if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
        return true
    }
    // Tier 2: connection errnos via errors.Is
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

> ⚠️ **Why Tier 2 is necessary:** `net.OpError.Temporary()` returns `false`
> for `connection refused` — the exact error produced by a disconnected
> network or a dead proxy. Without Tier 2 with `errors.Is`, the fallback
> never activated on a real network cut.

---

## What works in offline mode?

| Operation | Works? | Notes |
|---|---|---|
| **Read files** | ✅ | Only files previously opened (in `ContentCache`) |
| **List folders** | ✅ | Metadata cached in `InodeCache` + BoltDB |
| **Stat / Getattr** | ✅ | Metadata in memory |
| **Navigate the tree** | ✅ | Structure persisted in BoltDB |
| **Write existing files** | ✅ | Buffered locally (write-back); uploaded on reconnect |
| **Create files/folders** | ❌ | Fails (`EIO`) — requires Graph API |
| **Delete** | ❌ | Fails (`EIO`) — requires Graph API |
| **Rename** | ❌ | Fails (`EIO`) — requires Graph API |
| **Refresh expired access token** | ❌ | Auth is not recoverable without network |

---

## Fallback flow

```
fetchChildrenWithOffline(ctx, parentID)
    │
    ├─ fetchChildrenFromGraph(...)   ← HTTP to Graph
    │
    ├─ Network error? (isNetworkError)
    │     ├─ SetOffline(true)
    │     ├─ Parent has children in memory? (IsChildrenFetched)
    │     │     └─ serve cached children  ✅
    │     ├─ Are there inodes with ParentID == parentID? (ItemsByParent)
    │     │     └─ rebuild the list from there  ✅
    │     └─ If nothing in cache → propagate the error (EIO)
    │
    └─ Success → SetOffline(false) if was offline
```

The fallback lives in a **single point**: `nodeDeps.fetchChildrenWithOffline`.
Both the root (`OneCloudFS`) and subfolders (`DriveItemNode`) delegate to it.
Before this centralization, only the root had a fallback — navigating to a
**subfolder** offline returned EIO even though metadata was cached.

### Emergency fallback: `ItemsByParent`

The TTL eviction sweep sets `children = nil` on inactive folders to free
memory — but **does not delete child inodes** from the `sync.Map`. Each
child retains its `ParentID`, so `ItemsByParent(parentID)` scans the
`sync.Map` and rebuilds the list even if the parent was evicted.

### `ParentID` — why it's critical

Microsoft Graph `ListChildren` **does not return `parentReference`** without
`$expand`. The code explicitly assigns:

```go
if inode.ParentID() == "" {
    inode.DriveItem.Parent = &graph.DriveItemParent{ID: parentID}
}
```

Without this assignment, child inodes become orphans (`ParentID=""`) and:
- `ItemsByParent` finds nothing → broken offline fallback in subfolders
- `SerializeAll` does not persist them → the subtree does not survive round-trip

---

## Persistence across restarts

On clean unmount (Ctrl+C, SIGTERM, SIGHUP):

1. `inodeCache.SerializeAll()` — flushes all metadata to BoltDB
2. Files in `ContentCache` remain on disk
3. On the next mount:
   - `inodeCache.InitBoltDB()` + `DeserializeFromDisk()` restore metadata
   - `ContentCache` reuses existing files on disk

For an offline mount to start correctly, the **access token** must be
available. It is stored in the OS keyring:

| Token | Where stored | Keyring key |
|---|---|---|
| Refresh token | keyring | `onecloudriver:<account>` |
| Access token | keyring | `onecloudriver:access:<account>` |
| Account JSON | disk `~/.config/onecloudriver/` | — (no tokens) |

This allows a subsequent mount **without connection** to start directly in
offline mode with all data from the previous session.

---

## TTL freshness

Cached metadata expires with the base TTL (default 60s, adjustable with
`--cache-ttl`). When browsing:

- **With network:** if children are stale, they are refetched from Graph.
- **Without network:** if children are stale, the refetch fails due to
  network → the fallback serves the cache anyway.

The fallback **does not hammer the network**: after serving the cache,
`childrenCachedAt` is reset, so the next `GetChildren` is a cache hit
until the TTL expires again.

---

## How to test offline mode

### Method 1: Broken proxy (recommended)

Using `HTTPS_PROXY` pointing to a closed port simulates a network cut
without affecting other system connections.

```bash
# 1. Normal mount (with network) and populate the cache
./onecloudriver mount /tmp/onedrive -a user@outlook.com &
ls /tmp/onedrive/                       # populate root
cat /tmp/onedrive/.xdg-volume-info      # cache content
sleep 2 && fusermount3 -u /tmp/onedrive

# 2. Remount with broken proxy
setsid env HTTPS_PROXY=http://127.0.0.1:1 HTTP_PROXY=http://127.0.0.1:1 \
    nohup ./onecloudriver mount /tmp/onedrive -a user@outlook.com \
    > /tmp/offline.log 2>&1 < /dev/null & disown

# 3. Verify
ls /tmp/onedrive/                       # ✅ from cache
cat /tmp/onedrive/.xdg-volume-info      # ✅ cached content
head -c 10 /tmp/onedrive/new_file       # ❌ EIO (never cached)
```

> ⚠️ `unshare -Urn` **DOES NOT work** for FUSE tests: `fusermount3` does not
> mount inside user namespaces. The broken proxy is the correct technique.

### Method 2: Cut the network

```bash
sudo ip link set wlan0 down   # or disconnect WiFi
ls /tmp/onedrive/             # should work from cache
sudo ip link set wlan0 up     # restore
```

### Method 3: Automated tests

```bash
# Mock tests
go test ./internal/fs/... -run 'TestNetworkError|TestOffline' -v -race

# Integration test with real mount
go test -tags=integration ./internal/fs/... -run TestIntegration_Offline -v
```

---

## Connection recovery

When the connection is restored:

1. The next successful network operation calls `SetOffline(false)`
2. Delta sync reactivates and applies accumulated changes
3. The `UploadManager` resumes uploading the buffered writes

No manual intervention is required. The offline→online transition is transparent.

---

## Security

- Access/refresh tokens are **not** persisted in plain text on disk
- The refresh token is stored in the OS keyring (encrypted)
- The access token is stored in the keyring (to allow offline mode after restart)
- Cached files have `0600` permissions
- The account JSON file has `0600` permissions
- BoltDB has `0600` permissions

---

## Related tests

| Test | What it verifies |
|---|---|
| `TestIsNetworkError_RealProxyError` | Real broken proxy error → `isNetworkError` true |
| `TestIsNetworkError_ConnectionRefused_RealScenario` | ECONNREFUSED wrapped in url.Error → true |
| `TestIsNetworkError_OtherConnErrnos` | ECONNRESET, EHOSTUNREACH, ENETUNREACH, ETIMEDOUT → true |
| `TestOneCloudFS_FetchChildren_OfflineFallback_StaleData` | stale children + network down → serves cache |
| `TestOneCloudFS_FetchChildren_OfflineFallback_EvictedParent` | evicted parent + network down → ItemsByParent |
| `TestDriveItemNode_Readdir_OfflineFallback_Subfolder` | Subfolder offline with fallback |
| `TestInodeCache_GetChildren_SetsParentID` | Graph does not return parentReference → explicitly assigned |
| `TestInodeCache_SerializeAll_PersistsChildFiles` | Files without children survive round-trip |
| `TestInodeCache_SerializeAll_PersistsEvictedSubtree` | Evicted subtree is rebuilt offline |
| `TestInodeCache_RestoredFromDisk_RefetchesStaleChildren` | Previous session → refetch with network, cache offline |
