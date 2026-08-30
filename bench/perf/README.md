# Snapshots de rendimiento por versión

Cada fichero de este directorio es un **snapshot versionado** (cobertura +
benchmarks de Go) de `internal/fs` para una versión concreta del binario. Se
commitean para poder **verificar en cambios futuros** si una modificación
mejora, degrada o no altera el rendimiento, comparando el snapshot del nuevo
commit con el de la versión anterior.

## Formato

| Fichero | Contenido |
|---|---|
| `coverage-<version>.out` | Perfil de cobertura: `go test -count=1 -coverprofile=... ./internal/fs/...` (mismo comando que el job de CI, sin `-tags integration`; el % total se obtiene con `go tool cover -func=... \| tail -1`) |
| `benchmark-<version>.txt` | Salida de `go test -run '^$' -bench . -benchmem -benchtime=200x -count=1 ./internal/fs` |

`<version>` es la etiqueta del commit/tag, p. ej. `v0.1.4` o
`0.1.4-24-ge0678e5` (`git describe --tags --always`).

> ⚠️ **`-benchtime=200x` es obligatorio** (no el defecto de 1s): con benchtime=1s,
> `BenchmarkSizeEviction_50k` crece el heap sin límite (la iteración 0 evicta y
> baja `cachedFolders`, dejando de evictar pero empujando entradas al heap) y se
> vuelve GC-bound, colgando minutos. Con 200x termina en segundos y los ns/op
> son comparables entre versiones.

## Generar

```bash
# baseline (desde el worktree del tag)
bench/gen_perf_snapshot.sh v0.1.4 /tmp/ocr-v014

# versión actual (HEAD del repo)
bench/gen_perf_snapshot.sh 0.1.4-24-ge0678e5

# o via Makefile
make perf-snapshot
```

`make perf-snapshot` genera el snapshot del HEAD actual y, si existe un tag
`vX.Y.Z` previo con su worktree en `/tmp/ocr-v<tag>`, también el del baseline
para comparar.

## Comparar versiones

```bash
# 1. cobertura: statements totales por versión
go tool cover -func=bench/perf/coverage-0.1.4-24-ge0678e5.out | tail -1
go tool cover -func=bench/perf/coverage-v0.1.4.out | tail -1

# 2. benchmarks solapados (misma métrica, ns/op)
grep -E "^(BenchmarkSerializeAll|BenchmarkSerializeDirty)" bench/perf/benchmark-*.txt

# 3. benchmarks de evicción #66 (solo existen desde la implementación)
grep -E "^(BenchmarkTTLSweep|BenchmarkSizeEviction|BenchmarkFullSweep)" bench/perf/benchmark-*.txt
```

Benchmarks de #66 a vigilar en futuros cambios (50k inodes, `benchtime=200x`):

| Benchmark | v0.1.4 | 0.1.4-24-ge0678e5 |
|---|---:|---:|
| `TTLSweepBuckets_50k` (tick) | — | ~0.36 ms |
| `TTLSweepFullScan_50k` (ref.) | — | ~9.3 ms |
| `SizeEviction_50k` | — | ~66 ns/op |
| `FullSweep_50k` (tick real) | — | ~0.38 ms |

> La batería FUSE end-to-end (`run_fuse.sh`) no puede aislar estos costes
> (la red de Graph los enmascara); el snapshot Go in-process es la métrica
> reproducible para comparar rendimiento entre versiones. Ver
> `docs/BENCHMARKS.md`.

## Regla de release

Al publicar una release (ver `CONTRIBUTING.md` → Releasing y el skill
`onecloudriver-contributing`), **genera el snapshot de la nueva versión** y
compáralo con el anterior antes de taggear: si un benchmark clave degrada
>10 %, la release debe justificar el motivo.
