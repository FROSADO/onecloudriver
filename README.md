# OneCloudRiver

> **Native. Fast. No intermediaries.**

[![CI](https://github.com/FROSADO/onecloudriver/actions/workflows/ci.yml/badge.svg)](https://github.com/FROSADO/onecloudriver/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/Go-1.25-blue)](https://go.dev/)
[![License](https://img.shields.io/badge/license-GPLv3-green)](LICENSE)
[![Version](https://img.shields.io/badge/version-0.1.0-orange)](https://github.com/FROSADO/onecloudriver/releases/tag/v0.1.0)

**[🇪🇸 Versión en español](README.es.md)**

---

> ⚠️ **Notice about project origins**
>
> This project is **heavily inspired by [onedriver](https://github.com/jstaf/onedriver)**,
> the native FUSE filesystem for OneDrive created by [@jstaf](https://github.com/jstaf).
>
> OneCloudRiver **is not a GitHub fork** — the amount of changes, complete architectural
> reorganization, and new features added make it an independent project with its own identity.
> However, we want to explicitly acknowledge onedriver's foundational work, without which
> this project would not exist.

---

OneCloudRiver mounts your **OneDrive as a native FUSE filesystem on Linux**, allowing you
to read, write, create, and delete files directly from your file manager (Nautilus, Dolphin,
Thunar) and terminal.

## 🚀 Features

- **Full read-write** — `Create`, `Write`, `Mkdir`, `Rmdir`, `Unlink`, `Rename`, `Chmod`, `Touch`
- **Offline mode** — Cached files available without Internet connection
- **Delta sync** — Remote changes automatically detected via Microsoft Graph
- **Upload Manager** — Asynchronous uploads with retries and concurrency control
- **Smart cache** — Metadata with TTL+LFU eviction, disk content with size-based eviction
- **BoltDB persistence** — Filesystem state preserved across sessions
- **systemd service** — Auto-mount on login (`onecloudriver service install`)
- **Complete CLI** — Operations without mounting: `list`, `download`, `upload`, `info`, `mkdir`, `mv`, `cp`, `rm`, `rename`
- **Health check** — Connectivity and token verification before mounting, with clear diagnostics
- **Retry with backoff** — Automatic retries on 429/503/network errors
- **80%+ test coverage** — Unit, FUSE integration, fuzzing, and security audit

## 📦 Quick Installation

### From binary

```bash
# Download latest release
wget https://github.com/FROSADO/onecloudriver/releases/download/v0.1.0/onecloudriver_linux_amd64.zip
unzip onecloudriver_linux_amd64.zip
sudo cp onecloudriver /usr/local/bin/
```

### From .deb package

```bash
# Download and install the .deb package
wget https://github.com/FROSADO/onecloudriver/releases/download/v0.1.0/onecloudriver_0.1.0_amd64.deb
sudo dpkg -i onecloudriver_0.1.0_amd64.deb
```

Installing the .deb also registers the man page — try `man onecloudriver` after installation.

### From source

```bash
git clone https://github.com/FROSADO/onecloudriver.git
cd onecloudriver
make build
sudo cp onecloudriver /usr/local/bin/
```

### Requirements

- **Go 1.25+**
- **FUSE 3** (`libfuse3` or `fuse3`)
- **Git** (for building from source)

```bash
# Ubuntu/Debian
sudo apt-get install fuse3

# Fedora
sudo dnf install fuse3-libs fuse3
```

## 🔐 Authentication

```bash
# Add a Microsoft account
onecloudriver account add

# Browser will open at http://localhost:9090/callback
# Authorize the application and return to terminal
```

## 💾 Mount OneDrive

```bash
# Basic mount
onecloudriver mount ~/OneDrive -a user@outlook.com

# With custom cache configuration
onecloudriver mount ~/OneDrive -a user@outlook.com \
    --cache-dir ~/.cache/onecloudriver/custom \
    --cache-ttl 120s \
    --cache-max-entries 5000 \
    --cache-max-size 2GB

# Unmount (Ctrl+C in mount terminal, or)
fusermount3 -u ~/OneDrive
```

## 🖥️ CLI usage without mounting

```bash
# List files
onecloudriver list -a user@outlook.com /Documents

# Download
onecloudriver download -a user@outlook.com /photo.jpg -d ~/Downloads

# Upload
onecloudriver upload -a user@outlook.com -f ~/document.pdf --dest-path /Backup

# Create folder
onecloudriver mkdir -a user@outlook.com -n "New Folder" --dest-path /Documents

# Detailed info
onecloudriver info -a user@outlook.com /file.txt -o json
```

## 🔄 systemd service (auto-mount)

```bash
# Install service (auto-detects account if only one exists)
onecloudriver service install --mountpoint ~/OneDrive/%i

# Install and enable for specific account
onecloudriver service install --mountpoint ~/OneDrive/%i -a user@outlook.com --enable

# Install for ALL accounts
onecloudriver service install --mountpoint ~/OneDrive/%i --all --enable

# Manage
onecloudriver service status              # View status
onecloudriver service start user@outlook.com   # Start
onecloudriver service stop user@outlook.com    # Stop (clean unmount)
onecloudriver service stop --all               # Stop all

# Uninstall
onecloudriver service uninstall --all
```

## 🛠️ Development

```bash
# Build
make build

# Unit tests (mock, no FUSE)
make test-unit

# Integration tests (requires FUSE)
make test-integration

# All tests
make test-all

# Lint
make lint

# Security audit
make security-audit

# Coverage
make coverage

# Packaging
make dist          # Zip with binary + manual
make deb           # .deb package
make rpm           # .rpm package (requires rpm-build)

# Release
make release-check # Pre-flight release checklist (read-only)
make release       # Interactive release automation

# Full CI (as in GitHub Actions)
make clean && make build && make test-unit-short && make test-integration-short
```

## 🚀 Releases

The whole release process is automated with [`scripts/release.sh`](scripts/release.sh):
it runs an interactive checklist, updates the CHANGELOG and publishes the release
through the existing [Release workflow](.github/workflows/release.yml).

```bash
# 1. Pre-flight checklist — everything to review before publishing (read-only)
make release-check

# 2. Full interactive flow
make release
```

What the script does:

1. **Pre-flight checklist** — `gh` authentication, repository, default branch,
   working tree state, unpushed commits, open PRs, recent CI status and last tag.
2. **Optional PR merge** — lists the open PRs targeting the default branch and
   offers to squash-merge them (`gh pr merge --squash`) before publishing, then
   switches to the default branch so the release is published from there.
3. **Version selection** — proposes the next patch version (e.g. `0.1.0` → `0.1.1`).
4. **CHANGELOG draft** — generates a draft section from the commits since the last
   tag and opens it in your `$EDITOR` for review.
5. **Version references update (optional)** — refreshes the version badge and download
   URLs, and any version references in the docs (`docs/MANUAL*.md`, man pages).
6. **Commit + tag + push** — commits the changes, creates the annotated tag `vX.Y.Z`
   and pushes it. The [Release workflow](.github/workflows/release.yml) then builds
   the artifacts (zip, `.deb`, `.rpm`) and creates the GitHub Release.
7. **Monitoring** — watches the workflow run and verifies the published release.

> **Requirements:** [GitHub CLI](https://cli.github.com/) authenticated (`gh auth login`).
> The script is interactive; use `bash scripts/release.sh --yes` to auto-confirm the
> prompts (the editor still opens) or `--check` for the read-only checklist.

## 🏗️ Architecture

```
cmd/onecloudriver/     CLI (cobra): account, mount, list, download, upload, service...
    │
internal/
    ├── auth/          OAuth2 + keyring + account manager
    ├── graph/         Microsoft Graph HTTP client + retry/backoff
    └── fs/            FUSE filesystem
         ├── root.go              OneCloudFS: filesystem root
         ├── drive_item_node.go   FUSE node per file/folder
         ├── fs_ops.go            Operations: Mkdir, Create, Rename, Delete...
         ├── cache.go             InodeCache: metadata with TTL+LFU eviction + BoltDB
         ├── content_cache.go     ContentCache: disk content with eviction
         ├── delta.go             DeltaSync: Graph /delta polling
         ├── upload_manager.go    Async upload queue with retries
         └── mount.go             Mount: health check + lifecycle
```



## 📄 License

GPLv3 — see [LICENSE](LICENSE) for details.

## 🙏 Acknowledgments

This project would not exist without the pioneering work of **[onedriver](https://github.com/jstaf/onedriver)**
by [@jstaf](https://github.com/jstaf) and contributors. Much of the FUSE architecture and Graph
client are based on its original design.
