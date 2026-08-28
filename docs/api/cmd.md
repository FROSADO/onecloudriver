# API: cmd/onecloudriver

> Auto-generated with `go doc -all`. Date: 2026-08-27 09:06:13

```


TYPES

type Formatter interface {
	FormatDriveItems(items []graph.DriveItem) (string, error)
	FormatDriveItem(item *graph.DriveItem) (string, error)
}
    Formatter converts Graph data into a textual representation for output.

    Each implementation handles a different format (text, JSON, YAML, etc.).
    To add a new format, simply create a new implementation and register it in
    the formatters map.

```
