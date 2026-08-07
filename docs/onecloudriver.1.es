.\" Man page for OneCloudRiver
.\" Generated for version 0.1.1
.TH ONECLOUDRIVER 1 "2026-08-04" "OneCloudRiver 0.1.1" "Manual de Usuario"
.SH NOMBRE
onecloudriver \- Sistema de archivos nativo para OneDrive en Linux
.SH SINOPSIS
.B onecloudriver
\fI\,comando\/\fR [\fI\,opciones\/\fR]
.br
.B onecloudriver mount
\fI\,punto_de_montaje\/\fR \fB\-a\fR \fI\,cuenta\/\fR [\fI\,flags\/\fR]
.br
.B onecloudriver account
\fI\,subcomando\/\fR [\fI\,opciones\/\fR]
.SH DESCRIPCIÓN
OneCloudRiver monta tu OneDrive como un sistema de archivos FUSE en Linux,
permitiendo leer, escribir, crear y borrar archivos directamente desde el
explorador de archivos y la terminal.
.P
También proporciona comandos CLI para operar sobre OneDrive sin necesidad de
montar (listar, subir, descargar, crear carpetas, copiar, mover, renombrar,
eliminar).
.SH COMANDOS
.TP
.B mount \fIpunto_de_montaje\fR
Monta OneDrive en el directorio especificado como sistema de archivos FUSE.
Requiere \fB\-a\fR \fIcuenta\fR.
.TP
.B account add
Añade una nueva cuenta de Microsoft mediante OAuth2.
.TP
.B account list
Lista las cuentas configuradas.
.TP
.B account remove \fIcuenta\fR
Elimina una cuenta. Usa \fB\-\-purge\fR para borrar también la caché sin
preguntar, o \fB\-\-keep\fR para conservarla.
.TP
.B list \fI[ruta]\fR
Lista archivos en la raíz o en la ruta especificada. Requiere \fB\-a\fR \fIcuenta\fR.
.TP
.B upload \fIarchivo\fR \fI[ruta_remota]\fR
Sube un archivo local a OneDrive. Requiere \fB\-a\fR \fIcuenta\fR.
.TP
.B download \fIruta_remota\fR
Descarga un archivo de OneDrive al disco local. Requiere \fB\-a\fR \fIcuenta\fR.
.TP
.B mkdir \fIruta\fR
Crea una carpeta en OneDrive. Requiere \fB\-a\fR \fIcuenta\fR.
.TP
.B copy \fIorigen\fR \fIdestino\fR
Copia un archivo o carpeta. Requiere \fB\-a\fR \fIcuenta\fR.
.TP
.B mv \fIorigen\fR \fIdestino\fR
Mueve un archivo o carpeta. Requiere \fB\-a\fR \fIcuenta\fR.
.TP
.B rename \fIruta\fR \fInuevo_nombre\fR
Renombra un archivo o carpeta. Requiere \fB\-a\fR \fIcuenta\fR.
.TP
.B rm \fIruta\fR
Elimina un archivo o carpeta. Requiere \fB\-a\fR \fIcuenta\fR.
.TP
.B info \fIruta\fR
Muestra información detallada (ID, tamaño, fechas). Requiere \fB\-a\fR \fIcuenta\fR.
.TP
.B completion \fIshell\fR
Genera el script de autocompletado para el shell especificado (bash, zsh, fish).
.SH FLAGS DE MOUNT
.TP
\fB\-a\fR, \fB\-\-account\fR \fIcuenta\fR
Cuenta de Microsoft a usar (obligatorio).
.TP
\fB\-\-cache\-dir\fR \fIdirectorio\fR
Directorio raíz para la caché. Default: \fI~/.cache/onecloudriver/<cuenta>\fR.
.TP
\fB\-\-cache\-ttl\fR \fIduración\fR
TTL de metadatos. Ej: \fI60s\fR, \fI5m\fR. Default: \fI60s\fR.
.TP
\fB\-\-cache\-max\-entries\fR \fIn\fR
Máximo de carpetas con hijos cacheados. Default: \fI2000\fR.
.TP
\fB\-\-cache\-max\-size\fR \fItamaño\fR
Tamaño máximo de caché de contenido. Ej: \fI1GB\fR, \fI500MB\fR. Default: \fI0\fR (sin límite).
.SH FLAGS DE ACCOUNT REMOVE
.TP
\fB\-\-purge\fR
Borra la caché local sin preguntar.
.TP
\fB\-\-keep\fR
Conserva la caché local sin preguntar.
.P
\fB\-\-purge\fR y \fB\-\-keep\fR son mutuamente excluyentes. Si no se
especifica ninguno, se pregunta interactivamente.
.SH EJEMPLOS
.P
Montar OneDrive:
.RS
.B onecloudriver mount ~/OneDrive -a usuario@outlook.com
.RE
.P
Montar con caché personalizada:
.RS
.B onecloudriver mount ~/OneDrive -a usuario@outlook.com \-\-cache\-ttl 5m \-\-cache\-max\-size 2GB
.RE
.P
Añadir cuenta:
.RS
.B onecloudriver account add
.RE
.P
Eliminar cuenta con limpieza:
.RS
.B onecloudriver account remove usuario@outlook.com \-\-purge
.RE
.P
Listar archivos sin montar:
.RS
.B onecloudriver list -a usuario@outlook.com
.RE
.SH MODO OFFLINE
Si no hay conexión a Internet al montar, OneCloudRiver inicia en modo offline
(solo lectura de los archivos previamente cacheados).
.SH SEÑALES
.TP
.B SIGINT, SIGTERM, SIGHUP
Desmontan limpiamente el sistema de archivos (Ctrl+C, kill, cierre de terminal).
.SH ARCHIVOS
.TP
.I ~/.config/onecloudriver/
Configuración de cuentas (JSON).
.TP
.I ~/.cache/onecloudriver/<cuenta>/
Caché de metadatos (BoltDB) y contenido (archivos en disco).
.SH REQUISITOS
FUSE 3 (paquete \fIfuse3\fR). El usuario debe pertenecer al grupo \fIfuse\fR.
.SH SOLUCIÓN DE PROBLEMAS
.SS "Device or resource busy"
Cierra el explorador de archivos y terminales dentro del mountpoint.
.RS
.B fusermount3 -u /ruta/al/mountpoint
.RE
.SS "Transport endpoint is not connected"
El proceso terminó inesperadamente.
.RS
.B fusermount3 -uz /ruta/al/mountpoint
.RE
.SS "Error de token"
Reautentica la cuenta:
.RS
.B onecloudriver account remove usuario@outlook.com
.br
.B onecloudriver account add
.RE
.SH VER TAMBIÉN
.BR fusermount3 (1),
.BR mount.fuse3 (8)
.P
Repositorio: \fIhttps://github.com/frosado/onecloudriver\fR
