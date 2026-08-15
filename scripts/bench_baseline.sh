#!/usr/bin/env bash
# bench_baseline.sh — benchmark suite with a reproducible baseline.
#
# Runs the Go benchmark suite (the Benchmark* functions in internal/fs),
# stores the results as a flat JSON artifact and compares future runs
# against it, failing when a metric regresses beyond a threshold.
#
# Usage:
#   scripts/bench_baseline.sh --save              # run suite, store .bench/baseline.json
#   scripts/bench_baseline.sh --compare           # run suite, fail on regression > threshold
#   scripts/bench_baseline.sh --compare --threshold 0.25 --bench-time 3s
#
# Metrics: for ns/op, B/op and allocs/op lower is better; for MB/s higher is
# better. A metric that has no baseline entry (new benchmark) is not a
# regression — it is simply reported.
#
# Env:
#   BENCH_PKG       package list to benchmark      (default: ./internal/fs/...)
#   BENCH_REGEX     -bench regex                   (default: .)
#   BENCH_TIME      -benchtime                     (default: 10s)
#   BASELINE_FILE   baseline path                  (default: .bench/baseline.json)
#   THRESHOLD       allowed relative regression    (default: 0.20 = 20%)
set -euo pipefail

BENCH_PKG="${BENCH_PKG:-./internal/fs/...}"
BENCH_REGEX="${BENCH_REGEX:-.}"
BENCH_TIME="${BENCH_TIME:-10s}"
BASELINE_FILE="${BASELINE_FILE:-.bench/baseline.json}"
THRESHOLD="${THRESHOLD:-0.20}"

ACTION=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --save) ACTION="save" ;;
    --compare) ACTION="compare" ;;
    --threshold) THRESHOLD="$2"; shift ;;
    --bench-time) BENCH_TIME="$2"; shift ;;
    *)
      echo "error: unknown argument: $1" >&2
      echo "usage: $0 --save | --compare [--threshold F] [--bench-time DUR]" >&2
      exit 2
      ;;
  esac
  shift
done

if [[ -z "$ACTION" ]]; then
  echo "usage: $0 --save | --compare [--threshold F] [--bench-time DUR]" >&2
  exit 2
fi

RAW_FILE=""

# run_suite executes the benchmark suite, teeing the output to $RAW_FILE.
# Fails the script if `go test` itself errors.
run_suite() {
  RAW_FILE="$(mktemp)"
  local rc
  echo "==> go test -bench=\"$BENCH_REGEX\" -benchmem -benchtime=\"$BENCH_TIME\" -run='^$' $BENCH_PKG"
  set +e
  go test -bench="$BENCH_REGEX" -benchmem -benchtime="$BENCH_TIME" "$BENCH_PKG" -run='^$' 2>&1 | tee "$RAW_FILE"
  rc=${PIPESTATUS[0]}
  set -e
  if [[ $rc -ne 0 ]]; then
    echo "ERROR: benchmark run failed (exit $rc)" >&2
    rm -f "$RAW_FILE"
    exit 1
  fi
}

# parse_bench <file> → "name metric value" lines.
#   BenchmarkLookup-8 1000 500 ns/op 2 B/op 1 allocs/op
# becomes:
#   BenchmarkLookup ns_per_op 500
#   BenchmarkLookup bytes_per_op 2
#   BenchmarkLookup allocs_per_op 1
# (the -GOMAXPROCS suffix is stripped so results are comparable across hosts)
parse_bench() {
  grep -E '^Benchmark' "$1" | awk '{
    name=$1
    sub(/-[0-9]+$/, "", name)
    prev=""
    for (i=2; i<=NF; i++) {
      if      ($i == "ns/op")     printf "%s ns_per_op %s\n", name, prev
      else if ($i == "B/op")      printf "%s bytes_per_op %s\n", name, prev
      else if ($i == "allocs/op") printf "%s allocs_per_op %s\n", name, prev
      else if ($i == "MB/s")      printf "%s mb_per_s %s\n", name, prev
      prev=$i
    }
  }'
}

# save_baseline writes the current run as the new baseline (flat JSON).
save_baseline() {
  mkdir -p "$(dirname "$BASELINE_FILE")"
  {
    echo "{"
    echo "  \"date\": \"$(date -Is)\","
    echo "  \"benchtime\": \"$BENCH_TIME\","
    echo "  \"metrics\": {"
    local first=1
    while read -r name metric value; do
      if [[ $first -eq 1 ]]; then first=0; else echo ","; fi
      printf '    "%s.%s": %s' "$name" "$metric" "$value"
    done < <(parse_bench "$RAW_FILE")
    echo
    echo "  }"
    echo "}"
  } > "$BASELINE_FILE"
  local count
  count="$(parse_bench "$RAW_FILE" | wc -l | tr -d ' ')"
  echo "==> Baseline saved: $BASELINE_FILE ($count metrics)"
}

# baseline_value <key> → the stored numeric value, or empty.
baseline_value() {
  local key="$1"
  grep -F "\"$key\":" "$BASELINE_FILE" 2>/dev/null | grep -oE '[0-9.]+' | tail -1 || true
}

# compare_baseline runs the suite and fails when any metric regresses more
# than THRESHOLD relative to the stored baseline.
compare_baseline() {
  if [[ ! -f "$BASELINE_FILE" ]]; then
    echo "ERROR: no baseline at $BASELINE_FILE — run --save first" >&2
    exit 1
  fi

  local checked=0 regressions=0
  while read -r name metric value; do
    local key="$name.$metric"
    local base
    base="$(baseline_value "$key")"
    if [[ -z "$base" ]]; then
      echo "new:  $key = $value (no baseline entry)"
      continue
    fi
    checked=$((checked + 1))

    # mb_per_s: higher is better (regression when current < baseline).
    # others:   lower is better (regression when current > baseline).
    local verdict
    if [[ "$metric" == "mb_per_s" ]]; then
      if awk -v cur="$value" -v base="$base" -v t="$THRESHOLD" \
        'BEGIN { if (base <= 0) exit 1; exit !(((base - cur) / base) > t) }'; then
        verdict="REGRESSION"
      else
        verdict="ok"
      fi
    else
      if awk -v cur="$value" -v base="$base" -v t="$THRESHOLD" \
        'BEGIN { if (base <= 0) exit 1; exit !(((cur - base) / base) > t) }'; then
        verdict="REGRESSION"
      else
        verdict="ok"
      fi
    fi

    if [[ "$verdict" == "REGRESSION" ]]; then
      regressions=$((regressions + 1))
      echo "REGRESSION: $key = $value (baseline $base, threshold ${THRESHOLD})"
    else
      echo "ok:      $key = $value (baseline $base)"
    fi
  done < <(parse_bench "$RAW_FILE")

  echo
  echo "==> Compared $checked metrics; regressions: $regressions (threshold ${THRESHOLD})"
  if [[ $regressions -gt 0 ]]; then
    exit 1
  fi
}

run_suite
case "$ACTION" in
  save) save_baseline ;;
  compare) compare_baseline ;;
esac
rm -f "$RAW_FILE"
