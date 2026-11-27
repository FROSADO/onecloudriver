#!/usr/bin/env bash
# Reproducible FUSE cache benchmark for onecloudriver (issue #66 cache work).
#
# Mounts a given binary at the real production mountpoint of a test account
# (ACCOUNT, see below) using an ISOLATED --cache-dir (never touches the
# persisted production cache), runs the deterministic cache battery
# (cold_read / warm_read / write_readback / metadata / mixed) collecting
# per-iteration client wall-time and daemon CPU/RSS/IO, then unmounts.
#
# Usage:
#   ./run_fuse.sh baseline            # measure installed/official 0.1.4
#   ./run_fuse.sh current             # measure the repo's current binary
#   ./run_fuse.sh compare             # run both and generate the report
#   ./run_fuse.sh pressure            # run ONLY the pressure_evict battery (both)
#   ./run_fuse.sh report              # only re-render docs/PERFORMANCE_REPORT.md
#
# Config (override by env):
#   BIN_BASELINE         baseline binary path (default: ./bin/onecloudriver-v014)
#   BIN_CURRENT          current  binary path (default: ./bin/onecloudriver-current)
#   ACCOUNT              test account (REQUIRED: env var, or interactive prompt;
#                        the repository ships no account)
#   MOUNTPOINT           production mountpoint (default: ~/OneDrive/$ACCOUNT)
#   CACHE_BASE/CACHE_CUR isolated caches for baseline/current runs
#   ITERS                iterations per test (default: 100)
#   COLD_COUNT           distinct files for cold read (default: 100)
#   CACHE_MAX_ENTRIES    >0 forces size-eviction pressure (pressure_evict) (default: 0)
#   PRESS_ITERS          iterations of the Graph-mkdir churn (default: 40)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib_account.sh
. "$SCRIPT_DIR/lib_account.sh"
BIN_BASELINE="${BIN_BASELINE:-$SCRIPT_DIR/bin/onecloudriver-v014}"
BIN_CURRENT="${BIN_CURRENT:-$SCRIPT_DIR/bin/onecloudriver-current}"
ACCOUNT="${ACCOUNT:-}"
MOUNTPOINT="${MOUNTPOINT:-}"
CACHE_BASE="${CACHE_BASE:-/tmp/ocr-bench/cache-base}"
CACHE_CUR="${CACHE_CUR:-/tmp/ocr-bench/cache-cur}"
ITERS="${ITERS:-100}"
COLD_COUNT="${COLD_COUNT:-100}"
CACHE_MAX_ENTRIES="${CACHE_MAX_ENTRIES:-0}"   # 0 = persisted/default; >0 forces size eviction
PRESS_ITERS="${PRESS_ITERS:-40}"              # iterations of the Graph-mkdir churn
RESULTS_DIR="$SCRIPT_DIR/results"

mkdir -p "$RESULTS_DIR"

# Resolve the account lazily: `report` only re-renders existing JSON and needs
# no credentials.
require_account() {
    if [ -z "$ACCOUNT" ]; then
        ACCOUNT="$(ocr_require_account "$BIN_CURRENT")" || exit 2
    fi
    MOUNTPOINT="${MOUNTPOINT:-$HOME/OneDrive/$ACCOUNT}"
}

run_one() {
    local binary="$1" cache="$2" out="$3"
    [ -x "$binary" ] || { echo "ERROR: binary not found: $binary" >&2; exit 1; }
    echo ">> [$out] mounting $binary on $MOUNTPOINT ($ACCOUNT)"
    python3 "$SCRIPT_DIR/fuse_bench.py" \
        --binary "$binary" \
        --account "$ACCOUNT" \
        --mount "$MOUNTPOINT" \
        --cache-dir "$cache" \
        --iters "$ITERS" \
        --cold-count "$COLD_COUNT" \
        --cache-max-entries "$CACHE_MAX_ENTRIES" \
        --press-iters "$PRESS_ITERS" \
        --out "$out"
    echo ">> [$out] done -> $out"
}

run_pressure() {
    # run only the pressure_evict battery into results/<name>-pressure.json
    local binary="$1" cache="$2" out="$3"
    [ -x "$binary" ] || { echo "ERROR: binary not found: $binary" >&2; exit 1; }
    echo ">> [$out] pressure battery ($binary)"
    python3 "$SCRIPT_DIR/fuse_bench.py" \
        --binary "$binary" --account "$ACCOUNT" --mount "$MOUNTPOINT" --cache-dir "$cache" \
        --iters 1 --cold-count 1 --cache-max-entries "$CACHE_MAX_ENTRIES" \
        --press-iters "$PRESS_ITERS" --only pressure_evict --out "$out"
    echo ">> [$out] done -> $out"
}

case "${1:-compare}" in
    baseline)
        require_account
        run_one "$BIN_BASELINE" "$CACHE_BASE" "$RESULTS_DIR/baseline.json"
        ;;
    current)
        require_account
        run_one "$BIN_CURRENT" "$CACHE_CUR" "$RESULTS_DIR/current.json"
        ;;
    compare)
        require_account
        echo "=== BASELINE ($BIN_BASELINE) ==="
        run_one "$BIN_BASELINE" "$CACHE_BASE" "$RESULTS_DIR/baseline.json"
        echo
        echo "=== CURRENT ($BIN_CURRENT) ==="
        run_one "$BIN_CURRENT" "$CACHE_CUR" "$RESULTS_DIR/current.json"
        echo
        echo "=== REPORT ==="
        python3 "$SCRIPT_DIR/fuse_report.py" --iters "$ITERS" --cold-count "$COLD_COUNT"
        ;;
    pressure)
        require_account
        echo "=== PRESSURE BASELINE ==="
        run_pressure "$BIN_BASELINE" "${CACHE_BASE}-press" "$RESULTS_DIR/baseline-pressure.json"
        echo
        echo "=== PRESSURE CURRENT ==="
        run_pressure "$BIN_CURRENT" "${CACHE_CUR}-press" "$RESULTS_DIR/current-pressure.json"
        echo
        echo "=== REPORT ==="
        python3 "$SCRIPT_DIR/fuse_report.py" --iters "$ITERS" --cold-count "$COLD_COUNT"
        ;;
    report)
        python3 "$SCRIPT_DIR/fuse_report.py" --iters "$ITERS" --cold-count "$COLD_COUNT"
        ;;
    *)
        echo "usage: $0 {baseline|current|compare|pressure|report}" >&2
        exit 2
        ;;
esac