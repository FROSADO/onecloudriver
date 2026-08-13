# API: internal/service

> Auto-generated with `go doc -all`. Date: 2026-08-14 00:40:10

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

func ServiceFilePath() (string, error)
    ServiceFilePath returns the path to the user's systemd service file.

func ServiceUnit(mountpoint, binary string) string
    ServiceUnit generates the content of the systemd service file for the given
    mountpoint and the already-resolved binary path.

func Status(args []string) error
    Status shows the status of the service.

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

```
