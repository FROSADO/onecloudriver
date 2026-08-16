# OneCloudRiver — Manual de Uso

Sistema de archivos nativo para OneDrive en Linux.

OneCloudRiver monta tu OneDrive como un sistema de archivos FUSE en Linux,
permitiendo leer, escribir, crear y borrar archivos directamente desde el
explorador de archivos (Nautilus, Dolphin, Thunar, etc.) y la terminal.

---

## Instalación

### Desde el binario (zip)

```bash
unzip onecloudriver_linux_amd64.zip
sudo cp onecloudriver /usr/local/bin/
```

### Desde el paquete .deb

```bash
sudo dpkg -i onecloudriver_*.deb
```


### Desde el paquete .rpm

```bash
sudo dnf install ./onecloudriver*.rpm
```

O con el gestor de paquetes de tu distro (requiere `fuse3`, que se resuelve automáticamente):

```bash
# Fedora / RHEL 8+ / Rocky Linux / AlmaLinux
sudo dnf install ./onecloudriver-0.1.3-1.x86_64.rpm

# RHEL / CentOS 7 (antiguos)
sudo yum install ./onecloudriver-0.1.3-1.x86_64.rpm

# openSUSE
sudo zypper install ./onecloudriver-0.1.3-1.x86_64.rpm
```

El paquete instala el binario en `/usr/local/bin`, la página de manual (`man onecloudriver`),
la plantilla de servicio systemd y la documentación en `/usr/share/doc/onecloudriver/`.

### Requisitos

- **FUSE**: `sudo apt install fuse3` (o `fuse` en distribuciones antiguas)
- **Permisos**: el usuario debe pertenecer al grupo `fuse`
  ```bash
  sudo usermod -aG fuse $USER
  # Cerrar sesión y volver a entrar
  ```

---

## Configuración inicial

### 1. Añadir una cuenta de Microsoft

```bash
onecloudriver account add
```

Esto abre el navegador en `http://localhost:9090/callback`. Inicia sesión con tu
cuenta de Microsoft y autoriza a OneCloudRiver a acceder a tus archivos.

Si el navegador no está disponible, el programa muestra una URL para copiar y
pegar manualmente.

### 2. Listar cuentas configuradas

```bash
onecloudriver account list
```

### 3. Eliminar una cuenta

```bash
# Pregunta si borrar la caché local
onecloudriver account remove usuario@outlook.com

# Borrar cuenta y caché sin preguntar
onecloudriver account remove usuario@outlook.com --purge

# Conservar la caché local
onecloudriver account remove usuario@outlook.com --keep
```

---

## Montar OneDrive

### Montaje básico

```bash
onecloudriver mount /ruta/al/mountpoint -a usuario@outlook.com
```

Ejemplo:

```bash
mkdir ~/OneDrive
onecloudriver mount ~/OneDrive -a paveryutu72@hotmail.com
```

### Flags de configuración de caché

| Flag | Default | Descripción |
|---|---|---|
| `--cache-dir` | `~/.cache/onecloudriver/<cuenta>` | Directorio raíz para la caché — **solo sesión, nunca se guarda en la config de la cuenta** |
| `--cache-ttl` | `60s` | TTL de metadatos (ej: `5m`, `300s`) |
| `--cache-max-entries` | `2000` | Máximo de carpetas con hijos cacheados |
| `--cache-max-size` | `0` (sin límite) | Tamaño máximo de caché de contenido (ej: `1GB`, `500MB`) |

### Desmontaje

Presiona `Ctrl+C` en la terminal donde ejecutaste `mount`. Si el explorador de
archivos está abierto en el mountpoint, el desmontaje usa lazy-unmount y se
completa cuando cierres el explorador.

### Configuración persistida

Al montar con éxito, la configuración se guarda automáticamente en
`~/.config/onecloudriver/<cuenta>.json` y se reutiliza en la siguiente sesión.
Esto incluye el punto de montaje, los parámetros de caché, y las opciones
avanzadas.

**Excepción:** `--cache-dir` es un override solo de sesión y **nunca** se guarda, para que una ruta temporal (p. ej. `/tmp/...`) no pueda sobrescribir el directorio de caché configurado para futuros mounts.

El JSON de cuenta queda así:

```json
{
  "name": "usuario@outlook.com",
  "config": { "...oauth2..." },
  "expires_at": 1785884980,
  "mount": {
    "defaultMountpoint": "/home/usuario/OneDrive",
    "cacheDir": "~/.cache/onecloudriver/usuario@outlook.com",
    "cacheTTL": "60s",
    "cacheMaxEntries": 2000,
    "cacheMaxSize": 0,
    "deltaInterval": "5m",
    "maxUploadsInFlight": 5,
    "maxUploadRetries": 5,
    "httpTimeout": "15s",
    "graphRetries": 3
  }
}
```

Los campos que no aparezcan en el JSON usan sus valores por defecto.

#### Tabla completa de parámetros

| Campo JSON | Flag CLI | Default | Descripción |
|---|---|---|---|
| `defaultMountpoint` | *(argumento posicional)* | `./<cuenta>` | Último punto de montaje usado con éxito |
| `cacheDir` | `--cache-dir` | `~/.cache/onecloudriver/<cuenta>` | Directorio raíz de la caché (un valor de sesión de `--cache-dir` nunca se guarda) |
| `cacheTTL` | `--cache-ttl` | `60s` | TTL base de metadatos cacheados |
| `cacheMaxEntries` | `--cache-max-entries` | `2000` | Máximo de carpetas con hijos cacheados |
| `cacheMaxSize` | `--cache-max-size` | `0` (sin límite) | Tamaño máximo de ContentCache en disco |
| `deltaInterval` | `--delta-interval` | `5m` | Intervalo de polling del endpoint `/delta` |
| `maxUploadsInFlight` | `--max-uploads` | `5` | Subidas simultáneas máximas |
| `maxUploadRetries` | `--upload-retries` | `5` | Reintentos antes de abandonar una subida |
| `httpTimeout` | `--http-timeout` | `15s` | Timeout de peticiones HTTP a Graph |
| `graphRetries` | `--graph-retries` | `3` | Reintentos HTTP en errores 429/503 |

Los parámetros avanzados (`deltaInterval`, `maxUploadsInFlight`, etc.) no se
escriben en el JSON a menos que el usuario los configure explícitamente con los
flags CLI. Si no están en el JSON, se usan los defaults.

#### Ejemplo: personalizar y persistir

```bash
# Primer montaje: configura todo
onecloudriver mount ~/OneDrive -a user@outlook.com \
    --cache-ttl 120s \
    --cache-max-size 2GB \
    --delta-interval 10m \
    --max-uploads 3

# Siguientes montajes: hereda la configuración automáticamente
onecloudriver mount
# → Usando punto de montaje guardado: /home/usuario/OneDrive
# → (cache-ttl=120s, cache-max-size=2GB, delta-interval=10m, max-uploads=3)
```

---

## Servicio systemd (automontaje)

OneCloudRiver puede instalarse como un servicio systemd de usuario para
montar automáticamente OneDrive al iniciar sesión.

### Instalar el servicio

El mountpoint se determina automáticamente si la cuenta tiene un
`defaultMountpoint` guardado. Si no, usa `~/OneDrive/%i` expandido a la ruta
absoluta del home (p. ej. `/home/usuario/OneDrive/%i`) como fallback.
Puedes sobrescribirlo con `--mountpoint`:

```bash
# Usa el defaultMountpoint del JSON de cuenta (si existe)
onecloudriver service install

# O especifica uno explícito (un ~/ inicial se expande a la ruta del home)
onecloudriver service install --mountpoint /home/usuario/OneDrive/%i
```

Si solo hay **una cuenta** configurada, se usa automáticamente. Con múltiples
cuentas, especifica `--account`:

```bash
onecloudriver service install --mountpoint /home/usuario/OneDrive/%i -a usuario@outlook.com
```

Esto crea la plantilla `~/.config/systemd/user/onecloudriver@.service` y
recarga systemd. Si el directorio del mountpoint no existe, se crea
automáticamente durante la instalación (el CLI lo informa).

### Activar y arrancar en un solo paso

Con `--enable`, el servicio se activa e inicia inmediatamente:

```bash
onecloudriver service install --mountpoint /home/usuario/OneDrive/%i --enable
```

OneDrive se monta en `/home/usuario/OneDrive/usuario@outlook.com` y se
rearranca automáticamente al iniciar sesión.

### Instalar para todas las cuentas

Con `--all`, se instala el servicio para **todas** las cuentas configuradas:

```bash
onecloudriver service install --mountpoint /home/usuario/OneDrive/%i --all --enable
```

### Gestionar el servicio

```bash
# Ver estado de todas las cuentas
onecloudriver service status

# Ver estado de una cuenta específica
onecloudriver service status usuario@outlook.com

# Iniciar manualmente
onecloudriver service start usuario@outlook.com

# Detener y desmontar (ejecuta fusermount3 -uz + systemctl stop)
onecloudriver service stop usuario@outlook.com

# Ver logs
journalctl --user -u onecloudriver@usuario@outlook.com -f
```

### Desinstalar el servicio

```bash
# Desinstalar para todas las cuentas (desmonta + stop + disable + borra archivo)
onecloudriver service uninstall --all

# O desinstalar solo las instancias activas actuales
onecloudriver service uninstall
```

### Mountpoint personalizado

```bash
onecloudriver service install --mountpoint /mnt/onedrive/%i
```

### Servicio systemd generado

```ini
[Unit]
Description=OneCloudRiver - OneDrive filesystem for %i
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/onecloudriver mount /home/usuario/OneDrive/%i -a %i
ExecStop=/bin/fusermount3 -uz /home/usuario/OneDrive/%i
Restart=on-failure
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
```

#### Cómo se resuelve la ruta del binario en `ExecStart`

La ruta del binario en `ExecStart` **no** se copia tal cual de cómo invocaste
el comando. `onecloudriver service install` la resuelve así:

1. Si el binario se invocó con una ruta explícita (absoluta o relativa, p. ej.
   `/usr/local/bin/onecloudriver` o `./onecloudriver`), esa ruta se resuelve a
   una ruta absoluta y debe existir y ser ejecutable.
2. En caso contrario (invocado por nombre simple, p. ej. `onecloudriver` desde
   tu `PATH`), se busca con `exec.LookPath`.
3. La ruta de un binario de `go test` (un fichero `*.test` bajo un directorio
   temporal `go-build`) nunca se acepta; en su lugar se usa el nombre canónico
   `onecloudriver`.

Si no se puede resolver ningún binario válido, `service install` falla con un
error y **no escribe la unit**, en lugar de escribir silenciosamente un
`ExecStart` roto. Para el resultado más predecible, ejecuta el comando a través
del binario instalado, p. ej. `/usr/local/bin/onecloudriver service install`.

> ⚠️ **No uses `go run` para `service install`.** `go run` compila en un
> directorio temporal `/tmp/go-build*` y borra el binario al terminar el
> proceso, así que la unit referenciaría una ruta que ya no existe y el
> servicio fallaría con `203/EXEC`. Usa `go build` (o `make build`) y ejecuta
> el binario resultante con una ruta explícita.

#### Solución de problemas: `203/EXEC`

Si una instancia del servicio no arranca con `203/EXEC` en
`systemctl --user status onecloudriver@<cuenta>`, la ruta del binario en
`ExecStart` no existe (p. ej. se generó desde un binario temporal de `go test`
o una ruta obsoleta). Arréglalo reinstalando el servicio con el binario real:

```bash
/usr/local/bin/onecloudriver service uninstall --all
/usr/local/bin/onecloudriver service install -a usuario@outlook.com
systemctl --user daemon-reload
```

### La unit de servicio empaquetada (deb/rpm)

Instalar el paquete `.deb` o `.rpm` también incluye una plantilla de servicio
systemd **de usuario** en `/usr/lib/systemd/user/onecloudriver@.service`:

```ini
[Unit]
Description=OneCloudRiver - OneDrive Filesystem
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/onecloudriver mount %h/OneDrive/%i -a %i
ExecStop=/bin/fusermount3 -uz %h/OneDrive/%i
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
```

Es una **unit plantilla** (`onecloudriver@.service`): el placeholder `%i` se
sustituye por el nombre de la instancia (la cuenta) al activarla. Por ejemplo,
para automontar la cuenta `usuario@outlook.com` tras instalar el paquete:

```bash
systemctl --user daemon-reload
systemctl --user enable --now 'onecloudriver@usuario@outlook.com.service'
```

- `%i` → el nombre de la cuenta (p. ej. `usuario@outlook.com`)
- `%h` → tu directorio home (p. ej. `/home/usuario`), así que el mountpoint
  por defecto es `$HOME/OneDrive/<cuenta>`

> ⚠️ Solo un `%i`/`%h` **simple** es un specifier. Un `%%` literal en una unit
> es un signo de porcentaje escapado, así que `%%i` se pasaría al comando como
> la cadena literal `%i` y la instancia nunca se expandiría.

Ten en cuenta que `onecloudriver service install` escribe una unit **de nivel
de usuario** en `~/.config/systemd/user/onecloudriver@.service`, que **tiene
precedencia** sobre la empaquetada para tu usuario. Usa `service install` (o
`service uninstall`) cuando quieras la unit generada y consciente de la
cuenta; la unit empaquetada es un fallback que funciona de serie para
cualquier cuenta sin configuración adicional.

---

## Operaciones con archivos

Una vez montado, puedes usar cualquier herramienta estándar de Linux:

```bash
# Listar archivos
ls ~/OneDrive

# Crear carpeta
mkdir ~/OneDrive/NuevaCarpeta

# Crear archivo
echo "Hola mundo" > ~/OneDrive/documento.txt

# Copiar archivos locales a OneDrive
cp foto.jpg ~/OneDrive/Fotos/

# Mover/renombrar
mv ~/OneDrive/documento.txt ~/OneDrive/renombrado.txt

# Borrar
rm ~/OneDrive/archivo_viejo.txt

# Cambiar permisos
chmod 600 ~/OneDrive/secreto.txt

# Actualizar timestamp
touch ~/OneDrive/documento.txt
```

También funciona desde Nautilus, Dolphin, Thunar o cualquier explorador de
archivos que soporte FUSE.

---

## Operaciones CLI (sin montar)

Puedes usar OneCloudRiver directamente desde la línea de comandos sin necesidad
de montar:

### Listar archivos

```bash
onecloudriver list -a usuario@outlook.com
onecloudriver list "/Documentos" -a usuario@outlook.com
```

### Subir archivo

```bash
onecloudriver upload archivo_local.txt "/Documentos/" -a usuario@outlook.com
```

### Descargar archivo

```bash
onecloudriver download "/Documentos/archivo.txt" -a usuario@outlook.com
```

### Crear carpeta

```bash
onecloudriver mkdir "/NuevaCarpeta" -a usuario@outlook.com
```

### Copiar

```bash
onecloudriver copy "/origen.txt" "/destino.txt" -a usuario@outlook.com
```

### Mover

```bash
onecloudriver mv "/origen.txt" "/Documentos/" -a usuario@outlook.com
```

### Renombrar

```bash
onecloudriver rename "/viejo.txt" "nuevo.txt" -a usuario@outlook.com
```

### Eliminar

```bash
onecloudriver rm "/archivo.txt" -a usuario@outlook.com
```

### Información

```bash
onecloudriver info "/Documentos/archivo.txt" -a usuario@outlook.com
```

---

## Modo offline

Si no hay conexión a Internet al montar, OneCloudRiver inicia en modo offline:
solo lectura de los archivos previamente cacheados.

```bash
# Al montar sin red:
⚠️  Sin conexión a Internet. Iniciando en modo offline (solo lectura de caché).
```

Los archivos que ya estaban en la caché se pueden leer. Las operaciones de
escritura no están disponibles en modo offline.

---

## Health check

Al montar, OneCloudRiver verifica que el token de autenticación sea válido
llamando a Microsoft Graph. Si el token expiró o fue revocado:

```
Error: falló la verificación del token contra Microsoft Graph

📋 Diagnóstico: el token de acceso para 'usuario@outlook.com' no es válido.
   Causas comunes:
   • La sesión expiró y el refresh token fue revocado
   • La cuenta fue eliminada o la contraseña cambiada
   • La aplicación fue revocada en el portal de Azure

   Solución: vuelve a autenticarte con:
     onecloudriver account remove usuario@outlook.com
     onecloudriver account add
```

---

## Estructura de la caché

```
~/.cache/onecloudriver/
└── usuario@outlook.com/
    ├── inodes.db          # Metadatos (BoltDB)
    └── content/           # Archivos cacheados en disco
        ├── <id_item_1>
        ├── <id_item_2>
        └── ...
```

---

## Auditoría de seguridad

```bash
make security-audit
cat audit-report.txt
```

Herramientas ejecutadas: gosec, govulncheck, golangci-lint, go test -race.

---

## Solución de problemas

### "Device or resource busy" al desmontar

Cierra el explorador de archivos y cualquier terminal con `cd` dentro del
mountpoint. Luego:

```bash
fusermount3 -u /ruta/al/mountpoint
```

### "Transport endpoint is not connected"

El proceso de OneCloudRiver terminó inesperadamente. Desmonta y vuelve a montar:

```bash
fusermount3 -uz /ruta/al/mountpoint
onecloudriver mount /ruta/al/mountpoint -a usuario@outlook.com
```

### El token expiró

```bash
onecloudriver account remove usuario@outlook.com
onecloudriver account add
```

### Permiso denegado (FUSE)

```bash
sudo usermod -aG fuse $USER
# Cerrar sesión y volver a entrar
```

---

## Licencia

OneCloudRiver es software libre.
