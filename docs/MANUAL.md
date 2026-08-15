# OneCloudRiver — User Manual

Native filesystem for OneDrive on Linux.

OneCloudRiver mounts your OneDrive as a FUSE filesystem on Linux,
allowing you to read, write, create, and delete files directly from the
file explorer (Nautilus, Dolphin, Thunar, etc.) and the terminal.

---

## Installation

### From binary (zip)

```bash
unzip onecloudriver_linux_amd64.zip
sudo cp onecloudriver /usr/local/bin/
```

### From .deb package

```bash
sudo dpkg -i onecloudriver_*.deb
```

### From .rpm package

```bash
sudo dnf install ./onecloudriver*.rpm
```

Or with your distro's package manager (requires `fuse3`, which is resolved automatically):

```bash
# Fedora / RHEL 8+ / Rocky Linux / AlmaLinux
sudo dnf install ./onecloudriver-0.1.3-1.x86_64.rpm

# RHEL / CentOS 7 (older)
sudo yum install ./onecloudriver-0.1.3-1.x86_64.rpm

# openSUSE
sudo zypper install ./onecloudriver-0.1.3-1.x86_64.rpm
```

The package installs the binary to `/usr/local/bin`, the man page (`man onecloudriver`),
the systemd user service template and the documentation under `/usr/share/doc/onecloudriver/`.

### Requirements

- **FUSE**: `sudo apt install fuse3` (or `fuse` on older distributions)
- **Permissions**: the user must belong to the `fuse` group
  ```bash
  sudo usermod -aG fuse $USER
  # Log out and log back in
  ```

---

## Initial setup

### 1. Add a Microsoft account

```bash
onecloudriver account add
```

This opens the browser at `http://localhost:9090/callback`. Sign in with your
Microsoft account and authorize OneCloudRiver to access your files.

If the browser is not available, the program displays a URL to copy and
paste manually.

### 2. List configured accounts

```bash
onecloudriver account list
```

### 3. Remove an account

```bash
# Ask whether to delete the local cache
onecloudriver account remove user@outlook.com

# Delete account and cache without asking
onecloudriver account remove user@outlook.com --purge

# Keep the local cache
onecloudriver account remove user@outlook.com --keep
```

---

## Mounting OneDrive

### Basic mount

```bash
onecloudriver mount /path/to/mountpoint -a user@outlook.com
```

Example:

```bash
mkdir ~/OneDrive
onecloudriver mount ~/OneDrive -a paveryutu72@hotmail.com
```

### Cache configuration flags

| Flag | Default | Description |
|---|---|---|
| `--cache-dir` | `~/.cache/onecloudriver/<account>` | Root cache directory |
| `--cache-ttl` | `60s` | Metadata TTL (e.g.: `5m`, `300s`) |
| `--cache-max-entries` | `2000` | Max folders with cached children |
| `--cache-max-size` | `0` (unlimited) | Max content cache size (e.g.: `1GB`, `500MB`) |

### Unmounting

Press `Ctrl+C` in the terminal where you ran `mount`. If the file explorer
is open at the mountpoint, unmounting uses lazy-unmount and completes when
you close the explorer.

### Persisted configuration

On successful mount, the configuration is automatically saved to
`~/.config/onecloudriver/<account>.json` and reused on the next session.
This includes the mountpoint, cache parameters, and advanced options.

The account JSON looks like this:

```json
{
  "name": "user@outlook.com",
  "config": { "...oauth2..." },
  "expires_at": 1785884980,
  "mount": {
    "defaultMountpoint": "/home/user/OneDrive",
    "cacheDir": "~/.cache/onecloudriver/user@outlook.com",
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

Fields not present in the JSON use their default values.

#### Full parameter table

| JSON field | CLI flag | Default | Description |
|---|---|---|---|
| `defaultMountpoint` | *(positional argument)* | `./<account>` | Last mountpoint used successfully |
| `cacheDir` | `--cache-dir` | `~/.cache/onecloudriver/<account>` | Root cache directory |
| `cacheTTL` | `--cache-ttl` | `60s` | Base TTL for cached metadata |
| `cacheMaxEntries` | `--cache-max-entries` | `2000` | Max folders with cached children |
| `cacheMaxSize` | `--cache-max-size` | `0` (unlimited) | Max ContentCache size on disk |
| `deltaInterval` | `--delta-interval` | `5m` | Polling interval for the `/delta` endpoint |
| `maxUploadsInFlight` | `--max-uploads` | `5` | Max concurrent uploads |
| `maxUploadRetries` | `--upload-retries` | `5` | Retries before abandoning an upload |
| `httpTimeout` | `--http-timeout` | `15s` | Timeout for HTTP requests to Graph |
| `graphRetries` | `--graph-retries` | `3` | HTTP retries on 429/503 errors |

Advanced parameters (`deltaInterval`, `maxUploadsInFlight`, etc.) are not
written to the JSON unless the user explicitly configures them via CLI flags.
If absent from the JSON, defaults are used.

#### Example: customize and persist

```bash
# First mount: configure everything
onecloudriver mount ~/OneDrive -a user@outlook.com \
    --cache-ttl 120s \
    --cache-max-size 2GB \
    --delta-interval 10m \
    --max-uploads 3

# Subsequent mounts: inherit configuration automatically
onecloudriver mount
# → Using saved mountpoint: /home/user/OneDrive
# → (cache-ttl=120s, cache-max-size=2GB, delta-interval=10m, max-uploads=3)
```

---

## systemd service (auto-mount)

OneCloudRiver can be installed as a user systemd service to
automatically mount OneDrive on login.

### Install the service

The mountpoint is determined automatically if the account has a saved
`defaultMountpoint`. Otherwise, it uses `~/OneDrive/%i` expanded to the
absolute home path (e.g. `/home/<user>/OneDrive/%i`) as fallback.
You can override it with `--mountpoint`:

```bash
# Use the defaultMountpoint from the account JSON (if it exists)
onecloudriver service install

# Or specify one explicitly (a leading ~/ is expanded to the home path)
onecloudriver service install --mountpoint /home/<user>/OneDrive/%i
```

If only **one account** is configured, it is used automatically. With multiple
accounts, specify `--account`:

```bash
onecloudriver service install --mountpoint /home/<user>/OneDrive/%i -a user@outlook.com
```

This creates the template `~/.config/systemd/user/onecloudriver@.service` and
reloads systemd. If the mountpoint directory does not exist yet, it is
created automatically during installation (the CLI reports it).

### Enable and start in one step

With `--enable`, the service is enabled and started immediately:

```bash
onecloudriver service install --mountpoint /home/<user>/OneDrive/%i --enable
```

OneDrive mounts at `/home/<user>/OneDrive/user@outlook.com` and restarts
automatically on login.

### Install for all accounts

With `--all`, the service is installed for **all** configured accounts:

```bash
onecloudriver service install --mountpoint /home/<user>/OneDrive/%i --all --enable
```

### Manage the service

```bash
# View status of all accounts
onecloudriver service status

# View status of a specific account
onecloudriver service status user@outlook.com

# Start manually
onecloudriver service start user@outlook.com

# Stop and unmount (runs fusermount3 -uz + systemctl stop)
onecloudriver service stop user@outlook.com

# View logs
journalctl --user -u onecloudriver@user@outlook.com -f
```

### Uninstall the service

```bash
# Uninstall for all accounts (unmount + stop + disable + delete file)
onecloudriver service uninstall --all

# Or uninstall only the currently active instances
onecloudriver service uninstall
```

### Custom mountpoint

```bash
onecloudriver service install --mountpoint /mnt/onedrive/%i
```

### Generated systemd service

```ini
[Unit]
Description=OneCloudRiver - OneDrive filesystem for %i
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/onecloudriver mount /home/<user>/OneDrive/%i -a %i
ExecStop=/bin/fusermount3 -uz /home/<user>/OneDrive/%i
Restart=on-failure
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
```

#### How the `ExecStart` binary path is resolved

The `ExecStart` binary path is **not** taken verbatim from how you invoked the
command. `onecloudriver service install` resolves it like this:

1. If the binary was invoked by an explicit path (absolute or relative, e.g.
   `/usr/local/bin/onecloudriver` or `./onecloudriver`), that path is resolved
   to an absolute path and must exist and be executable.
2. Otherwise (invoked by bare name, e.g. `onecloudriver` from your `PATH`), it
   is looked up with `exec.LookPath`.
3. A `go test` binary path (a `*.test` file under a temporary `go-build`
   directory) is never accepted; the canonical `onecloudriver` name is used
   instead.

If no valid binary can be resolved, `service install` fails with an error and
**does not write the unit**, rather than silently writing a broken `ExecStart`.
For the most predictable result, run the command through the installed binary,
e.g. `/usr/local/bin/onecloudriver service install`.

> ⚠️ **Do not use `go run` for `service install`.** `go run` compiles into an
> ephemeral `/tmp/go-build*` directory and deletes the binary when the process
> exits, so the unit would reference a path that no longer exists and the
> service would fail with `203/EXEC`. Use `go build` (or `make build`) and run
> the resulting binary with an explicit path instead.

#### Troubleshooting: `203/EXEC`

If a service instance fails to start with `203/EXEC` in
`systemctl --user status onecloudriver@<account>`, the `ExecStart` binary path
in the unit does not exist (e.g. it was generated from a temporary `go test`
binary or a stale path). Fix it by reinstalling the service with the real
binary:

```bash
/usr/local/bin/onecloudriver service uninstall --all
/usr/local/bin/onecloudriver service install -a user@outlook.com
systemctl --user daemon-reload
```

### The packaged service unit (deb/rpm)

Installing the `.deb` or `.rpm` package also ships a systemd **user** service
template at `/usr/lib/systemd/user/onecloudriver@.service`:

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

It is a **template unit** (`onecloudriver@.service`): the `%i` placeholder is
replaced by the instance name (the account) when you enable it. For example,
to auto-mount the account `user@outlook.com` after installing the package:

```bash
systemctl --user daemon-reload
systemctl --user enable --now 'onecloudriver@user@outlook.com.service'
```

- `%i` → the account name (e.g. `user@outlook.com`)
- `%h` → your home directory (e.g. `/home/user`), so the default mountpoint
  is `$HOME/OneDrive/<account>`

> ⚠️ Only a **single** `%i`/`%h` is a specifier. A literal `%%` in a unit file
> is an escaped percent sign, so `%%i` would be passed to the command as the
> literal string `%i` and the instance would never expand.

Note that `onecloudriver service install` writes a **user-level** unit at
`~/.config/systemd/user/onecloudriver@.service`, which **takes precedence**
over the packaged one for your user. Use `service install` (or `service
uninstall`) when you want the generated, account-aware unit; the packaged
unit is a fallback that works out of the box for any account without further
setup.

---

## File operations

Once mounted, you can use any standard Linux tool:

```bash
# List files
ls ~/OneDrive

# Create folder
mkdir ~/OneDrive/NewFolder

# Create file
echo "Hello world" > ~/OneDrive/document.txt

# Copy local files to OneDrive
cp photo.jpg ~/OneDrive/Photos/

# Move/rename
mv ~/OneDrive/document.txt ~/OneDrive/renamed.txt

# Delete
rm ~/OneDrive/old_file.txt

# Change permissions
chmod 600 ~/OneDrive/secret.txt

# Update timestamp
touch ~/OneDrive/document.txt
```

Also works from Nautilus, Dolphin, Thunar, or any file explorer
that supports FUSE.

---

## CLI operations (without mounting)

You can use OneCloudRiver directly from the command line without
needing to mount:

### List files

```bash
onecloudriver list -a user@outlook.com
onecloudriver list "/Documents" -a user@outlook.com
```

### Upload file

```bash
onecloudriver upload local_file.txt "/Documents/" -a user@outlook.com
```

### Download file

```bash
onecloudriver download "/Documents/file.txt" -a user@outlook.com
```

### Create folder

```bash
onecloudriver mkdir "/NewFolder" -a user@outlook.com
```

### Copy

```bash
onecloudriver copy "/source.txt" "/dest.txt" -a user@outlook.com
```

### Move

```bash
onecloudriver mv "/source.txt" "/Documents/" -a user@outlook.com
```

### Rename

```bash
onecloudriver rename "/old.txt" "new.txt" -a user@outlook.com
```

### Delete

```bash
onecloudriver rm "/file.txt" -a user@outlook.com
```

### Info

```bash
onecloudriver info "/Documents/file.txt" -a user@outlook.com
```

---

## Offline mode

If there is no Internet connection when mounting, OneCloudRiver starts
in offline mode: read-only of previously cached files.

```bash
# When mounting without network:
⚠️  No Internet connection. Starting in offline mode (cache read-only).
```

Files that were already in the cache can be read. Write operations are
not available in offline mode.

---

## Health check

On mount, OneCloudRiver verifies that the authentication token is valid
by calling Microsoft Graph. If the token expired or was revoked:

```
Error: token verification against Microsoft Graph failed

📋 Diagnosis: the access token for 'user@outlook.com' is invalid.
   Common causes:
   • The session expired and the refresh token was revoked
   • The account was deleted or the password changed
   • The application was revoked in the Azure portal

   Solution: re-authenticate with:
     onecloudriver account remove user@outlook.com
     onecloudriver account add
```

---

## Cache structure

```
~/.cache/onecloudriver/
└── user@outlook.com/
    ├── inodes.db          # Metadata (BoltDB)
    └── content/           # Cached files on disk
        ├── <item_id_1>
        ├── <item_id_2>
        └── ...
```

---

## Security audit

```bash
make security-audit
cat audit-report.txt
```

Tools run: gosec, govulncheck, golangci-lint, go test -race.

---

## Troubleshooting

### "Device or resource busy" on unmount

Close the file explorer and any terminal with `cd` inside the
mountpoint. Then:

```bash
fusermount3 -u /path/to/mountpoint
```

### "Transport endpoint is not connected"

The OneCloudRiver process terminated unexpectedly. Unmount and remount:

```bash
fusermount3 -uz /path/to/mountpoint
onecloudriver mount /path/to/mountpoint -a user@outlook.com
```

### Token expired

```bash
onecloudriver account remove user@outlook.com
onecloudriver account add
```

### Permission denied (FUSE)

```bash
sudo usermod -aG fuse $USER
# Log out and log back in
```

---

## License

OneCloudRiver is free software.
