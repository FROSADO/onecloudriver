#!/usr/bin/env bash
# crash_sim.sh — crash-simulation for the BoltDB persistence layer.
#
# Default mode (CI-safe, no FUSE, no account): runs the Go crash-simulation
# tests (internal/fs/crash_sim_test.go). Those spawn child test processes
# that write inodes to BoltDB and are killed at controlled points:
#   - abrupt exit without Close (equivalent to `kill -9` / power loss), and
#   - SIGKILL while a transaction is being committed.
# Afterwards BoltDB must reopen cleanly (no "invalid meta page" / "freelist
# corrupt") and every fully-committed transaction must have survived
# (zero data loss).
#
# Optional --fuse mode (local machines only): performs the real
# mount → write → kill -9 → remount → verify loop with a real OneDrive
# account and the onecloudriver binary. Requires FUSE and credentials, so it
# is NOT used in CI — it is the documented local fallback.
#
# Usage:
#   scripts/crash_sim.sh                     # BoltDB-level (CI-safe)
#   scripts/crash_sim.sh --fuse              # full FUSE loop (local, real account)
#
# Env:
#   CRASH_ITERS  iterations (CI: 10, local: 100)
#   CRASH_PKG    package to test            (default: ./internal/fs/...)
#   ONECLOUDRIVER_ACCOUNT  account for --fuse mode (required there)
set -euo pipefail

CRASH_ITERS="${CRASH_ITERS:-10}"
CRASH_PKG="${CRASH_PKG:-./internal/fs/...}"

MODE="boltdb"
if [[ "${1:-}" == "--fuse" ]]; then
  MODE="fuse"
fi

echo "==> Crash simulation ($MODE), iterations: $CRASH_ITERS"

if [[ "$MODE" == "boltdb" ]]; then
  echo "==> go test -run 'TestCrashSimulation' -count=1 -timeout=10m $CRASH_PKG"
  CRASH_ITERS="$CRASH_ITERS" go test -run 'TestCrashSimulation' -count=1 -timeout=10m "$CRASH_PKG"
  echo "==> PASS: 0 BoltDB errors, 0 data loss over $CRASH_ITERS iterations"
  exit 0
fi

# ──── --fuse mode: real mount loop (local machines with an account) ────

ACCOUNT="${ONECLOUDRIVER_ACCOUNT:?set ONECLOUDRIVER_ACCOUNT for --fuse mode}"
command -v onecloudriver >/dev/null 2>&1 || {
  echo "ERROR: onecloudriver binary not on PATH — run 'make build' first" >&2
  exit 1
}
FUSERMOUNT="fusermount3"
if ! command -v "$FUSERMOUNT" >/dev/null 2>&1; then
  FUSERMOUNT="fusermount"
fi
command -v "$FUSERMOUNT" >/dev/null 2>&1 || {
  echo "ERROR: $FUSERMOUNT not found — FUSE required for --fuse mode" >&2
  exit 1
}

# verify_mount <mountpoint> <file> — the file must exist and be readable
# after remount (metadata was persisted to BoltDB before the crash).
verify_mount() {
  local mnt="$1" file="$2"
  for _ in $(seq 1 50); do
    if stat "$mnt/$file" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.2
  done
  return 1
}

failures=0
for i in $(seq 1 "$CRASH_ITERS"); do
  mnt="$(mktemp -d)"
  cache="$(mktemp -d)"
  echo "-- iteration $i/$CRASH_ITERS"

  # 1. Mount in background
  onecloudriver mount "$mnt" -a "$ACCOUNT" --cache-dir "$cache" >/dev/null 2>&1 &
  pid=$!

  # 2. Wait for the mount to respond
  ready=0
  for _ in $(seq 1 50); do
    if stat "$mnt" >/dev/null 2>&1; then ready=1; break; fi
    sleep 0.2
  done
  if [[ $ready -ne 1 ]]; then
    echo "FAIL: mount did not become ready (iteration $i)" >&2
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    "$FUSERMOUNT" -uz "$mnt" 2>/dev/null || true
    rmdir "$mnt" "$cache" 2>/dev/null || true
    failures=$((failures + 1))
    continue
  fi

  # 3. Create files (forces metadata + content writes)
  touch "$mnt/crashfile-$i"
  sleep 1

  # 4. kill -9 (no cleanup, no graceful unmount)
  kill -9 "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true

  # 5. Clean the stale mount before remounting
  "$FUSERMOUNT" -uz "$mnt" 2>/dev/null || true
  sleep 0.5

  # 6. Remount and verify the file survived (BoltDB round-trip)
  onecloudriver mount "$mnt" -a "$ACCOUNT" --cache-dir "$cache" >/dev/null 2>&1 &
  pid=$!
  if verify_mount "$mnt" "crashfile-$i"; then
    echo "ok: crashfile-$i survived (iteration $i)"
  else
    echo "FAIL: crashfile-$i lost after kill -9 (iteration $i)" >&2
    failures=$((failures + 1))
  fi
  kill "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
  "$FUSERMOUNT" -uz "$mnt" 2>/dev/null || true
  rmdir "$mnt" "$cache" 2>/dev/null || true
done

if [[ $failures -gt 0 ]]; then
  echo "==> FAIL: $failures/$CRASH_ITERS iterations lost data" >&2
  exit 1
fi
echo "==> PASS: 0 data loss over $CRASH_ITERS iterations"
