# API Documentation — onecloudriver

> **⚠️ Este archivo ya no se mantiene manualmente.**

La documentación de la API se genera automáticamente desde los comentarios `godoc`
del código fuente. Para regenerarla:

```bash
make docs
```

Esto genera los siguientes archivos en `docs/api/`:

| Archivo | Paquete |
|---|---|
| `docs/api/fs.md` | `internal/fs` — Sistema de archivos FUSE |
| `docs/api/auth.md` | `internal/auth` — Autenticación OAuth2 |
| `docs/api/graph.md` | `internal/graph` — Microsoft Graph API |
| `docs/api/cmd.md` | `cmd/onecloudriver` — CLI |

La salida usa `go doc -all` que extrae tipos públicos, métodos, y funciones
directamente de los comentarios `//` en el código fuente. Siempre está
actualizada con la versión actual del código.
