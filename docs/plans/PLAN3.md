Sí, **es posible**, pero no como una “configuración” normal de Nautilus. La forma práctica de hacerlo es mediante una **extensión/plugin de Nautilus** que decida, para cada archivo/carpeta dentro del montaje de `oneCloudDriver`, qué **emblem** mostrar.

Lo más realista en GNOME/Nautilus es:

- Mostrar **emblems** pequeños sobre el icono: una nube, una nube verde, una nube blanca, etc.
- No es tan sencillo reemplazar completamente el icono base de cada archivo/carpeta dinámicamente desde una extensión. Nautilus expone mejor los **emblems** que los iconos principales.
- Para la carpeta raíz del montaje podrías probar a fijar un icono personalizado mediante metadata de GIO/Nautilus, pero es menos fiable y no sirve para estado dinámico.

La arquitectura recomendada sería:

```text
oneCloudDriver (FUSE / daemon)
        |
        |-- expone estado por archivo/carpeta
        |   por ejemplo: cached / online / error
        v
Nautilus extension
        |
        |-- lee ese estado
        |-- añade emblem:
        |     nube verde  -> cached
        |     nube blanca -> online
        v
Nautilus UI
```

---

# 1. Qué mecanismo usar en Nautilus

Nautilus soporta extensiones. Para tu caso interesa principalmente:

## `Nautilus.InfoProvider`

Una extensión que implementa `Nautilus.InfoProvider` puede interceptar la carga de información de cada archivo y añadir:

- Emblems:

```python
file.add_emblem("emblem-oneclouddriver-cached")
```

- Atributos de texto:

```python
file.add_string_attribute("oneclouddriver_state", "cached")
```

Con eso puedes mostrar una nube verde, blanca, etc.

---

# 2. Limitación importante: emblems, no iconos completos

Nautilus permite bastante bien añadir **emblems**, que son pequeños iconos superpuestos.

Por ejemplo:

```text
archivo.pdf   [icono normal + nube verde pequeña]
documento.odt [icono normal + nube blanca pequeña]
```

Pero cambiar el icono principal, por ejemplo convertir:

```text
carpeta normal -> carpeta con forma de nube
```

de forma dinámica no es tan directo. La API pública de extensiones de Nautilus está más orientada a emblems y metadatos.

Por tanto, mi recomendación para MVP:

- Usar **emblems** para estado.
- Opcionalmente intentar poner un icono especial sólo para la carpeta raíz del montaje.

---

# 3. Cómo debería exponer el estado `oneCloudDriver`

Para que Nautilus sepa si un archivo está descargado/cacheado o no, tu fork de `onedriver`/`oneCloudDriver` debe exponer esa información.

La opción más sencilla y elegante es usar **extended attributes**, por ejemplo:

```text
user.oneclouddriver.state = cached
user.oneclouddriver.state = online
user.oneclouddriver.state = error
```

Así podrías consultarlo desde shell:

```bash
getfattr -n user.oneclouddriver.state /ruta/al/archivo
```

O desde Python:

```python
import os

state = os.getxattr("/ruta/al/archivo", "user.oneclouddriver.state")
print(state.decode())
```

Esto encaja muy bien con una extensión de Nautilus.

---

## Alternativas para exponer el estado

### Opción A: xattrs virtuales en FUSE

Es la opción que yo intentaría primero.

Tu filesystem FUSE puede implementar algo como:

```text
Getxattr("user.oneclouddriver.state")
```

y devolver:

```text
cached
```

o:

```text
online
```

sin descargar el archivo.

Muy importante: consultar el estado **no debe provocar una descarga**.

---

### Opción B: socket local / API del daemon

`oneCloudDriver` podría tener un daemon escuchando en un socket Unix, por ejemplo:

```text
/run/user/1000/oneclouddriver.sock
```

y la extensión preguntaría:

```json
{
  "path": "/home/usuario/OneCloudDriver/documento.pdf"
}
```

Respuesta:

```json
{
  "state": "cached"
}
```

Esto es más complejo, pero más flexible.

---

### Opción C: comando CLI

Podrías tener algo como:

```bash
oneclouddriver state --path /home/usuario/OneCloudDriver/documento.pdf
```

Respuesta:

```text
cached
```

Funciona, pero no es ideal para Nautilus porque la extensión podría llamarlo muchísimas veces. Si usas esto, necesitas caché y timeouts muy agresivos.

---

# 4. Ejemplo de extensión de Nautilus en Python

Vamos a hacer una extensión simple que:

- Detecta archivos dentro del montaje de `oneCloudDriver`.
- Lee el xattr:

```text
user.oneclouddriver.state
```

- Añade un emblem:
  - `emblem-oneclouddriver-cached` si está descargado.
  - `emblem-oneclouddriver-online` si no está descargado.

---

## Instalación de dependencias

En Debian/Ubuntu normalmente necesitas algo como:

```bash
sudo apt install python3-gi nautilus
```

Y el paquete de extensiones Python para Nautilus. El nombre puede variar según distribución:

```bash
sudo apt install python3-nautilus
```

o en algunas distros:

```bash
sudo apt install nautilus-python
```

En Arch/Manjaro suele ser:

```bash
sudo pacman -S nautilus-python
```

En Fedora:

```bash
sudo dnf install nautilus-python
```

Si estás usando la versión Flatpak de Files/Nautilus, probablemente no puedas cargar extensiones del sistema fácilmente. Este método funciona mejor con el Nautilus nativo de la distribución.

---

## Crear directorio de extensiones

```bash
mkdir -p ~/.local/share/nautilus-python/extensions
```

---

## Extensión de ejemplo

Crea el archivo:

```bash
nano ~/.local/share/nautilus-python/extensions/oneclouddriver_emblems.py
```

Contenido:

```python
import os
import time
from urllib.parse import urlparse, unquote

from gi.repository import GObject, Nautilus


# Carpeta donde monta oneCloudDriver.
# Puedes cambiarlo o usar la variable de entorno ONECLOUDDRIVER_MOUNT.
MOUNT = os.path.realpath(
    os.path.expanduser(
        os.environ.get("ONECLOUDDRIVER_MOUNT", "~/OneCloudDriver")
    )
)

# Extended attribute que oneCloudDriver expondrá.
STATE_XATTR = os.environ.get(
    "ONECLOUDDRIVER_STATE_XATTR",
    "user.oneclouddriver.state"
)

# Pequeña caché para no consultar demasiado.
CACHE_TTL = float(os.environ.get("ONECLOUDDRIVER_NAUTILUS_TTL", "2"))

_cache = {}


def _path_from_file(file):
    """
    Obtiene la ruta local del NautilusFileInfo.
    """
    try:
        if file.get_uri_scheme() != "file":
            return None
    except Exception:
        return None

    try:
        location = file.get_location()
        if location:
            path = location.get_path()
            if path:
                return path
    except Exception:
        pass

    try:
        uri = file.get_uri()
        parsed = urlparse(uri)
        return unquote(parsed.path)
    except Exception:
        return None


def _is_under_mount(path):
    """
    Comprueba si la ruta está dentro del montaje de oneCloudDriver.
    """
    if not path:
        return False

    try:
        real = os.path.realpath(path)
    except OSError:
        return False

    return real == MOUNT or real.startswith(MOUNT + os.sep)


def _get_state_from_xattr(path):
    """
    Lee el estado desde el xattr.

    oneCloudDriver debería devolver algo como:
      cached
      online
      error
    """
    try:
        raw = os.getxattr(path, STATE_XATTR)
        state = raw.decode("utf-8", "replace").strip()
        if state:
            return state
    except OSError:
        pass

    return "online"


def _get_state(path):
    """
    Devuelve el estado con una pequeña caché.
    """
    now = time.monotonic()

    cached = _cache.get(path)
    if cached:
        state, ts = cached
        if now - ts < CACHE_TTL:
            return state

    state = _get_state_from_xattr(path)
    _cache[path] = (state, now)
    return state


class OneCloudDriverInfoProvider(GObject.GObject, Nautilus.InfoProvider):
    """
    Extensión de Nautilus para mostrar emblems de oneCloudDriver.
    """

    def update_file_info(self, file):
        path = _path_from_file(file)

        if not path:
            return Nautilus.OperationResult.COMPLETE

        if not _is_under_mount(path):
            return Nautilus.OperationResult.COMPLETE

        state = _get_state(path)

        # Si quieres una nube genérica para todos los elementos:
        # file.add_emblem("emblem-oneclouddriver")

        if state == "cached":
            file.add_emblem("emblem-oneclouddriver-cached")
        elif state == "error":
            # Puedes reemplazarlo por un emblem propio de error.
            file.add_emblem("emblem-important")
        else:
            file.add_emblem("emblem-oneclouddriver-online")

        return Nautilus.OperationResult.COMPLETE
```

---

# 5. Iconos personalizados

Necesitas iconos con estos nombres:

```text
emblem-oneclouddriver-cached
emblem-oneclouddriver-online
emblem-oneclouddriver
```

Por ejemplo, en SVG:

```text
~/.local/share/icons/hicolor/scalable/emblems/emblem-oneclouddriver.svg
~/.local/share/icons/hicolor/scalable/emblems/emblem-oneclouddriver-cached.svg
~/.local/share/icons/hicolor/scalable/emblems/emblem-oneclouddriver-online.svg
```

Crear directorio:

```bash
mkdir -p ~/.local/share/icons/hicolor/scalable/emblems
```

Copiar ahí tus SVG.

Luego actualizar caché de iconos:

```bash
gtk-update-icon-cache ~/.local/share/icons/hicolor || true
```

Y reiniciar Nautilus:

```bash
nautilus -q
```

Si tu sistema usa PNG en lugar de SVG, puedes ponerlos por tamaños, por ejemplo:

```text
~/.local/share/icons/hicolor/16x16/emblems/emblem-oneclouddriver-cached.png
~/.local/share/icons/hicolor/24x24/emblems/emblem-oneclouddriver-cached.png
~/.local/share/icons/hicolor/32x32/emblems/emblem-oneclouddriver-cached.png
~/.local/share/icons/hicolor/48x48/emblems/emblem-oneclouddriver-cached.png
```

---

# 6. Probar primero con emblems existentes

Antes de crear tus propios iconos, puedes probar si la extensión funciona usando emblems ya existentes.

Por ejemplo, cambia esta parte:

```python
if state == "cached":
    file.add_emblem("emblem-oneclouddriver-cached")
else:
    file.add_emblem("emblem-oneclouddriver-online")
```

por algo como:

```python
if state == "cached":
    file.add_emblem("emblem-default")
else:
    file.add_emblem("emblem-important")
```

Reinicia Nautilus:

```bash
nautilus -q
```

Si ves los emblems, entonces la extensión funciona. Después puedes reemplazarlos por tus nubes verdes/blancas.

---

# 7. Probar sin tener aún el backend de oneCloudDriver

Puedes simular el comportamiento usando archivos normales y xattrs.

Crea una carpeta de pruebas:

```bash
mkdir -p ~/oc-test
cd ~/oc-test
echo "hola" > a.txt
echo "mundo" > b.txt
```

Marca un archivo como cacheado:

```bash
setfattr -n user.oneclouddriver.state -v cached a.txt
```

Marca otro como online:

```bash
setfattr -n user.oneclouddriver.state -v online b.txt
```

Comprueba:

```bash
getfattr -n user.oneclouddriver.state a.txt
getfattr -n user.oneclouddriver.state b.txt
```

Lanza Nautilus con la variable de entorno de prueba:

```bash
nautilus -q
ONECLOUDDRIVER_MOUNT=~/oc-test nautilus
```

Deberías ver los emblems si todo está correcto.

Nota: si Nautilus ya estaba abierto, asegúrate de matarlo antes:

```bash
nautilus -q
```

---

# 8. Cómo exponer el xattr desde tu fork en Go

La parte concreta depende de la librería FUSE que use `onedriver`.

La idea es implementar algo equivalente a:

```text
Getxattr(name)
Listxattr()
```

Cuando Nautilus o cualquier proceso pregunte por:

```text
user.oneclouddriver.state
```

tu filesystem debe responder:

```text
cached
```

o:

```text
online
```

sin abrir ni descargar el archivo.

Ejemplo conceptual, no dependiente de una librería concreta:

```go
func (n *Node) Getxattr(ctx context.Context, name string, dest []byte) (uint32, syscall.Errno) {
    if name != "user.oneclouddriver.state" {
        return 0, syscall.ENODATA
    }

    // Aquí consultas el estado interno del driver.
    // No debes descargar el archivo.
    state := n.fs.StateForPath(n.path)

    // state podría ser:
    //   "cached"
    //   "online"
    //   "error"

    value := []byte(state)

    if dest == nil {
        return uint32(len(value)), 0
    }

    if len(dest) < len(value) {
        return 0, syscall.ERANGE
    }

    copy(dest, value)
    return uint32(len(value)), 0
}
```

También conviene implementar `Listxattr`, al menos para devolver:

```text
user.oneclouddriver.state
```

Si usas `hanwen/go-fuse/v2`, normalmente implementarás interfaces como `NodeGetxattrer` y `NodeListxattrer`.

Si usas `bazil.org/fuse`, será algo parecido a manejar peticiones `GetxattrRequest`.

---

# 9. Estados recomendados

Yo usaría estados simples:

```text
online
cached
error
```

Significado:

```text
online  -> el archivo está en OneDrive, no en caché local
cached  -> el archivo está descargado/cacheado localmente
error   -> hay un problema con ese archivo/carpeta
```

Más adelante podrías añadir:

```text
syncing
pinned
excluded
mixed
```

Para carpetas, tienes que decidir una política. Por ejemplo:

```text
cached   -> todos los hijos importantes están cacheados
online   -> ninguno está cacheado
mixed    -> algunos sí, otros no
```

Pero calcular estados recursivos puede ser caro. Mejor que el daemon de `oneCloudDriver` exponga el estado ya calculado para cada carpeta.

---

# 10. Rendimiento: punto crítico

Nautilus puede llamar a `update_file_info` muchas veces.

Por eso, la extensión debe:

- No leer contenido de archivos.
- No descargar archivos.
- No hacer peticiones lentas.
- Usar caché.
- Fallar rápido si el estado no está disponible.

El ejemplo anterior incluye una caché simple con TTL de 2 segundos. Para uso real quizá quieras algo más elaborado.

Si tu backend es un socket/D-Bus, la extensión debería hacer consultas asíncronas y con timeout. Una extensión que bloquea Nautilus puede hacer que el navegador se sienta lento.

---

# 11. Refresco automático del estado

Hay un punto incómodo: si un archivo pasa de `online` a `cached`, Nautilus no siempre se entera inmediatamente.

Opciones:

## Opción simple: TTL corto

La extensión vuelve a consultar el estado al cabo de unos segundos.

Ventaja:

- Fácil.

Desventaja:

- Puede tardar en actualizarse.

---

## Opción intermedia: invalidar información

Nautilus tiene:

```python
file.invalidate_extension_info()
```

Si haces una consulta asíncrona, cuando llega el resultado puedes hacer:

```python
file.invalidate_extension_info()
```

Esto puede forzar una actualización.

---

## Opción avanzada: daemon + señales

El daemon de `oneCloudDriver` podría emitir señales D-Bus cuando un archivo cambia de estado:

```text
FileStateChanged(path, state)
```

La extensión podría escuchar esas señales e invalidar archivos visibles o recientemente consultados.

Esto es más complejo, pero sería la solución más pulida.

---

# 12. Carpeta raíz con icono de nube

Para la carpeta raíz del montaje, por ejemplo:

```text
~/OneCloudDriver
```

puedes intentar fijar un icono personalizado usando metadata de GIO.

Por ejemplo:

```bash
gio set ~/OneCloudDriver metadata::custom-icon-name folder-cloud
```

Pero esto depende de la versión de Nautilus y no siempre funciona igual.

También necesitarás un icono llamado algo así:

```text
folder-cloud
```

instalado en tu tema de iconos.

Para quitarlo:

```bash
gio set ~/OneCloudDriver metadata::custom-icon-name ""
```

O, según versión:

```bash
gio set -t string ~/OneCloudDriver metadata::custom-icon-name ""
```

Importante: esto no es dinámico. Sirve para la carpeta raíz, no para cambiar automáticamente entre nube verde/blanca según estado.

Para estado dinámico, mejor emblems.

---

# 13. Posible diseño visual

Una posibilidad:

## Carpeta raíz

Icono principal:

```text
carpeta nube
```

o emblem:

```text
emblem-oneclouddriver
```

## Archivos/carpetas ya descargados

```text
emblem-oneclouddriver-cached
```

Nube verde.

## Archivos/carpetas solo en la nube

```text
emblem-oneclouddriver-online
```

Nube blanca o gris.

## Archivos con error

```text
emblem-oneclouddriver-error
```

Nube roja o símbolo de advertencia.

---

# 14. Ejemplo de iconos SVG simples

Puedes crear archivos SVG simples para probar.

## Nube verde

`emblem-oneclouddriver-cached.svg`:

```xml
<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 16 16">
  <path
    d="M5 12.5
       A3.2 3.2 0 1 1 5.4 6.1
       A4.3 4.3 0 0 1 13.5 7.8
       A2.7 2.7 0 0 1 12.8 12.5
       Z"
    fill="#2ec27e"
    stroke="#1a7a4f"
    stroke-width="1"
  />
</svg>
```

## Nube blanca

`emblem-oneclouddriver-online.svg`:

```xml
<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 16 16">
  <path
    d="M5 12.5
       A3.2 3.2 0 1 1 5.4 6.1
       A4.3 4.3 0 0 1 13.5 7.8
       A2.7 2.7 0 0 1 12.8 12.5
       Z"
    fill="#ffffff"
    stroke="#777777"
    stroke-width="1"
  />
</svg>
```

Son iconos muy simples, pero sirven para verificar que el mecanismo funciona.

---

# 15. Comprobación de iconos instalados

Puedes comprobar si GTK encuentra tus iconos con algo así:

```bash
python3 - <<'PY'
import gi
gi.require_version("Gtk", "3.0")
from gi.repository import Gtk

theme = Gtk.IconTheme.get_default()

icons = [
    "emblem-oneclouddriver",
    "emblem-oneclouddriver-cached",
    "emblem-oneclouddriver-online",
]

for icon in icons:
    print(icon, theme.has_icon(icon))
PY
```

Si devuelve `False`, revisa la ruta y el nombre del icono.

---

# 16. Depuración de la extensión

Si la extensión no aparece:

## Comprueba sintaxis

```bash
python3 -m py_compile ~/.local/share/nautilus-python/extensions/oneclouddriver_emblems.py
```

## Mata Nautilus y ejecútalo desde terminal

```bash
nautilus -q
nautilus
```

Así verás mensajes de error en la terminal.

## Comprueba que la ruta de extensiones es correcta

```bash
ls -la ~/.local/share/nautilus-python/extensions
```

## Comprueba permisos

```bash
ls -l ~/.local/share/nautilus-python/extensions/oneclouddriver_emblems.py
```

---

# 17. Si Python no está disponible

Algunas distribuciones o versiones de Nautilus pueden tener soporte limitado para extensiones Python.

En ese caso, la alternativa “seria” es escribir una extensión en C usando:

```text
libnautilus-extension
```

El concepto sería el mismo:

- Implementar un `NautilusInfoProvider`.
- En `update_file_info`, añadir emblems.
- Compilar como shared library.
- Instalarla donde Nautilus busque extensiones.

Es más incómodo, pero es el mecanismo nativo.

---

# 18. Otra alternativa más profunda: GVFS

Si quisieras una integración muy profunda, podrías plantear no usar solo FUSE, sino un backend GVFS.

GVFS puede integrarse mejor con GNOME y Nautilus, permitiendo controlar más aspectos de la visualización remota.

Pero es bastante más complejo que un FUSE + extensión de Nautilus.

Para tu caso, yo empezaría por:

```text
FUSE + xattr + extensión Nautilus con emblems
```

---

# 19. Resumen práctico

Sí, puedes hacer algo como esto:

```text
~/OneCloudDriver
├── documentos
│   ├── informe.pdf   [nube verde]
│   ├── video.mp4     [nube blanca]
│   └── notas.md      [nube verde]
```

La forma recomendada:

1. Haz que `oneCloudDriver` exponga:

```text
user.oneclouddriver.state
```

con valores:

```text
cached
online
error
```

2. Instala iconos:

```text
emblem-oneclouddriver-cached
emblem-oneclouddriver-online
```

3. Instala una extensión Python de Nautilus.

4. En `update_file_info()`, comprueba si el archivo está bajo el montaje de `oneCloudDriver`.

5. Añade el emblem correspondiente:

```python
file.add_emblem("emblem-oneclouddriver-cached")
```

o:

```python
file.add_emblem("emblem-oneclouddriver-online")
```

6. Reinicia Nautilus:

```bash
nautilus -q
```

---

# 20. Mi recomendación para tu fork

Para empezar, yo haría este MVP:

## MVP mínimo

- `oneCloudDriver` expone xattr:

```text
user.oneclouddriver.state
```

- Extensión Nautilus en Python.
- Dos emblems:
  - nube verde para `cached`.
  - nube blanca para `online`.
- Cache simple en la extensión.
- Sin actualización automática avanzada.

## Segunda iteración

- Añadir estado `error`.
- Añadir columna en Nautilus:

```text
Estado oneCloudDriver
```

mediante `Nautilus.ColumnProvider`.
- Mejorar caché.
- Añadir iconos bonitos.

## Tercera iteración

- Actualización automática vía D-Bus/socket.
- Estado mixto para carpetas.
- Posible icono especial para la raíz.
- Empaquetado formal del plugin.

En resumen: **sí es posible**, y el camino más razonable es una extensión de Nautilus que use `InfoProvider` para añadir **emblems**, mientras `oneCloudDriver` expone el estado de cada archivo mediante un mecanismo eficiente como xattrs o una API local del daemon.