# OneCloudRiver

> **Nativo. Rápido. Sin intermediarios.**

[![CI](https://github.com/FROSADO/onecloudriver/actions/workflows/ci.yml/badge.svg)](https://github.com/FROSADO/onecloudriver/actions/workflows/ci.yml)
[![Coverage Status](https://coveralls.io/repos/github/FROSADO/onecloudriver/badge.svg)](https://coveralls.io/github/FROSADO/onecloudriver)
[![Go Version](https://img.shields.io/badge/Go-1.25-blue)](https://go.dev/)
[![License](https://img.shields.io/badge/license-GPLv3-green)](LICENSE)
[![Version](https://img.shields.io/badge/version-0.1.3-orange)](https://github.com/FROSADO/onecloudriver/releases/tag/v0.1.3)

**[🇬🇧 English version](README.md)**

---

> ⚠️ **Aviso sobre el origen del proyecto**
>
> Este proyecto está **fuertemente inspirado por [onedriver](https://github.com/jstaf/onedriver)**,
> el sistema de archivos FUSE nativo para OneDrive creado por [@jstaf](https://github.com/jstaf).
>
> OneCloudRiver **no es un fork de GitHub** — la cantidad de cambios, la reorganización
> completa de la arquitectura, y las nuevas funcionalidades añadidas hacen que sea un
> proyecto independiente con su propia identidad. Sin embargo, queremos reconocer
> explícitamente el trabajo fundacional de onedriver, sin el cual este proyecto no existiría.

---

OneCloudRiver monta tu **OneDrive como un sistema de archivos FUSE nativo en Linux**,
permitiéndote leer, escribir, crear y borrar archivos directamente desde tu gestor de
archivos (Nautilus, Dolphin, Thunar) y terminal.

Los archivos se descargan únicamente cuando los necesitas. OneCloudRiver te da acceso
instantáneo a todos tus archivos y solo descarga los que realmente usas—sin esperar horas
a que un cliente de sincronización transfiera toda tu cuenta. Un archivo solo se
redescargará si ha cambiado remotamente en OneDrive y lo accedes nuevamente.

## 🚀 Características

- **Lecto-escritura completa** — `Create`, `Write`, `Mkdir`, `Rmdir`, `Unlink`, `Rename`, `Chmod`, `Touch`
- **Modo offline** — Archivos cacheados disponibles sin conexión a Internet
- **Sincronización Delta** — Cambios remotos detectados automáticamente vía Microsoft Graph
- **Upload Manager** — Subidas asíncronas con reintentos y control de concurrencia
- **Caché inteligente** — Metadatos con evicción TTL+LFU, contenido en disco con evicción por tamaño
- **Persistencia BoltDB** — Estado del sistema de archivos preservado entre sesiones
- **Servicio systemd** — Automontaje al iniciar sesión (`onecloudriver service install`)
- **CLI completo** — Operaciones sin montar: `list`, `download`, `upload`, `info`, `mkdir`, `mv`, `cp`, `rm`, `rename`
- **Health check** — Verificación de conectividad y token antes de montar, con diagnóstico claro
- **Retry con backoff** — Reintentos automáticos en 429/503/errores de red
- **80%+ cobertura de tests** — Unitarios, integración FUSE, fuzzing, y auditoría de seguridad

## 📦 Instalación rápida

### Desde binario

```bash
# Descargar el último release
wget https://github.com/FROSADO/onecloudriver/releases/download/v0.1.3/onecloudriver_linux_amd64.zip
unzip onecloudriver_linux_amd64.zip
sudo cp onecloudriver /usr/local/bin/
```

### Desde paquete .deb

```bash
# Descargar e instalar el paquete .deb
wget https://github.com/FROSADO/onecloudriver/releases/download/v0.1.3/onecloudriver_0.1.3_amd64.deb
sudo dpkg -i onecloudriver_0.1.3_amd64.deb
```

Instalar el .deb también registra la página de manual — prueba `man onecloudriver` tras la instalación.

> 💡 El paquete también incluye la plantilla de servicio systemd **de usuario**
> (`/usr/lib/systemd/user/onecloudriver@.service`). Actívala para una cuenta de
> serie con:
> ```bash
> systemctl --user daemon-reload
> systemctl --user enable --now 'onecloudriver@usuario@outlook.com.service'
> ```
> Consulta [el manual](docs/MANUAL.es.md#la-unit-de-servicio-empaquetada-debrpm) para la semántica de los specifiers `%i`/`%h` y su relación con `service install`.

### Desde paquete .rpm

```bash
# Descargar el paquete .rpm
wget https://github.com/FROSADO/onecloudriver/releases/download/v0.1.3/onecloudriver-0.1.3-1.x86_64.rpm
```

Instala con el gestor de paquetes de tu distro (requiere `fuse3`, que se resuelve automáticamente):

```bash
# Fedora / RHEL 8+ / Rocky Linux / AlmaLinux
sudo dnf install ./onecloudriver-0.1.3-1.x86_64.rpm

# RHEL / CentOS 7 (antiguos)
sudo yum install ./onecloudriver-0.1.3-1.x86_64.rpm

# openSUSE
sudo zypper install ./onecloudriver-0.1.3-1.x86_64.rpm
```

El .rpm instala el binario en `/usr/local/bin`, la página de manual (`man onecloudriver`),
la plantilla de servicio systemd y la documentación en `/usr/share/doc/onecloudriver/`.

### Desde código

```bash
git clone https://github.com/FROSADO/onecloudriver.git
cd onecloudriver
make build
sudo cp onecloudriver /usr/local/bin/
```

### Requisitos

- **Go 1.25+**
- **FUSE 3** (`libfuse3` o `fuse3`)
- **Git** (para build desde fuente)

```bash
# Ubuntu/Debian
sudo apt-get install fuse3

# Fedora
sudo dnf install fuse3-libs fuse3
```

## ✅ Verificación de artefactos de release

Cada release de GitHub publica un manifest de checksums firmado y firmas GPG por artefacto:

- `SHA256SUMS` — checksums sha256 de todos los artefactos
- `SHA256SUMS.asc` — firma GPG separada del manifest
- `<artefacto>.asc` — firma GPG separada de cada artefacto
- `public.key` — la clave pública de firma de releases

```bash
# Descarga todos los assets de la release a un directorio (o desde la página de la release)
cd /tmp/release-check
gh release download v0.1.3 --repo FROSADO/onecloudriver

# 1. Importa la clave pública de firma
#    (solo una vez por máquina)
gpg --import public.key

# 2. Verifica la firma del manifest de checksums
#    → "Good signature from ..."
gpg --verify SHA256SUMS.asc SHA256SUMS

# 3. Verifica la integridad de cada artefacto
#    → "...: OK" para cada archivo
sha256sum -c SHA256SUMS

# 4. (Opcional) Verifica la firma separada de cada artefacto
gpg --verify onecloudriver_linux_amd64.zip.asc onecloudriver_linux_amd64.zip
```

> [!TIP]
> Si la firma muestra `Good signature` pero avisa de que la clave no está certificada, es normal hasta que [confíes en la clave](https://www.gnupg.org/gph/en/manual/x334.html).

> [!NOTE]
> `Good signature` prueba que el archivo fue firmado con esa clave, no que la clave pertenezca al proyecto. Confirma el fingerprint de la clave pública por un canal de confianza (p. ej. el anuncio de la release) antes de confiar en ella.

📖 Cómo se gestiona la clave de firma (configuración, rotación, recuperación): [docs/RELEASE_SIGNING.es.md](docs/RELEASE_SIGNING.es.md).

## 🔐 Autenticación

```bash
# Añadir una cuenta Microsoft
onecloudriver account add

# Se abrirá el navegador en http://localhost:9090/callback
# Autoriza la aplicación y vuelve a la terminal
```

## 💾 Montar OneDrive

```bash
# Montaje básico
onecloudriver mount ~/OneDrive -a usuario@outlook.com

# Con configuración de caché personalizada
onecloudriver mount ~/OneDrive -a usuario@outlook.com \
    --cache-dir ~/.cache/onecloudriver/custom \
    --cache-ttl 120s \
    --cache-max-entries 5000 \
    --cache-max-size 2GB

# Desmontar (Ctrl+C en la terminal del mount, o)
fusermount3 -u ~/OneDrive
```

## 🖥️ Uso del CLI sin montar

```bash
# Listar archivos
onecloudriver list -a usuario@outlook.com /Documentos

# Descargar
onecloudriver download -a usuario@outlook.com /foto.jpg -d ~/Descargas

# Subir
onecloudriver upload -a usuario@outlook.com -f ~/documento.pdf --dest-path /Backup

# Crear carpeta
onecloudriver mkdir -a usuario@outlook.com -n "Nueva Carpeta" --dest-path /Documentos

# Info detallada
onecloudriver info -a usuario@outlook.com /archivo.txt -o json
```

## 🔄 Servicio systemd (automontaje)

```bash
# Instalar el servicio (auto-detecta la cuenta si solo hay una; si falta, el
# directorio del mountpoint se crea automáticamente durante la instalación)
onecloudriver service install --mountpoint /home/usuario/OneDrive/%i

# Instalar y activar para una cuenta específica
onecloudriver service install --mountpoint /home/usuario/OneDrive/%i -a usuario@outlook.com --enable

# Instalar para TODAS las cuentas
onecloudriver service install --mountpoint /home/usuario/OneDrive/%i --all --enable

# Gestionar
onecloudriver service status              # Ver estado
onecloudriver service start usuario@outlook.com   # Iniciar
onecloudriver service stop usuario@outlook.com    # Parar (desmonta limpio)
onecloudriver service stop --all                  # Parar todas

# Desinstalar
onecloudriver service uninstall --all
```

> 🛠️ **Solución de problemas `203/EXEC`** — Si una instancia no arranca con
> `203/EXEC` (`systemctl --user status onecloudriver@<cuenta>`), la ruta del
> binario en `ExecStart` está obsoleta (p. ej. se generó desde un binario
> temporal de `go test`). Reinstala el servicio con el binario instalado:
> `onecloudriver service uninstall --all` y después
> `/usr/local/bin/onecloudriver service install -a usuario@outlook.com`.
> Consulta [el manual](docs/MANUAL.es.md#solución-de-problemas-203exec).

## 🛠️ Desarrollo

```bash
# Build
make build

# Tests unitarios (mock, sin FUSE)
make test-unit

# Tests de integración (requiere FUSE)
make test-integration

# Todos los tests
make test-all

# Lint
make lint

# Auditoría de seguridad
make security-audit

# Cobertura
make coverage

# Empaquetado
make dist          # Zip con binario + manual
make deb           # Paquete .deb
make rpm           # Paquete .rpm (requiere rpm-build)

# Release
make release-check # Checklist pre-release (solo lectura)
make release       # Automatización interactiva de la release

# CI completo (como en GitHub Actions)
make clean && make build && make test-unit-short && make test-integration-short
```

## 🚀 Releases

Todo el proceso de release está automatizado con [`scripts/release.sh`](scripts/release.sh):
ejecuta un checklist interactivo, actualiza el CHANGELOG y publica la release a través
del [workflow Release](.github/workflows/release.yml) existente.

```bash
# 1. Checklist pre-release — todo lo que debes revisar antes de publicar (solo lectura)
make release-check

# 2. Flujo completo interactivo
make release
```

Qué hace el script:

1. **Checklist pre-release** — autenticación de `gh`, repositorio, rama por defecto,
   estado del working tree, commits sin push, PRs abiertas, CI reciente y último tag.
2. **Merge de PRs (opcional)** — lista las PRs abiertas hacia la rama por defecto y
   ofrece mergearlas con squash (`gh pr merge --squash`) antes de publicar, cambiando
   después a la rama por defecto para publicar desde ahí.
3. **Selección de versión** — propone la siguiente versión patch (p.ej. `0.1.0` → `0.1.1`).
4. **Borrador del CHANGELOG** — genera una sección borrador a partir de los commits
   desde el último tag y la abre en tu `$EDITOR` para revisarla.
5. **Actualización de referencias de versión (opcional)** — refresca los badges de versión y las
   URLs de descarga de `README.md` y `README.es.md`, además de cualquier referencia de versión
   en la documentación (`docs/MANUAL*.md`, man pages).
6. **Commit + tag + push** — commitea los cambios, crea el tag anotado `vX.Y.Z` y hace push.
   El [workflow Release](.github/workflows/release.yml) compila entonces los artefactos
   (zip, `.deb`, `.rpm`) y crea la GitHub Release.
7. **Monitorización** — vigila el run del workflow y verifica la release publicada.

> **Requisitos:** [GitHub CLI](https://cli.github.com/) autenticada (`gh auth login`).
> El script es interactivo; usa `bash scripts/release.sh --yes` para auto-confirmar los
> prompts (el editor sigue abriéndose) o `--check` para el checklist de solo lectura.

## 🏗️ Arquitectura

```
cmd/onecloudriver/     CLI (cobra): account, mount, list, download, upload, service...
    │
internal/
    ├── auth/          OAuth2 + keyring + manager de cuentas
    ├── graph/         Cliente HTTP de Microsoft Graph + retry/backoff
    └── fs/            Sistema de archivos FUSE
         ├── root.go              OneCloudFS: raíz del filesystem
         ├── drive_item_node.go  Nodo FUSE por archivo/carpeta
         ├── fs_ops.go           Operaciones: Mkdir, Create, Rename, Delete...
         ├── cache.go            InodeCache: metadatos con evicción TTL+LFU + BoltDB
         ├── content_cache.go    ContentCache: contenido en disco con evicción
         ├── delta.go            DeltaSync: polling /delta de Graph
         ├── upload_manager.go   Cola asíncrona de subidas con reintentos
         └── mount.go            Mount: health check + ciclo de vida
```

## 📄 Licencia

GPLv3 — ver [LICENSE](LICENSE) para más detalles.

## 🙏 Reconocimientos

Este proyecto no existiría sin el trabajo pionero de **[onedriver](https://github.com/jstaf/onedriver)**
por [@jstaf](https://github.com/jstaf) y contribuidores. Gran parte de la arquitectura FUSE y
el cliente Graph están basados en su diseño original.
