#!/usr/bin/env bash
# =============================================================================
# gen_perf_snapshot.sh — Genera el snapshot de rendimiento de una versión.
#
# Para cada versión se guardan dos ficheros versionados en bench/perf/:
#   coverage-<version>.out    perfil de cobertura de internal/fs (mismo comando
#                             que el job de CI, sin -tags integration)
#   benchmark-<version>.txt   salida de `go test -bench . -benchmem` de
#                             internal/fs (serialización #97 + evicción #66)
#
# Uso (desde la raíz del repo):
#   bench/gen_perf_snapshot.sh <version> [ruta_a_fuentes]
#
#   <version>       etiqueta única (p. ej. v0.1.4, 0.1.4-24-ge0678e5)
#   [ruta]          raíz de los fuentes a medir (por defecto: el repo actual).
#                   Para el baseline usa el worktree del tag: /tmp/ocr-v014
#
# Ejemplos:
#   bench/gen_perf_snapshot.sh v0.1.4 /tmp/ocr-v014          # baseline
#   bench/gen_perf_snapshot.sh 0.1.4-24-ge0678e5             # HEAD actual
#
# Los ficheros generados se commitean para poder comparar versiones futuras.
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
PERF_DIR="${SCRIPT_DIR}/perf"

if [ "$#" -lt 1 ]; then
    echo "usage: $0 <version> [ruta_a_fuentes]" >&2
    exit 2
fi
VERSION="$1"
SRC="${2:-$REPO_ROOT}"

[ -d "$SRC/internal/fs" ] || { echo "ERROR: no hay internal/fs en $SRC" >&2; exit 1; }
mkdir -p "$PERF_DIR"

COV_OUT="$PERF_DIR/coverage-$VERSION.out"
BENCH_OUT="$PERF_DIR/benchmark-$VERSION.txt"

echo ">> [$VERSION] cobertura de internal/fs -> $COV_OUT"
(
    cd "$SRC"
    go test -count=1 -coverprofile="$COV_OUT" ./internal/fs/...
)
go tool cover -func="$COV_OUT" | tail -1

echo ">> [$VERSION] benchmarks de internal/fs -> $BENCH_OUT"
# -benchtime=200x (no el defecto de 1s): con benchtime=1s, el benchmark
# BenchmarkSizeEviction_50k crece el heap sin límite (la iteración 0 evicta y
# baja cachedFolders, dejando de evictar pero empujando entradas) y se vuelve
# GC-bound (hang). 200x es comparable entre versiones y termina en segundos.
(
    cd "$SRC"
    go test -run '^$' -bench . -benchmem -benchtime=200x -count=1 ./internal/fs
) | tee "$BENCH_OUT" | grep -E "^(Benchmark|ok|FAIL)" || true

echo ">> [$VERSION] snapshot completo:"
ls -la "$COV_OUT" "$BENCH_OUT"
echo ">> recuerda: git add $COV_OUT $BENCH_OUT"