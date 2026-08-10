# Agent instructions for onecloudriver

This file gives coding agents the essential project conventions. **Read
`CONTRIBUTING.md` first** — it is the source of truth (workflow, branch
protection, project board, release process).

## Non-negotiables

1. **No change without an issue.** Every commit, branch or PR must be backed
   by a GitHub issue that describes the reason for the change. If no issue
   exists, **create it first** (in English, with `Problem` / `Goal` /
   `Proposed changes` / `Files involved` sections and a label). A commit or
   PR without an issue is a process error.
2. **English for all GitHub-facing output**: issues, PRs, comments, commit
   messages, release descriptions.
3. **Branch naming**: `issue-<N>-<short-slug>`, always created from `main`.
   Example: `issue-3-extract-account-helper`.
4. **Commits**: [Conventional Commits](https://www.conventionalcommits.org/)
   (`feat`, `fix`, `docs`, `refactor`, `test`, `perf`, `ci`), short English
   summary in imperative mood.
5. **PRs**: use `.github/PULL_REQUEST_TEMPLATE.md` and **always put
   `Closes #N`** (or `Related to #N`) in the body — this auto-closes the
   issue and moves the project-board item to Done on merge. Wait for the 5
   required CI checks; merge with squash (no review currently required).
6. **Emoji rule**: never use literal emojis in Go console/log output
   (`fmt.*`, `log.*`) — use the `internal/printer` package symbols (emoji on
   TTY, ASCII fallback otherwise). New symbols are added to
   `internal/printer/printer.go`, never inline. See `CONTRIBUTING.md` →
   `## Emoji rule`.

## Local verification (before committing)

```bash
make build          # compile the binary
make test-unit      # unit/mock tests with -race
make lint-all       # golangci-lint (style + bugs + basic security)
```

> ⚠️ Never run unit and integration tests in parallel — they interfere
> (orphaned FUSE mounts, conflicting ports). `make test-unit` first, then
> `make test-integration` if the change touches FUSE.

For a small, localized change:

```bash
go build ./... && go vet ./<package>/... && go test ./<package>/... -count=1 -short
```

## Project layout (quick map)

| Path | Purpose |
|---|---|
| `cmd/onecloudriver/` | CLI commands (cobra) — `mount`, `list`, `upload`, ... |
| `internal/fs/` | FUSE filesystem, caches, delta sync, upload manager |
| `internal/graph/` | Microsoft Graph API client (items, upload, download, delta) |
| `internal/auth/` | Account management, OAuth flow, token storage (keyring) |
| `internal/service/` | systemd service integration (extracted from the CLI) |
| `internal/printer/` | TTY-aware output symbols (emoji/ASCII) |
| `internal/i18n/` | (planned) locale detection and catalogs |
| `docs/` | Manuals and architecture docs (EN + ES) |

## Skills (optional, gitignored)

When present, these project-specific skills carry more detail:

- `onecloudriver-cli` — full reference of the CLI commands and flags.
- `onecloudriver-contributing` — the contribution conventions above,
  including the emoji symbol table.

## License

GPLv3 (see `LICENSE`).
