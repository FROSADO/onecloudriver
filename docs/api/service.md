# API: internal/service

> Auto-generated with `go doc -all`. Date: 2026-08-20 00:38:19

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

```
