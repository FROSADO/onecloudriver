# API: internal/service

> Auto-generated with `go doc -all`. Date: 2026-08-20 23:36:27

```
package service // import "github.com/frosado/onecloudriver/internal/service"

Package service contains the systemd integration logic of the onecloudriver CLI:
service file generation, systemctl calls and FUSE unmount helpers.

Extracted from cmd/onecloudriver/service.go (issue #6) so the logic is testable
and independent of the cobra layer.

FUNCTIONS

func DefaultMountpointFor(account string) string
    DefaultMountpointFor returns the default mountpoint for an account.

func EnableUnit(account string)
    EnableUnit enables and starts a systemd unit for an account.

func EnableUnitQuiet(account string) error
    EnableUnitQuiet enables and starts a systemd unit for an account without
    writing to stdout. It is the structured-output counterpart of EnableUnit.

func InstallService(mountpoint, account string) error
    InstallService creates the service file and reloads systemd.

func JournalTail(unit string, lines int) ([]string, error)
    JournalTail returns up to lines from the unit's user journal without using a
    pager. A non-positive line count uses the default of ten lines.

func ServiceFilePath() (string, error)
    ServiceFilePath returns the path to the user's systemd service file.

func ServiceUnit(mountpoint, binary string) string
    ServiceUnit generates the content of the systemd service file for the given
    mountpoint and the already-resolved binary path.

func Status(args []string) error
    Status shows the status of the service. A failed service is reported as a
    successful status query, with its recent journal lines when available.

func Systemctl(action, account string) error
    Systemctl runs a systemctl --user command for an account.

func UninstallService(accounts ...string) error
    UninstallService stops the running instances, removes the service file and
    reloads systemd.

    With accounts, each listed account is unmounted, stopped and disabled (the
    former --all CLI behaviour). With no accounts, the running instances are
    discovered via `systemctl list-units onecloudriver@*` (the single-account
    behaviour). Both paths share the same tail: remove the service file and
    reload the daemon.

func UnmountMountpoint(account string)
    UnmountMountpoint unmounts the FUSE filesystem for an account. Uses
    fusermount3 -uz (lazy-unmount) to guarantee the mountpoint is freed even if
    there are processes accessing it.


TYPES

type ActionResult struct {
	Action           string   `json:"action" yaml:"action"`
	OK               bool     `json:"ok" yaml:"ok"`
	Account          string   `json:"account,omitempty" yaml:"account,omitempty"`
	AffectedAccounts []string `json:"affected_accounts,omitempty" yaml:"affected_accounts,omitempty"`
	ServiceFile      string   `json:"service_file,omitempty" yaml:"service_file,omitempty"`
	Mountpoint       string   `json:"mountpoint,omitempty" yaml:"mountpoint,omitempty"`
	Warning          string   `json:"warning,omitempty" yaml:"warning,omitempty"`
	Message          string   `json:"message,omitempty" yaml:"message,omitempty"`
	Error            string   `json:"error,omitempty" yaml:"error,omitempty"`
}
    ActionResult is the machine-readable result envelope for the service action
    subcommands (install, uninstall, start, stop). The CLI serializes it for
    structured output modes; the service package never renders it.

    Field semantics:
      - Action is one of install, uninstall, start, or stop.
      - OK reports whether the operation completed (a successful no-op counts).
      - Account is set for single-account operations.
      - AffectedAccounts is set for --all operations and sorted
        deterministically.
      - ServiceFile and Mountpoint are set when the operation has unambiguous
        values.
      - Warning carries a non-fatal advisory (e.g. a saved mountpoint that
        was ignored), mirroring what text mode prints.
      - Message is a concise human-readable explanation (not an API contract).
      - Error is set when the action failed after a valid result context
        existed.

func InstallServiceResult(mountpoint, account string) (ActionResult, error)
    InstallServiceResult creates the service file, ensures the mountpoint and
    reloads systemd without writing to stdout. A failure returns the partially
    populated result (OK=false, Error set) alongside the wrapped error so the
    CLI can still emit one machine-readable document and exit non-zero.

func StartServiceResult(account string) (ActionResult, error)
    StartServiceResult starts the unit for an account without writing to stdout,
    returning an ActionResult.

func StopServiceResult(account string) (ActionResult, error)
    StopServiceResult unmounts the account's FUSE mountpoint and stops its unit
    without writing to stdout, returning an ActionResult.

func UninstallServiceResult(accounts ...string) (ActionResult, error)
    UninstallServiceResult stops and disables instances, removes the
    service file and reloads systemd without writing to stdout, returning an
    ActionResult. With accounts (the former --all behaviour) each listed account
    is processed; otherwise the running instances are discovered via `systemctl
    list-units onecloudriver@*`.

type InstanceInfo struct {
	Unit        string `json:"unit" yaml:"unit"`
	Account     string `json:"account" yaml:"account"`
	Enabled     string `json:"enabled" yaml:"enabled"`
	ActiveState string `json:"active_state" yaml:"active_state"`
	SubState    string `json:"sub_state" yaml:"sub_state"`
	State       string `json:"state" yaml:"state"`
	Mountpoint  string `json:"mountpoint,omitempty" yaml:"mountpoint,omitempty"`
}
    InstanceInfo describes one installed onecloudriver user-service instance.

func ListInstances() ([]InstanceInfo, error)
    ListInstances returns every installed instantiated onecloudriver user unit,
    including disabled, stopped, and never-started instances.

type UnitStatus struct {
	Unit        string   `json:"unit" yaml:"unit"`
	Account     string   `json:"account" yaml:"account"`
	ActiveState string   `json:"active_state" yaml:"active_state"`
	SubState    string   `json:"sub_state" yaml:"sub_state"`
	State       string   `json:"state" yaml:"state"`
	PID         int64    `json:"pid,omitempty" yaml:"pid,omitempty"`
	Mountpoint  string   `json:"mountpoint,omitempty" yaml:"mountpoint,omitempty"`
	JournalTail []string `json:"journal_tail,omitempty" yaml:"journal_tail,omitempty"`
}
    UnitStatus contains the machine-readable state of one service instance.
    Raw systemd states are kept alongside State so callers do not lose detail
    when the CLI renders a concise label.

func GetUnitStatus(account string) (UnitStatus, error)
    GetUnitStatus returns the current structured status for an account.
    A service being failed is valid status data; an error is returned only when
    systemd state itself cannot be queried or parsed.

func QueryUnitStatus(account string) (UnitStatus, error, error)
    QueryUnitStatus returns the current structured status for an account
    together with a best-effort journal error (nil when the journal was read or
    not needed). The journal error is separate because a missing/inaccessible
    journal must not hide an otherwise valid service state; callers surface it
    on stderr.

```
