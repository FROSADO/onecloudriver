# Service command structured output (`--output text|json|yaml`)

> Reference for the machine-readable output of every `onecloudriver service`
> subcommand. Implemented for issue #43. Source of truth: `cmd/onecloudriver/service.go`,
> `cmd/onecloudriver/output.go` and `internal/service/results.go`.

## Overview

Every `service` subcommand accepts a **persistent** `--output`/`-o` flag:

```bash
onecloudriver service <install|uninstall|list|status|start|stop> -o text|json|yaml
```

- `text` (default) — unchanged human-oriented output with `internal/printer` symbols.
- `json` / `yaml` — exactly **one** serialized document on stdout, no printer
  symbols, no progress chatter, no raw `systemctl` output.

The flag is registered once on the parent `serviceCmd`
(`serviceCmd.PersistentFlags().StringP("output", "o", "text", ...)`) so every
subcommand inherits it. It is validated **before** any systemd or account side
effect.

## Output contract

| Stream | `text` | `json` / `yaml` |
|---|---|---|
| stdout | human output (tables, symbols) | exactly one serialized document |
| stderr | (not used for normal flow) | diagnostics, warnings, progress ("Using saved mountpoint", journal warnings) |

Exit-code semantics:

- A **successfully queried failed/stopped service** is data, not an error →
  exit code `0` (the document carries `state: failed` / `state: stopped`).
- A genuine systemd/query failure (missing unit, no bus, malformed output) →
  non-zero, diagnostic on stderr, no partial document.
- An action (`install`/`uninstall`/`start`/`stop`) that fails after a valid
  action context → one `ActionResult` with `ok: false` + `error`, and a
  non-zero exit. The document is still emitted so scripts can inspect it.

Invalid formats are rejected before side effects:

```bash
onecloudriver service list -o xml
# Error: unsupported format: "xml" (valid: text, json, yaml)
```

## Structured result shapes

| Invocation | Structured result |
|---|---|
| `service list` | `[]InstanceInfo` (empty → `[]`) |
| `service status` (no account) | `[]InstanceInfo` (same data source as `list`) |
| `service status ACCOUNT` | `UnitStatus` (one object, incl. `journal_tail` when applicable) |
| `install` / `uninstall` / `start` / `stop` | `ActionResult` |
| `... --all` | one `ActionResult` with sorted `affected_accounts` |

## Data models

All models carry stable JSON/YAML tags (snake_case). `omitempty` is used only
where the field legitimately has no value for the operation.

### `InstanceInfo` (internal/service/instances.go)

One installed instantiated unit, including disabled/stopped/never-started.

| Field | Meaning |
|---|---|
| `unit` | unit name, e.g. `onecloudriver@a@example.com.service` |
| `account` | account (instance) name |
| `enabled` | `UnitFileState` from `systemctl show` |
| `active_state` | raw `ActiveState` |
| `sub_state` | raw `SubState` |
| `state` | normalized label (`running`, `stopped`, `failed`, `starting`, `restarting`, ...) |
| `mountpoint` | mountpoint parsed from `ExecStart` and `%h`/`%i`-expanded |

### `UnitStatus` (internal/service/status.go)

Detailed status of one account. Extends the instance fields with:

| Field | Meaning |
|---|---|
| `pid` | `MainPID` (`0` → omitted) |
| `journal_tail` | up to 10 best-effort journal lines for non-running units |

### `ActionResult` (internal/service/results.go)

Operation envelope for the action subcommands.

| Field | Meaning |
|---|---|
| `action` | `install` / `uninstall` / `start` / `stop` |
| `ok` | operation completed (a successful no-op counts) |
| `account` | single-account operations |
| `affected_accounts` | `--all` operations, sorted deterministically |
| `service_file` | path when the operation creates/removes/reports it |
| `mountpoint` | when the operation has one unambiguous mountpoint |
| `warning` | non-fatal advisory (e.g. a saved mountpoint that was ignored) |
| `message` | concise human-readable explanation (not an API contract) |
| `error` | set when the action failed after a valid result context |

`OK` reports operation success, **not** that a service is currently running;
current state belongs to `InstanceInfo`/`UnitStatus`.

## Architecture

The feature is deliberately **separate** from the existing `Formatter`
interface in `cmd/onecloudriver/output.go`, which is tied to `graph.DriveItem`
(`FormatDriveItems`/`FormatDriveItem`). Serialization is identical for every
value type, so a generic helper was added instead of growing that interface:

- `validateOutputFormat(name)` — canonical `unsupported format` error, shared
  by `getFormatter` and the service path.
- `formatStructuredValue(format, v)` — generic `json.MarshalIndent` /
  `yaml.Marshal` for arbitrary values. `text` is rejected (the caller renders
  text itself).

The service package separates systemd execution from presentation:

- `internal/service/results.go` — `ActionResult` + data-oriented operations
  that **return data and never print to stdout**:
  `InstallServiceResult`, `UninstallServiceResult`, `StartServiceResult`,
  `StopServiceResult`, `EnableUnitQuiet`.
- The text-printing functions (`InstallService`, `UninstallService`,
  `Systemctl`, `EnableUnit`, `Status`) are preserved unchanged for text mode.
- `internal/service/status.go` — `QueryUnitStatus` exposes the journal error
  separately so the CLI can surface it on stderr without failing the query.

CLI wiring (`cmd/onecloudriver/service.go`):

- `resolveServiceOutput(cmd)` — reads + validates the inherited flag.
- `writeServiceStructured(cmd, format, v)` — writes the single document to
  `cmd.OutOrStdout()`.
- `resolveServiceAccount(cmd, manager, quiet)` — quiet account resolution
  (suppresses the "Using the only default account" line in structured mode).
- `installAllStructured` / `stopAllStructured` — aggregate one `ActionResult`
  for `--all`, with deterministic sorted `affected_accounts`.

## Examples

```bash
onecloudriver service list -o json
onecloudriver service status user@outlook.com -o yaml
onecloudriver service install -a user@outlook.com -o json
onecloudriver service stop --all -o yaml

# Pipe to jq
onecloudriver service list -o json | jq .
```

## Testing

- `cmd/onecloudriver/output_test.go` — `validateOutputFormat`,
  `formatStructuredValue` (JSON/YAML round-trip, empty-slice → `[]`, text/unknown
  rejection), existing DriveItem formatters unchanged.
- `cmd/onecloudriver/service_test.go` — persistent flag registration,
  `resolveServiceOutput`, `writeServiceStructured` (parseable JSON, empty list).
- `internal/service/results_test.go` — `ActionResult` operations with fake
  `systemctl`/`mount` scripts on `PATH` (no live user systemd session), sorted
  `affected_accounts`, not-installed no-op, empty-account rejection.

## Pitfalls / gotchas

1. **stdout purity** — `internal/service` must never `fmt.Print` in structured
   mode. The data-oriented `*Result` functions use `runSystemCommand`
   (captures stdout/stderr) and a quiet unmount, so nothing leaks.
2. **Account resolution prints** — `resolveAccountName`/`resolveInstallMountpoint`
   print informational lines. In structured mode they must go to stderr or be
   suppressed (`resolveServiceAccount(..., quiet=true)`, writer-aware
   `resolveInstallMountpoint`).
3. **`install` without `-a`** — the old code passed the empty flag value to
   `InstallService`; use the resolved `acc.Name`.
4. **Fake systemctl scripts in tests** — build the shell script with explicit Go
   string concatenation (`"... >> \"" + logPath + "\""`). A single literal
   `"...\"\"+logPath+\"\"..."` writes to a file literally named `+logPath+`.
5. **Trailing newline** — `formatStructuredValue` normalizes the document to end
   with exactly one `\n` (JSON emits none, YAML emits one). The existing
   `list`/`info` DriveItem formatters are **not** changed.
