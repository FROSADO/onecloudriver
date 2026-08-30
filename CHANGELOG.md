# Changelog

All notable changes to OneCloudRiver are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [0.1.5] - 2026-08-30

### Added
- perf(fs): time-bucketed TTL sweep + persistent size-eviction heap for InodeCache metadata eviction (#66) (#139) — `ca94a68`
- feat(fs): cap in-flight FUSE request bytes at 16 MiB (#138) — `de52e92`
- feat(fs): log FUSE handler panics via zerolog and return EIO instead of panicking (#137) — `db9f64c`

### Fixed
- fix(fs): trust local children list in HasChildren to allow rmdir of emptied folders (#130) — `cfc60f8`

### Changed
- chore: upgrade Go toolchain to 1.26.7 (#136) — `98129a9`
- chore(deps): update indirect and test dependencies (#135) — `441893a`

### Other
- test(bench): add reproducible benchmark batteries (binary-local, FUSE cache, size-eviction pressure and per-version perf snapshots) alongside the #66 rework, documented in `docs/BENCHMARKS.md` and `docs/plans/PLAN_4.md` (#139) — `ca94a68`

---

## [0.1.4] - 2026-08-24

### Added
- feat(obs): lightweight observability (JSON logs + debug expvar/pprof) (#127) — `72b1cba`
- feat(service): add structured output and safe mountpoint templates (#115) — `73ffd51`
- feat(service): list installed service instances (#112) — `af2e962`
- feat(service): show structured status and failure journal (#111) — `e50bc50`
- feat(fs): configurable metadata pre-warm depth after mount (#107) — `0b9d252`
- feat(cli): add manual sync command (onecloudriver sync) (#100) — `ef33e3c`
- feat(fs): dirty inode tracking -> SerializeDirty (cut BoltDB writes >=99%) (#97) — `eee4079`
- feat(fs): serialize content snapshots against concurrent writes (#93) — `f26625b`
- feat(fs): optimistic concurrency (If-Match) on uploads with conflict preservation (#91) — `edbaf53`
- feat(graph): implement QuickXorHash and content-integrity verification (#32) (#90) — `e0b30ef`

### Fixed
- fix: propagate errors that were silently swallowed (#123) — `b70a3ef`
- fix: bind OAuth callbacks to the login with state and PKCE (#121) — `cd6e517`
- fix(service): use list-units to discover installed service instances (#113) — `fae2fd0`
- fix(fs): apply root-level delta changes (canonicalize the drive root ID) (#102) — `ad5bdc8`
- fix(fs): release in-flight slot on failed uploads; keep dirty flag on empty flush (#87) (#88) — `ee72d06`
- fix(mount): make --cache-dir a session-only override (#85) (#86) — `297b857`
- fix(fs): allow renaming locally-created items before upload (#83) (#84) — `d96b074`

### Changed
- refactor(graph): drop dead DefaultBaseURL trim in PollDelta deltaLink (#125) — `02a0ca6`
- refactor: extract shared helpers for duplicated patterns (#119) — `6df742d`
- perf(fs): bound content cache eviction selection cost (#110) — `69c23aa`
- refactor(cmd): extract account cache-deletion confirmation (#109) — `af00828`
- refactor(cmd): consolidate duplicated command boilerplate (#108) — `da8a910`
- refactor(auth): extract shared keyring key helpers (#103) — `6cfdf4f`
- perf(fs): streaming delta apply with bounded memory (issue #68) (#99) — `d2c42bd`
- perf(graph): tune HTTP transport connection pooling + WithTransport option (#70) (#96) — `9ac94f3`
- refactor(cmd): create graph.Client once in PersistentPreRun and share via context (#10) (#95) — `f45f6d1`
- perf(fs): zero-copy upload snapshot via on-disk file instead of ReadAll (#69) (#94) — `dafc33c`
- refactor(fs): consolidate eviction logic into EvictionController (#89) — `1fcb8bd`

### Other
- test: raise coverage of the least-covered modules (auth flow, service systemd, CLI commands, types) (#122) — `7f5d25a`
- test: improve coverage for cmd/onecloudriver (#106) — `620edbc`
- Replace global manager variable with dependency injection (issue #5) (#104) — `f900b83`
- test(graph): regression test for exact-multiple-of-chunk downloads (#98) — `6cf65f0`

---

## [0.1.3] - 2026-08-15

### Added
- feat(cli): add ASCII fallback for emoji output in non-TTY environments (#25) — `1a9aeab`

### Fixed
- fix(fs): harden InitBoltDB lock timeout with clear double-mount message (#75) (#77) — `2895b0f`
- fix(service) : Remove all services use the same procedure always (#62) — `3923f1b`
- fix(delta): preserve local changes when a remote delta arrives (#58) — `5708ca3`
- fix(scripts): ASCII fallback for emoji output in shell scripts (#57) — `0d0d752`
- fix(upload): keep transient-error upload sessions alive through outages (#56) — `ce4105d`
- fix(service): resolve binary robustly for service install ExecStart (#55) — `87cedd5`
- fix(ci): extract .rpm packaged unit with correct cpio pattern (#54) — `6394884`
- fix(packaging): fix %%i escaping in shipped systemd user unit and verify in CI (#52) — `f461bcd`
- fix(service): expand tilde in systemd unit mountpoint and create mountpoint dir on install (#51) — `2e099ac`
- fix(cmd): use effective cache dir when removing an account (#23) — `6faa569`
- fix(packaging): declare GPLv3 license in RPM spec (#21) — `ed1ebb5`
- fix(test): skip TestGetAuthCodeLocalServer_CannotBind in CI — `72160b7`
- fix(ci): increase graph package race timeout from 60s to 120s — `4d71ee3`
- fix(ci): use GITHUB_ACTIONS env var to detect CI (always set in GH Actions) — `0e61260`
- fix(ci): use os.Getenv CI check to skip slow server tests in GitHub Actions — `ac5b910`
- fix(ci): add -short to auth/graph race tests, skip slow server tests — `407317b`
- fix(test): increase server startup retry to 60 iterations (3s) for -race — `4eaea5b`
- fix(test): fix race in local server tests and use retry loops — `3854244`
- fix(test): use cancelled contexts instead of 127.0.0.1:1 for offline tests — `5e643b3`
- fix(ci): include all packages in coverage report + add Coveralls badge — `f342f02`

### Changed
- refactor(cli): centralize 'exactly one of' flag validation helpers (#9) (#59) — `27c2546`
- refactor(service): extract systemd logic to internal/service package (#34) — `2a0f2a0`
- refactor(cmd): extract buildResource helper for id/path resolution (#24) — `352df89`
- perf(test): remove redundant local-server test and reduce timeouts — `856d7b2`

### Documentation
- docs: correct offline-mode write behavior (write-back, not EROFS) (#61) — `95f2180`
- docs(man): document the service command in the man pages (#47) (#60) — `9349de1`
- docs: add AGENTS.md with project conventions for AI agents (#37) — `bdb1094`
- docs: expand emoji symbol table to the 14 printer symbols (#31) — `370c9a2`
- docs: document emoji rule (internal/printer) in CONTRIBUTING.md (#27) — `f458175`
- docs: add CONTRIBUTING.md and PR template (#19) — `b8828cd`

### Other
- ci(coverage): include FUSE integration tests in Coveralls report (#78) (#79) — `30a3b33`
- test(ci): add benchmark baseline, stress and crash-simulation jobs (#72) (#76) — `9aa368a`
- Update make doc command and upload docs (#63) — `fb2ad47`
- Issue 8 add yaml output support (#38) — `e2fa7c9`
- Update README — `92c0faf`
- #3 - extract account helper (#12) — `be2f8f8`
- test(cli): add binary-driven smoke tests for onecloudriver CLI — `024bd10`
- README new coverage badge — `e58873a`
- test(graph): add 15 edge-case tests to push graph coverage from 88.1% to 91.0% — `1338cf8`
- test(auth): add integration tests for AddAccount flow and fix flaky port test — `42813c6`
- test: add integration tests for graph, FUSE ops, and AddAccount (+2.1% coverage) — `dc623c7`
- test: add integration tests for healthCheck, CLI RunE validation, and coverage gaps — `a34374c`
- Launch integraation tool — `fd0aa96`
- Report integration — `d4d6174`
- Docomuent relase sign  artefacts configuration process — `d2969f1`
- Report code coverage — `3f570d4`

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

[0.1.5]: https://github.com/FROSADO/onecloudriver/releases/tag/v0.1.5
[0.1.4]: https://github.com/FROSADO/onecloudriver/releases/tag/v0.1.4
[0.1.3]: https://github.com/FROSADO/onecloudriver/releases/tag/v0.1.3
[0.1.2]: https://github.com/FROSADO/onecloudriver/releases/tag/v0.1.2
[0.1.1]: https://github.com/FROSADO/onecloudriver/releases/tag/v0.1.1
[0.1.0]: https://github.com/frosado/onecloudriver/releases/tag/v0.1.0
