# Changelog

Todas las mejoras notables de OneCloudRiver están documentadas aquí.

El formato sigue [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
y el proyecto adhiere a [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [0.1.0] - 2026-08-05

### 🗂️ Sistema de Archivos FUSE

- **Lecto-escritura completo**: `Create`, `Write`, `Mkdir`, `Rmdir`, `Unlink`, `Rename`, `Fsync`, `Flush`

### 💾 Caché de Contenido (`ContentCache`)

- Almacenamiento en disco de cache
- Evicción por tamaño con `evictMu` (TOCTOU-safe)

### 🔄 Sincronización Delta (`DeltaSync`)

- Polling del endpoint `/delta` de Microsoft Graph cada N minutos (default: 5)
- Soporte para los 3 casos delta: item nuevo, modificado (incluyendo mover entre carpetas), y eliminado
- `deltaLink` persistido en BoltDB → retoma desde la última sincronización tras reinicio
- Resolución de conflictos: items locales (IDs con prefijo `local:`) reconcileados contra remotos

### 🔐 Autenticación

- Flujo OAuth2 con Microsoft Identity Platform
- Fallback copy-paste cuando el navegador no está disponible
- Credenciales se alamacenan en el Keyring


---

[0.1.0]: https://github.com/frosado/onecloudriver/releases/tag/v0.1.0
