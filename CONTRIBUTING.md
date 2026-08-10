# Contributing to onecloudriver

Thanks for your interest in contributing! This guide explains the development
setup and the contribution workflow used by the project.

> **Language:** all GitHub-facing output (issues, pull requests, comments,
> commits) must be written in **English**. Spanish documentation lives in the
> `docs/` folder (e.g. `docs/MANUAL.es.md`).

---

## Table of contents

- [Project overview](#project-overview)
- [Development setup](#development-setup)
- [Project layout](#project-layout)
- [Contribution workflow](#contribution-workflow)
- [Branch naming](#branch-naming)
- [Commit message convention](#commit-message-convention)
- [Branch protection](#branch-protection)
- [How the project board updates itself](#how-the-project-board-updates-itself)
- [Issue guidelines](#issue-guidelines)
- [Code style & quality](#code-style--quality)
- [Emoji rule](#emoji-rule)
- [Releasing](#releasing)
- [License](#license)

---

## Project overview

onecloudriver mounts your **OneDrive as a native FUSE filesystem on Linux**,
allowing you to read, write, create, and delete files directly from your file
manager and terminal. It is not a sync client: files are downloaded on demand
and cached locally (memory + BoltDB + disk), with bidirectional delta
synchronization and asynchronous uploads.

**Tech stack:** Go 1.25 · [go-fuse/v2](https://github.com/hanwen/go-fuse)
· Microsoft Graph API · [cobra](https://github.com/spf13/cobra)
· [BoltDB](https://github.com/etcd-io/bbolt) · zerolog.

## Development setup

Requirements:

- Go 1.25.12+ (see `go.mod`)
- Linux with FUSE (`fuse3`) for the integration tests
- `make` for the common tasks

Quick start:

```bash
make build          # build the onecloudriver binary
make test-unit      # unit/mock tests (verbose, filtered output)
make test-unit-short
make setup-fuse     # verify FUSE prerequisites (required for integration tests)
make test-integration   # integration tests (requires FUSE)
make test-all           # unit + integration
make lint-all           # golangci-lint (style + bugs + basic security)
make lint-security      # golangci-lint with .golangci-security.yml
make security-audit     # gosec + govulncheck + security lint + race → audit-report.txt
make coverage           # HTML coverage report
```

> ⚠️ **Never run unit and integration tests in parallel** — they can interfere
> (orphaned FUSE mounts, conflicting ports). Always `make test-unit` first,
> then `make test-integration` (see the `Makefile`).

## Project layout

| Path | Purpose |
|---|---|
| `cmd/onecloudriver/` | CLI commands (cobra) — `mount`, `list`, `upload`, ... |
| `internal/fs/` | FUSE filesystem, caches (InodeCache, ContentCache), delta sync, upload manager |
| `internal/graph/` | Microsoft Graph API client (items, upload, download, delta) |
| `internal/auth/` | Account management, OAuth flow, token storage (keyring) |
| `internal/types/` | Shared interfaces (e.g. `TokenProvider`) |
| `docs/` | Manuals, architecture, offline mode docs (EN + ES) |

## Contribution workflow

1. **Pick a task** from the project board:
   <https://github.com/users/FROSADO/projects/2>. Tasks are planned per
   **iteration** (2-week sprints) — start with the current iteration, and check
   the task's priority (P1 before P2) and dependencies. New to the project?
   Look for issues labeled `good first issue`.

2. **Create or update the issue** (in English) with a clear description,
   proposed changes and files involved. Add a label (`bug`, `enhancement`,
   `documentation`, `code-refactor`, ...).

3. **Create a branch** from `main` (see [Branch naming](#branch-naming)).

4. **Implement** the change with tests. Run the local checks:
   `make build && make test-unit && make lint-all`.

5. **Commit** with a Conventional Commit message (see
   [Commit message convention](#commit-message-convention)).

6. **Open a pull request** using the
   [PR template](.github/PULL_REQUEST_TEMPLATE.md). **Always link the issue
   with `Closes #N`** in the PR body — this auto-closes the issue on merge and
   updates the project board.

7. **Wait for CI.** The `protect-main` ruleset requires these checks to pass
   before merging: `Build`, `Tests (unit + race)`, `Tests (integration FUSE)`,
   `Lint`, `Security Audit`.

8. **Merge** (squash) once CI is green. No approving review is currently
   required (see [Branch protection](#branch-protection)). The issue closes
   automatically and the board item moves to **Done**.

## Branch naming

Create one branch per issue, following the convention:

```
issue-<N>-<short-slug>
```

Example: `issue-3-extract-account-helper`.

## Commit message convention

Use [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<optional scope>): <short summary>
```

| Type | Use for |
|---|---|
| `feat` | New feature or behavior |
| `fix` | Bug fix |
| `docs` | Documentation only |
| `refactor` | Code change without behavior change |
| `test` | Adding/improving tests |
| `perf` | Performance improvement |
| `ci` | CI/build configuration |

Examples from the history:

```
fix(test): skip TestGetAuthCodeLocalServer_CannotBind in CI
test(graph): add 15 edge-case tests to push graph coverage to 91.0%
docs: align offline documentation with the real behavior
```

## Branch protection

`main` is protected by the **`protect-main`** ruleset:

- Direct pushes to `main` are **blocked** — every change goes through a pull
  request (including hotfixes).
- **No approving review required** (the approval requirement was removed for
  now) — you can merge your own PR once CI is green.
- Required status checks: `Build`, `Tests (unit + race)`,
  `Tests (integration FUSE)`, `Lint`, `Security Audit`.
- **Strict policy**: the PR must be up to date with `main` before merging
  (merge/rebase `main` into the PR if it advanced).

## How the project board updates itself

The board (<https://github.com/users/FROSADO/projects/2>) is driven by native
GitHub Projects workflows and the issue links in PRs:

| Event | Board effect |
|---|---|
| Issue/PR added to the project | Auto-added (`Auto-add to project`) |
| PR opened | PR item → **In progress** (native workflow, when configured) |
| PR merged | Item → **Done** (`Pull request merged`) |
| Issue closed | Item → **Done** (`Item closed`) |
| PR with `Closes #N` merged | Issue auto-closed (`Auto-close issue`) + item → **Done** |

> 💡 To keep the board accurate, always put `Closes #N` (or `Related to #N`)
> in the PR body. A PR that does not reference its issue will not update the
> board automatically.

## Issue guidelines

- Write issues in **English**.
- Give each issue a clear title and a body with: **Problem** (current
  behavior, with file:line references when possible), **Goal**, **Proposed
  changes**, and **Files involved**.
- Use labels: `bug`, `enhancement`, `documentation`, `code-refactor`,
  `question`, `good first issue`, `help wanted`.
- Mention dependencies between issues (e.g. "Depends on #N").

## Code style & quality

- Run `gofmt`/`go vet` before committing; `golangci-lint` runs in CI and blocks
  PRs (`make lint-all`).
- Write **tests** for new behavior (table-driven where it fits). The `-race`
  detector runs in CI.
- Coverage is tracked via Coveralls — keep it from regressing.
- Security is taken seriously: `gosec`, `govulncheck` and the security linters
  run in CI (`make security-audit` locally).

## Emoji rule

**Never use literal emojis in Go code** for console or log output (`fmt.Printf`,
`fmt.Println`, `log.Printf`, `log.Println`, or any string that ends up on a
terminal or in the systemd journal). Since issue #7 (PR #25), all output must
use the `internal/printer` package, which selects the symbol based on the
environment: emoji when stdout is a terminal, ASCII fallback when output is
piped, redirected, captured by the systemd journal, or shown on a non-unicode
terminal / screen reader.

| Symbol | Emoji (TTY) | ASCII (non-TTY) |
|---|---|---|
| `printer.Rocket` | 🚀 | `[*]` |
| `printer.Folder` | 📁 | `[D]` |
| `printer.Clock` | ⏱️ | `[T]` |
| `printer.Refresh` | 🔄 | `[R]` |
| `printer.Disk` | 💾 | `[S]` |
| `printer.Unplug` | 🔌 | `[-]` |
| `printer.Success` | ✅ | `OK` |
| `printer.Warning` | ⚠️ | `WARN` |
| `printer.Info` | ℹ️ | `INFO` |

```go
// ❌ Wrong — literal emoji
fmt.Println("✅ Service installed")
log.Printf("⚠️ Could not connect")

// ✅ Correct — printer symbol
fmt.Println(printer.Success, "Service installed")
log.Printf("%s Could not connect", printer.Warning)
```

- Need a new symbol? Add it to `internal/printer/printer.go` (with its
  emoji/ASCII pair) instead of writing the emoji inline in the caller.
- Emojis in embedded HTML (e.g. the login page in `internal/auth/flow.go`)
  stay as literals — that is web content, not terminal output.
- Shell scripts (`scripts/*.sh`) are out of scope (they do not use
  `internal/printer`).

## Releasing

Releases are automated with `scripts/release.sh`:

```bash
make release          # interactive: pre-flight → version → CHANGELOG → tag → push
make release-check    # read-only pre-flight checklist
```

The release workflow builds artifacts, signs them and publishes them on GitHub
Releases (see `.github/workflows/release.yml`).

## License

[GPLv3](LICENSE)
