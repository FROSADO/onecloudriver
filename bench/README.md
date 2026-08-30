# Benchmark de rendimiento de onecloudriver

Batería reproducible que compara el binario actual con el baseline oficial
`v0.1.4`, en **dos niveles**: una batería binaria local (arranque/CPU/RSS) y
una **batería de caché FUSE en vivo** (la que mide los cambios de la issue #66).
Este directorio solo **compila y mide** — no modifica código fuente.

## Prerrequisitos

- `bash`, `date` (soporte `+%s%N`), GNU `/usr/bin/time` (`time -v`), `python3`.
- `go` para compilar los binarios.
- Para la batería FUSE: `fusermount3`, además de **la cuenta de prueba
  `paveryutu72@hotmail.com` autenticada** y permisos para montar/desmontar FUSE
  (puede pedir `sudo` o pertenecer al grupo `fuse`). Se usa la **cuenta secundaria
  `paveryutu72`**, nunca la principal.

## Uso

```bash
cd bench

# 1. Compilar ambos binarios en bench/bin (baseline v0.1.4 + current)
make bins

# 2. Correr la batería binaria local (arranque/CPU/RSS)
./run_benchmark.sh --compare            # baseline + current + informe
./run_benchmark.sh --baseline           # solo baseline → results_baseline.json
./run_benchmark.sh --current            # solo current    → results_current.json

# 3. Correr la batería FUSE de caché (la que mide la issue #66)
./run_fuse.sh compare                   # baseline + current + informe
./run_fuse.sh baseline                  # solo baseline  → results/baseline.json
./run_fuse.sh current                   # solo current   → results/current.json
./run_fuse.sh report                    # solo re-render del informe
```

El informe se escribe en `../docs/PERFORMANCE_REPORT.md`.

## Batería binaria local

Cinco operaciones **deterministas y locales** (sin red):

| Medida | Comando | Qué mide |
|---|---|---|
| `startup_version` | `--version` | arranque + salida |
| `startup_help` | `--help` | arranque + render cobra |
| `account_list` | `account list` | arranque + resolución de cuentas |
| `mount_help` | `mount --help` | arranque + parseo de flags (cobra) |
| `completion_bash` | `completion bash` | arranque + generación de completions |

Cada una se repite `N` veces (mín. 5; por defecto `BENCH_ITERATIONS=10`). Se mide
el **tiempo de pared** (`date +%s%N`) y el **pico de RSS** (`/usr/bin/time -v`).

## Batería FUSE de caché (`run_fuse.sh`)

Mide el acceso a datos a través de un **montaje FUSE real** de `paveryutu72`,
moviendo el **mount daemon** (el proceso que implementa la caché). Para no
contaminar el cache persistente de producción, cada ejecución monta el binario
con un **`--cache-dir` aislado de sesión** (`--cache-dir` nunca se persiste).

Dado que puede montarse **una sola instancia por cuenta/cache** (bloqueo de
doble-montaje de BoltDB), **se desmonta el montaje de producción de la cuenta
`paveryutu72` antes de medir** y se remonta con el binario bajo prueba. La
cuenta principal no se toca.

| Prueba | Qué mide | Estado de caché |
|---|---|---|
| `cold_read` | `cat` de cada archivo distinto (caché Danguard fría) | fría |
| `warm_read` | `cat` repetido del mismo archivo | caliente |
| `write_readback` | escribir archivo nuevo y leerlo de inmediato | mixto |
| `metadata` | `stat` + `ls` de un directorio | caliente/frío |
| `mixed` | secuencia stat + read + ls + touch + rm | mixto |
| `pressure_evict` | churn de carpetas nuevas con `--cache-max-entries` bajo: fuerza el camino de **expulsión por tamaño** de la issue #66 (viejo: scan + sort; nuevo: min-heap) | mixto (presión) |

### Batería de presión (`pressure_evict`)

Se ejecuta con `CACHE_MAX_ENTRIES > 0` (p. ej. `10`) y crea `PRESS_ITERS`
carpetas nuevas (cada `mkdir` es una llamada Graph síncrona ~1.3s). La señal
**no es el wall-time** (dominado por la red) sino la **CPU y RSS globales del
daemon** (muestra antes/después de todo el churn, capturando también el sweep
en background). Por la resolución del sampler (ticks de 10 ms) y el coste de
red, deltas de CPU < 100 ms se reportan NEUTRO (suelo de ruido).

```bash
CACHE_MAX_ENTRIES=10 PRESS_ITERS=40 ./run_fuse.sh pressure
```

Para cada test se recogen, por iteración: **tiempo real de respuesta** (timers
ns de Python) y **del lado del daemon** CPU, RSS pico e I/O
(`/proc/<pid>/{stat,status,io}`). Se indican iteraciones, `--cache-dir` y estado
de caché en los JSON.

> **Por qué locales vs FUSE:** las operaciones binarias puras (`--version`,
> `--help`, `completion`) son 100% reproducibles sin red y miden el coste de
> arranque. Las operaciones FUSE son las que tocan la caché (issue #66). La
> latencia de red de OneDrive hace las lecturas en frío no-deterministas; si la
> varianza impide concluir a 100 iteraciones, la prueba se marca
> **NO CONCLUYENTE** (regla 7 del spec de QA).

## Variables de entorno

- `BENCH_ITERATIONS` — nº de iteraciones por prueba (mín. 5, por defecto 10).
- `BENCH_IMPROVE_THRESHOLD` — % de delta de mediana para declarar MEJORA
  (por defecto 5). Si las desviaciones estándar de los dos rangos se solapan,
  la métrica se marca NEUTRO aunque el delta numérico exceda el umbral.
- `OCR_BASELINE_BIN` / `OCR_CURRENT_BIN` — rutas a binarios alternativos.
- `CACHE_MAX_ENTRIES` — fuerza la expulsión por tamaño (batería de presión).
- `PRESS_ITERS` — iteraciones del churn de carpetas de `pressure_evict` (por defecto 40).

## Análisis y veredicto

- Estadísticos por métrica: media, mediana, desviación estándar, p95, p99.
- `delta_pct = (baseline_med - current_med) / baseline_med * 100` (mediana).
- Veredicto por métrica: **MEJORA** si `delta_pct > umbral` **y** los rangos
  `media ± σ` no se solapan; **DEGRADACIÓN** si el delta es < −umbral sin
  solapamiento; en otro caso **NEUTRO**.
- Veredicto global: compuesto por mayoría (MEJORA/DEGRADACIÓN/NEUTRO).

## Ficheros

| Fichero | Contenido |
|---|---|
| `run_benchmark.sh` | runner de la batería binaria local |
| `lib_report.py` | estadísticas de la batería binaria local |
| `run_fuse.sh` | runner de la batería FUSE de caché |
| `fuse_bench.py` | monta el binario con `--cache-dir` aislado y recolecta muestras FUSE |
| `fuse_report.py` | estadísticas + informe de la batería FUSE |
| `bin/` | binarios compilados: `onecloudriver-v014`, `onecloudriver-current` |
| `results/` | `baseline.json`, `current.json` (crudos) + `summary.json` |
| `results_baseline.json` / `results_current.json` | batería binaria local |
| `../docs/PERFORMANCE_REPORT.md` | informe para GitHub |