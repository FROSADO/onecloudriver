# API Documentation — onecloudriver

> **⚠️ This file is no longer maintained manually.**

API documentation is automatically generated from `godoc` comments in the source code.
To regenerate:

```bash
make docs
```

This produces the following files in `docs/api/`:

| File | Package |
|---|---|
| `docs/api/fs.md` | `internal/fs` — FUSE file system |
| `docs/api/auth.md` | `internal/auth` — OAuth2 Authentication |
| `docs/api/graph.md` | `internal/graph` — Microsoft Graph API |
| `docs/api/cmd.md` | `cmd/onecloudriver` — CLI |
| `docs/api/service.md` | `internal/service` — systemd integration and structured service results |

The output uses `go doc -all` which extracts public types, methods, and functions
directly from `//` comments in the source code. It is always up to date with the
current version of the code.

> This English version replaces the previous Spanish reference. The Spanish
> version has been saved as [GODOC.es.md](GODOC.es.md).
