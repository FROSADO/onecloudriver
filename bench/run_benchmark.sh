#!/usr/bin/env bash
#
# run_benchmark.sh — Reproducible performance comparison of onecloudriver.
#
# Compares the current source binary against the official v0.1.4 baseline
# across a deterministic battery of binary-local operations (startup, help,
# account resolution, completion generation). No source is modified; the
# script only compiles and measures.
#
# Usage:
#   ./run_benchmark.sh [--baseline|--current|--compare]
#
#   --baseline  run the battery against the baseline binary  → results_baseline.json
#   --current   run the battery against the current binary   → results_current.json
#   --compare   run both, then emit the report (default)
#
# Env overrides:
#   BENCH_ITERATIONS      iterations per test (default 10, min 5)
#   BENCH_IMPROVE_THRESHOLD  % delta for a MEJORA verdict (default 5)
#   OCR_BASELINE_BIN      override baseline binary path
#   OCR_CURRENT_BIN       override current binary path
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$HERE/bin"

BASELINE_BIN="${OCR_BASELINE_BIN:-$BIN_DIR/onecloudriver-v014}"
CURRENT_BIN="${OCR_CURRENT_BIN:-$BIN_DIR/onecloudriver-current}"

ITERATIONS="${BENCH_ITERATIONS:-10}"
if (( ITERATIONS < 5 )); then ITERATIONS=5; fi
IMPROVE_THRESHOLD_PCT="${BENCH_IMPROVE_THRESHOLD:-5}"

WORK="$HERE/.run"
PYLIB="$HERE/lib_report.py"

NAMES="startup_version startup_help account_list mount_help completion_bash"

# measure_one: run a command once, print "wall_ns rss_kb rc".
measure_one() {
  local bin="$1"; shift
  local t0 t1 wall_ns rss_kb rc
  t0=$(date +%s%N)
  "$bin" "$@" >/dev/null 2>&1
  rc=$?
  t1=$(date +%s%N)
  wall_ns=$((t1 - t0))
  rss_kb=$(/usr/bin/time -v "$bin" "$@" >/dev/null 2>"$WORK/rss.$$.txt" \
           && grep -m1 "Maximum resident" "$WORK/rss.$$.txt" | awk '{print $NF}')
  rm -f "$WORK/rss.$$.txt"
  printf "%d %d %d\n" "$wall_ns" "$rss_kb" "$rc"
}

# run_battery: sample every command against a binary, write one JSON file.
#   $1 bin   $2 out json   $3 label
run_battery() {
  local bin="$1" out="$2" label="$3"
  if [[ ! -x "$bin" ]]; then
    echo "FALLO: binario no encontrado o no ejecutable: $bin"
    return 1
  fi
  echo ">>> Batería vs ${label}: ${ITERATIONS} iteraciones por prueba (bin=$bin)"
  local name outfile
  for name in $NAMES; do
    case "$name" in
      startup_version)     args=(--version) ;;
      startup_help)        args=(--help) ;;
      account_list)        args=(account list) ;;
      mount_help)          args=(mount --help) ;;
      completion_bash)     args=(completion bash) ;;
    esac
    outfile="$WORK/samples_${name}.txt"
    : > "$outfile"
    for _ in $(seq 1 "$ITERATIONS"); do
      measure_one "$bin" "${args[@]}" >> "$outfile"
    done
    echo "    ${name}: ${ITERATIONS} muestras"
  done
  python3 "$PYLIB" emit-json-names "$WORK" "$out" "$label" "$bin" "$ITERATIONS" "$NAMES"
}

mode="${1:---compare}"
mkdir -p "$WORK"

if [[ "$mode" == "--baseline" ]]; then
  run_battery "$BASELINE_BIN" "$HERE/results_baseline.json" "baseline v0.1.4" || exit 1
elif [[ "$mode" == "--current" ]]; then
  run_battery "$CURRENT_BIN" "$HERE/results_current.json" "current" || exit 1
elif [[ "$mode" == "--compare" ]]; then
  run_battery "$BASELINE_BIN" "$HERE/results_baseline.json" "baseline v0.1.4" || exit 1
  run_battery "$CURRENT_BIN" "$HERE/results_current.json" "current" || exit 1
  echo ">>> Analizando y generando informe..."
  python3 "$PYLIB" report "$HERE/results_baseline.json" "$HERE/results_current.json" \
         "$HERE/../docs/PERFORMANCE_REPORT.md" "$ITERATIONS" "$IMPROVE_THRESHOLD_PCT"
else
  echo "Uso: $0 [--baseline|--current|--compare]" >&2
  exit 2
fi
rm -rf "$WORK"
echo "done."