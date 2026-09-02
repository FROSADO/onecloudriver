.\" Man page for OneCloudRiver
.\" Generated for version 0.1.4
.TH ONECLOUDRIVER 1 "2026-08-04" "OneCloudRiver 0.1.4" "Manual de Usuario"
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
eliminar, sync).
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
.B list [\fB\-\-id\fR \fIid\fR | \fB\-\-path\fR \fIruta\fR]
Lista archivos en la raíz o en la carpeta indicada por \fB\-\-id\fR/\fB\-\-path\fR.
Requiere \fB\-a\fR \fIcuenta\fR.
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
.B sync
Fuerza una sincronización delta inmediata: aplica los cambios remotos a la
caché persistida de la cuenta en ese momento y muestra cuántos se han aplicado,
sin esperar al intervalo del poll en segundo plano del mount. Requiere \fB\-a\fR
\fIcuenta\fR. Funciona sin montar; un mount en ejecución mantiene el bloqueo de
la caché, así que detén el mount o el servicio primero.
.TP
.B completion \fIshell\fR
Genera el script de autocompletado para el shell especificado (bash, zsh, fish).
.TP
.B service \fIsubcomando\fR
Gestiona el servicio systemd de usuario para el auto-montaje al iniciar sesión
(install, uninstall, status, start, stop). Consulta la sección COMANDO SERVICE más abajo.
.SH COMANDO SERVICE
El comando
.B service
gestiona un servicio systemd de usuario que monta OneDrive automáticamente al
iniciar sesión. Instala una plantilla de unidad
(\fI~/.config/systemd/user/onecloudriver@.service\fR) para que cada cuenta
configurada se monte de forma independiente: una instancia
(\fIonecloudriver@<cuenta>.service\fR) por cuenta.
.P
Subcomandos:
.TP
.B service install [\fB\-\-mountpoint\fR \fIruta\fR] [\fB\-a\fR \fIcuenta\fR] [\fB\-\-enable\fR] [\fB\-\-all\fR]
Crea la plantilla de unidad, recarga systemd y, con
\fB\-\-enable\fR, habilita e inicia la instancia. El directorio del punto de
montaje se crea automáticamente si no existe. Si se omite
\fB\-\-mountpoint\fR, solo se usa el punto de montaje guardado si contiene el marcador de instancia
\fI%i\fR. Una ruta concreta guardada por el comando interactivo de montaje se ignora con un aviso para evitar colisiones entre cuentas en la plantilla compartida. El fallback es \fI~/OneDrive/%i\fR (\fI%i\fR se sustituye por el nombre de la cuenta).
.TP
.B service uninstall [\fB\-\-all\fR]
Detiene y deshabilita todas las instancias y elimina la plantilla de unidad.
.TP
.B service list
Lista todas las unidades de usuario onecloudriver instaladas, incluidas las
deshabilitadas, detenidas y nunca iniciadas. Muestra la cuenta, el estado de
habilitación, el estado activo, el subestado y el mountpoint.
.TP
.B service status [\fIcuenta\fR]
Con una cuenta, muestra un resumen conciso con el estado, PID y mountpoint.
Para unidades fallidas, detenidas o reiniciándose, también muestra las últimas
10 líneas del journal. Sin una cuenta, lista las instancias activas y la ruta de
la unidad instalada. Una unidad fallida consultada correctamente termina con
código 0.
.TP
.B service start \fIcuenta\fR
Inicia la instancia de la cuenta indicada.
.TP
.B service stop [\fIcuenta\fR] [\fB\-\-all\fR]
Detiene la instancia y desmonta su sistema de archivos FUSE.
.P
Flags:
.TP
\fB\-\-mountpoint\fR \fIruta\fR
Ruta base de montaje de la instancia. Usa \fI%i\fR como marcador de la cuenta.
.TP
\fB\-a\fR, \fB\-\-account\fR \fIcuenta\fR
Cuenta para la que instalar el servicio. Si se omite, usa la única cuenta configurada.
.TP
\fB\-\-enable\fR
Habilita e inicia el servicio inmediatamente después de instalarlo.
.TP
\fB\-\-all\fR
Aplica la operación a todas las cuentas configuradas.
.TP
\fB\-o\fR, \fB\-\-output\fR \fIformato\fR
Formato de salida de todos los subcomandos de \fBservice\fR: \fItext\fR (por
defecto), \fIjson\fR o \fIyaml\fR. En los modos estructurados, stdout contiene
exactamente un documento serializado y los diagnósticos van a stderr.
.SH FLAGS GLOBALES
.TP
\fB--log-level\fR \fInivel\fR
Nivel mínimo que se escribe en el archivo de registro JSON. Valores admitidos:
\fItrace\fR, \fIdebug\fR, \fIinfo\fR, \fIwarn\fR y \fIerror\fR. Por defecto: \fIinfo\fR.
.TP
\fB--log-json\fR
Emite registros JSON estructurados a stderr en lugar del archivo en disco
(compatible con systemd/journal).
.TP
\fB--lang\fR \fIidioma\fR
Sobrescribe el idioma de la salida (p. ej. \fIes\fR, \fIen\fR). Por defecto se
detecta automáticamente del locale del sistema (\fILC_ALL\fR,
\fILC_MESSAGES\fR, \fILANG\fR), con inglés como respaldo. Soportados: \fIen\fR,
\fIes\fR.
.TP
\fB-v\fR, \fB--version\fR
Muestra la versión del binario (p. ej. \fI0.1.4\fR) y sale.
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
.TP
\fB--debug\fR
Inicia un servidor de depuración local expvar + pprof en \fI127.0.0.1:6060\fR
(loopback) y sube el nivel de registro a debug.
.TP
\fB--debug-addr\fR \fIdirección\fR
Dirección para el servidor \fB--debug\fR. Default: \fI127.0.0.1:6060\fR. Solo
vincula otra interfaz (p. ej. \fI0.0.0.0:6060\fR) en una red de confianza.
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
.B onecloudriver list --path /Documentos -a usuario@outlook.com
.RE
.P
Forzar una sincronización inmediata sin montar:
.RS
.B onecloudriver sync -a usuario@outlook.com
.RE
.P
Instalar y habilitar el servicio:
.RS
.B onecloudriver service install -a usuario@outlook.com \-\-enable
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
.TP
.I ~/.config/systemd/user/onecloudriver@.service
Plantilla de unidad systemd de usuario instalada por \fBservice install\fR.
.SH REQUISITOS
FUSE 3 (paquete \fIfuse3\fR). El usuario debe pertenecer al grupo \fIfuse\fR.
.SH OBSERVABILIDAD
.P
El registro en disco \fI~/.config/onecloudriver/onecloudriver.log\fR se rota por
tamaño (10 MB por archivo, manteniendo como máximo 5 copias gzip durante 30
días) y filtra a Info+ por defecto, de modo que no crece sin límite.
.P
Monta con \fB--debug\fR para inspeccionar su estado interno por HTTP en la
interfaz loopback:
.RS
.B onecloudriver mount ~/OneDrive -a usuario@outlook.com --debug
.br
.B curl 127.0.0.1:6060/debug/vars
.br
.B curl 127.0.0.1:6060/debug/pprof/
.RE
.P
/debug/vars expone \fIcache_hits\fR, \fIcache_misses\fR,
\fIcache_evictions\fR, \fIinode_count\fR,\n\fIcontent_cache_total_size\fR, \fIuploads_in_flight\fR,
\fIuploads_completed\fR, \fIuploads_failed\fR, \fIdelta_sync_count\fR y
\fIdelta_error_count\fR.
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
