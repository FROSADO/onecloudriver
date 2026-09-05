# onecloudriver control socket API

The mounted `onecloudriver` process exposes a **local control channel** over a
Unix domain socket. It speaks plain **HTTP/JSON** on versioned `/v1` endpoints
so any HTTP client (Go, `curl`, a future UI) can talk to the running mount.

- **Transport**: Unix domain socket (no TCP is ever opened).
- **Socket path**: `<cache-dir>/control.sock` by default (e.g.
  `~/.cache/onecloudriver/<account>/control.sock`). Override with
  `mount --control-socket /path/to.sock`; disable with
  `mount --control-socket off`.
- **Base URL**: `http://localhost` (the host part is ignored; the socket file
  is the transport). All paths are absolute under `/v1`.
- **Content type**: requests and responses are `application/json`.
- **Security**: the socket is created with mode `0600` inside the account's
  `0700` cache directory, so only the user who started the mount can connect.
  The API is read-only in this version and never exposes tokens or lets a
  caller read arbitrary files.
- **Errors**: non-2xx responses carry a JSON body
  `{"error":{"message":"..."}}`.

## Versioning

All endpoints live under `/v1`. Additive, non-breaking changes extend the
existing resources; breaking changes introduce `/v2`. The current version is
reported by `GET /v1/info` as `api.version = 1`.

## Endpoints

| Method | Path        | Description                          |
|--------|-------------|--------------------------------------|
| `GET`  | `/v1/info`  | State of the running mount process   |

Any other path returns `404 Not Found`; any method other than `GET` on
`/v1/info` returns `405 Method Not Allowed`.

### `GET /v1/info`

Returns a snapshot of the mounted process: identity of the channel, runtime
information of the daemon, the mounted account, the mountpoint/cache, and the
effective configuration.

Response `200 OK`:

```json
{
  "api": {
    "name": "onecloudriver-control",
    "version": 1
  },
  "daemon": {
    "pid": 12345,
    "startedAt": "2026-09-05T10:00:00Z",
    "goVersion": "go1.26.7",
    "binaryVersion": "v0.1.6"
  },
  "account": {
    "name": "user@outlook.com"
  },
  "mount": {
    "state": "running",
    "mountpoint": "/home/user/OneDrive/user@outlook.com",
    "cacheDir": "/home/user/.cache/onecloudriver/user@outlook.com",
    "configFile": "/home/user/.config/onecloudriver/user@outlook.com.json"
  },
  "config": {
    "cacheTTL": "1m0s",
    "cacheMaxEntries": 2000,
    "cacheMaxSize": "0",
    "deltaInterval": "5m0s",
    "maxUploadsInFlight": 5,
    "maxUploadRetries": 5,
    "graphRetries": 3,
    "httpTimeout": "15s",
    "preWarmDepth": 2,
    "debugAddr": ""
  }
}
```

Field semantics:

| Field | Meaning |
|---|---|
| `api.name` / `api.version` | Fixed protocol identity and version. |
| `daemon.pid` | PID of the mounted process. Owned by the server. |
| `daemon.startedAt` | When the control server started, RFC 3339 UTC. Owned by the server. |
| `daemon.goVersion` | Go runtime version of the daemon. Owned by the server. |
| `daemon.binaryVersion` | Binary version, when the build stamps it (`omitempty`). |
| `account.name` | Account being mounted. |
| `mount.state` | `"running"` for a live mount. |
| `mount.mountpoint` | Directory where the filesystem is mounted. |
| `mount.cacheDir` | Cache directory in use by this mount. |
| `mount.configFile` | Account JSON path, when available (`omitempty`). |
| `config.*` | Effective `MountConfig` values. Durations are strings (`"1m0s"`); `cacheMaxSize` is humanized, `"0"` = unlimited; `debugAddr` is empty unless `mount --debug` is active. |

## Examples

### Query the state of a running mount with `curl`

```bash
# Start a mount (control channel enabled by default)
onecloudriver mount ~/OneDrive/user@outlook.com -a user@outlook.com &

# Ask the running process for its state
curl --unix-socket ~/.cache/onecloudriver/user@outlook.com/control.sock \
     http://localhost/v1/info
```

### Communication over the socket

Because the transport is a Unix socket, every HTTP client that supports a
custom dialer can be used. Two equivalent requests, one with `curl` and one
with a raw socket:

```bash
# curl
curl --unix-socket /path/to/control.sock http://localhost/v1/info
```

```bash
# socat (any HTTP tooling over the raw socket)
printf 'GET /v1/info HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n' \
  | socat - UNIX-CONNECT:/path/to/control.sock
```

### Go client (`internal/control`)

The Go client is the supported way to consume the API from another
onecloudriver process (CLI or future UI):

```go
import "github.com/frosado/onecloudriver/internal/control"

client := control.NewClient("/home/user/.cache/onecloudriver/user@outlook.com/control.sock")
info, err := client.Info(ctx) // *control.Info
if err != nil {
    if errors.Is(err, control.ErrNotRunning) {
        // no mount is running for this cache dir
    }
}
```

### Error responses

```bash
# Unknown route -> 404
curl --unix-socket /path/to/control.sock http://localhost/v1/nope
# {"error":{"message":"not found"}}

# Wrong method on /v1/info -> 405
curl -X POST --unix-socket /path/to/control.sock http://localhost/v1/info
# {"error":{"message":"method not allowed"}}
```

## Socket lifecycle

- Created when the FUSE mount is up, removed on graceful shutdown (Ctrl+C /
  `service stop`) and on the normal unmount path.
- A stale socket file left by a crashed process is removed automatically when
  the next mount binds its path.
- The socket lives inside the account cache directory, so each account/cache
  has its own control channel. Two mounts sharing a cache directory are
  already prevented by the cache's exclusive lock.

## Future endpoints

This version only publishes `/v1/info`. Operations such as forcing a delta
sync, refreshing a folder or re-downloading content are designed to be added
here (as `POST /v1/...`) in later versions of the protocol.
