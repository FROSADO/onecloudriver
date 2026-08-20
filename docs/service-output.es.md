# Salida estructurada del comando `service` (`--output text|json|yaml`)

> Referencia de la salida legible por máquinas de todos los subcomandos de
> `onecloudriver service`. Implementada para la issue #43. Fuente de verdad:
> `cmd/onecloudriver/service.go`, `cmd/onecloudriver/output.go` e
> `internal/service/results.go`.

## Resumen

Todos los subcomandos de `service` aceptan un flag **persistente** `--output`/`-o`:

```bash
onecloudriver service <install|uninstall|list|status|start|stop> -o text|json|yaml
```

- `text` (por defecto) — salida humana sin cambios, con símbolos de `internal/printer`.
- `json` / `yaml` — **exactamente un** documento serializado en stdout, sin
  símbolos del printer, sin texto de progreso, sin salida cruda de `systemctl`.

El flag se registra una sola vez en el `serviceCmd` padre
(`serviceCmd.PersistentFlags().StringP("output", "o", "text", ...)`) y lo
heredan todos los subcomandos. Se valida **antes** de cualquier efecto
secundario en systemd o en las cuentas.

## Contrato de salida

| Flujo | `text` | `json` / `yaml` |
|---|---|---|
| stdout | salida humana (tablas, símbolos) | exactamente un documento serializado |
| stderr | (no usado en flujo normal) | diagnósticos, avisos, progreso ("Using saved mountpoint", avisos del journal) |

Semántica de código de salida:

- Un servicio **fallido/detenido consultado correctamente** es un dato, no un
  error → código `0` (el documento lleva `state: failed` / `state: stopped`).
- Un fallo real de systemd/consulta (unidad inexistente, sin bus, salida
  malformada) → no-cero, diagnóstico en stderr, sin documento parcial.
- Una acción (`install`/`uninstall`/`start`/`stop`) que falla tras un contexto
  de acción válido → un `ActionResult` con `ok: false` + `error`, y salida
  no-cero. El documento se emite igualmente para que los scripts lo inspeccionen.

Los formatos desconocidos se rechazan antes de los efectos secundarios:

```bash
onecloudriver service list -o xml
# Error: unsupported format: "xml" (valid: text, json, yaml)
```

## Formas de resultado estructurado

| Invocación | Resultado estructurado |
|---|---|
| `service list` | `[]InstanceInfo` (vacío → `[]`) |
| `service status` (sin cuenta) | `[]InstanceInfo` (misma fuente que `list`) |
| `service status CUENTA` | `UnitStatus` (un objeto, incl. `journal_tail` cuando aplica) |
| `install` / `uninstall` / `start` / `stop` | `ActionResult` |
| `... --all` | un `ActionResult` con `affected_accounts` ordenado |

## Modelos de datos

Todos los modelos llevan tags JSON/YAML estables (snake_case). `omitempty` se
usa solo cuando el campo realmente no aplica a la operación.

### `InstanceInfo` (internal/service/instances.go)

Una unidad instanciada instalada, incluidas deshabilitadas/detenidas/nunca
iniciadas.

| Campo | Significado |
|---|---|
| `unit` | nombre de la unidad, p. ej. `onecloudriver@a@example.com.service` |
| `account` | nombre de la cuenta (instancia) |
| `enabled` | `UnitFileState` de `systemctl show` |
| `active_state` | `ActiveState` crudo |
| `sub_state` | `SubState` crudo |
| `state` | etiqueta normalizada (`running`, `stopped`, `failed`, `starting`, `restarting`, ...) |
| `mountpoint` | mountpoint parseado de `ExecStart` y expandido `%h`/`%i` |

### `UnitStatus` (internal/service/status.go)

Estado detallado de una cuenta. Añade a los campos de instancia:

| Campo | Significado |
|---|---|
| `pid` | `MainPID` (`0` → omitido) |
| `journal_tail` | hasta 10 líneas best-effort del journal para unidades no activas |

### `ActionResult` (internal/service/results.go)

Envoltorio de operación para los subcomandos de acción.

| Campo | Significado |
|---|---|
| `action` | `install` / `uninstall` / `start` / `stop` |
| `ok` | la operación se completó (un no-op exitoso cuenta) |
| `account` | operaciones de una sola cuenta |
| `affected_accounts` | operaciones `--all`, ordenado determinísticamente |
| `service_file` | ruta cuando la operación la crea/borra/reporta |
| `mountpoint` | cuando la operación tiene un mountpoint inequívoco |
| `warning` | aviso no fatal (p. ej. un mountpoint guardado que fue ignorado) |
| `message` | explicación humana concisa (no es un contrato de API) |
| `error` | se rellena cuando la acción falló tras un contexto de resultado válido |

`OK` informa del éxito de la operación, **no** de que un servicio esté
corriendo; el estado actual pertenece a `InstanceInfo`/`UnitStatus`.

## Arquitectura

La feature está **separada** a propósito de la interfaz `Formatter` existente
en `cmd/onecloudriver/output.go`, que está ligada a `graph.DriveItem`
(`FormatDriveItems`/`FormatDriveItem`). La serialización es idéntica para
cualquier tipo, así que se añadió un helper genérico en lugar de ampliar esa
interfaz:

- `validateOutputFormat(name)` — error canónico `unsupported format`,
  compartido por `getFormatter` y por la ruta de service.
- `formatStructuredValue(format, v)` — `json.MarshalIndent` / `yaml.Marshal`
  genéricos para valores arbitrarios. `text` se rechaza (el texto lo renderiza
  el llamador).

El paquete service separa la ejecución de systemd de la presentación:

- `internal/service/results.go` — `ActionResult` + operaciones data-oriented
  que **devuelven datos y nunca imprimen a stdout**:
  `InstallServiceResult`, `UninstallServiceResult`, `StartServiceResult`,
  `StopServiceResult`, `EnableUnitQuiet`.
- Las funciones que imprimen en texto (`InstallService`, `UninstallService`,
  `Systemctl`, `EnableUnit`, `Status`) se conservan sin cambios para el modo texto.
- `internal/service/status.go` — `QueryUnitStatus` expone el error del journal
  por separado para que el CLI lo muestre en stderr sin fallar la consulta.

Wiring del CLI (`cmd/onecloudriver/service.go`):

- `resolveServiceOutput(cmd)` — lee y valida el flag heredado.
- `writeServiceStructured(cmd, format, v)` — escribe el único documento en
  `cmd.OutOrStdout()`.
- `resolveServiceAccount(cmd, manager, quiet)` — resolución de cuenta en modo
  silencioso (suprime "Using the only default account" en modo estructurado).
- `installAllStructured` / `stopAllStructured` — agregan un único `ActionResult`
  para `--all`, con `affected_accounts` ordenado determinísticamente.

## Ejemplos

```bash
onecloudriver service list -o json
onecloudriver service status usuario@outlook.com -o yaml
onecloudriver service install -a usuario@outlook.com -o json
onecloudriver service stop --all -o yaml

# Pasar a jq
onecloudriver service list -o json | jq .
```

## Testing

- `cmd/onecloudriver/output_test.go` — `validateOutputFormat`,
  `formatStructuredValue` (round-trip JSON/YAML, slice vacío → `[]`, rechazo de
  `text`/desconocido), formateadores de DriveItem sin cambios.
- `cmd/onecloudriver/service_test.go` — registro del flag persistente,
  `resolveServiceOutput`, `writeServiceStructured` (JSON parseable, lista vacía).
- `internal/service/results_test.go` — operaciones `ActionResult` con scripts
  falsos de `systemctl`/`mount` en `PATH` (sin sesión systemd real), orden de
  `affected_accounts`, no-op de "no instalado", rechazo de cuenta vacía.

## Pitfalls / gotchas

1. **Pureza de stdout** — `internal/service` nunca debe usar `fmt.Print` en modo
   estructurado. Las funciones `*Result` usan `runSystemCommand` (captura
   stdout/stderr) y un unmount silencioso, así que nada se filtra.
2. **La resolución de cuenta imprime** — `resolveAccountName` y
   `resolveInstallMountpoint` imprimen líneas informativas. En modo estructurado
   deben ir a stderr o suprimirse (`resolveServiceAccount(..., quiet=true)` y
   `resolveInstallMountpoint` con writer).
3. **`install` sin `-a`** — el código antiguo pasaba el flag vacío a
   `InstallService`; hay que usar el `acc.Name` resuelto.
4. **Scripts falsos de systemctl en tests** — construir el script con
   concatenación Go explícita (`"... >> \"" + logPath + "\""`). Un literal único
   `"...\"\"+logPath+\"\"..."` escribe en un fichero llamado literalmente
   `+logPath+`.
5. **Salto de línea final** — `formatStructuredValue` normaliza el documento para
   que termine con exactamente un `\n` (JSON no emite ninguno, YAML emite uno).
   Los formateadores de DriveItem de `list`/`info` **no** se cambian.
