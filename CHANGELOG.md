# Changelog

All notable changes to OneCloudRiver are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [0.1.2] - 2026-08-07

### Added
- feat(ci): sign release artifacts with GPG — `9eddda0`
- feat(cli): add --version flag — `e15162c`

### Other
- chore(packaging): unify systemd service template between deb and rpm — `cc5b230`
- chore(docs): add .rpm install instructions — `48c875c`

---

## [0.1.1] - 2026-08-07

### Added

- RPM distribution package (`onecloudriver-*.rpm`) for RPM-based distros (Fedora, RHEL, openSUSE, etc.)
- Release automation: `scripts/release.sh` with `make release` / `make release-check` targets

---

## [0.1.0] - 2026-08-05

### 🗂️ FUSE Filesystem

- **Full read-write support**: `Create`, `Write`, `Mkdir`, `Rmdir`, `Unlink`, `Rename`, `Fsync`, `Flush`

### 💾 Content Cache (`ContentCache`)

- On-disk content storage
- Size-based eviction guarded by `evictMu` (TOCTOU-safe)

### 🔄 Delta Synchronization (`DeltaSync`)

- Polls the Microsoft Graph `/delta` endpoint every N minutes (default: 5)
- Handles all 3 delta cases: new, modified (including moves between folders), and deleted items
- `deltaLink` persisted in BoltDB → resumes from the last sync after a restart
- Conflict resolution: local items (IDs prefixed with `local:`) reconciled against remote ones

### 🔐 Authentication

- OAuth2 flow with Microsoft Identity Platform
- Copy-paste fallback when no browser is available
- Credentials stored in the system keyring

---

[0.1.2]: https://github.com/FROSADO/onecloudriver/releases/tag/v0.1.2
[0.1.1]: https://github.com/FROSADO/onecloudriver/releases/tag/v0.1.1
[0.1.0]: https://github.com/frosado/onecloudriver/releases/tag/v0.1.0
