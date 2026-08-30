# Benchmarking

Two reproducible benchmark batteries live under [`bench/`](../bench/). They
compare the **current build** against the official **v0.1.4** baseline. Neither
modifies source code — they only compile, mount, run and measure.

- **Binary-local battery** (`bench/run_benchmark.sh`): startup/CPU/RSS of the
  binary itself (no network). Deterministic, 100% reproducible.
- **FUSE cache battery** (`bench/run_fuse.sh` + `bench/fuse_bench.py`): the one
  that exercises the cache work of issue #66. Mounts the binary under test on a
  **real FUSE mount** of the secondary test account `paveryutu72@hotmail.com`
  with an **isolated session `--cache-dir`** (never the persisted production
  cache), runs a fixed cache workload, and samples client wall-time plus
  daemon-side CPU / RSS / I/O from `/proc/<pid>/{stat,status,io}` per iteration.

## Why a FUSE battery exists

The issue #66 optimization (time-bucketed TTL sweep + size min-heap in
`internal/fs/cache.go`) only runs inside the FUSE mount daemon. A binary-local
battery cannot observe it. Measuring on a real mount requires unmounting the
production mount of the test account (a second concurrent mount would clash
with the BoltDB double-mount lock), so `bench/fuse_bench.py` always:

1. force-unmounts any existing mount at the target mountpoint (even a stale
   `rawBridge` entry left by a dead daemon), then
2. mounts the binary under test with a fresh isolated `--cache-dir`, and
3. unmounts cleanly at the end.

## Dataset

`bench/seed_data.sh` builds the deterministic `QA-Bench` dataset inside the
account and **verifies it is fully committed server-side** with a fresh mount
before measuring:

| Asset | Size / count |
|---|---|
| `large.bin` | 8 MB |
| `med.bin` | 1 MB |
| `small.txt` | 1 KB |
| `coldfile_1..100` | 100 x 256 B (distinct, for cold reads) |
| `metadir/f1..f100.txt` | 100 entries (for metadata/listdir) |

It waits for the upload queue to drain (3 forced `sync` passes + graceful
unmount), because killing the seeding daemon too early leaves uncommitted
uploads and corrupts the dataset (observed during development).

## Cache workload (FUSE battery)

| Test | What it measures | Cache state |
|---|---|---|
| `cold_read` | `cat` of each of 100 distinct files (one fetch each) | cold |
| `warm_read` | repeated `cat` of the same file | warm |
| `write_readback` | write a fresh file then read it back | mixed |
| `metadata` | `stat` + `ls` of a populated directory | warm/cold |
| `mixed` | stat + read + listdir + touch + unlink | mixed |
| `pressure_evict` | churn of fresh folders with a low `--cache-max-entries`: forces the **size-eviction** path that issue #66 optimizes | pressure |

### Pressure battery (`pressure_evict`)

Run with `CACHE_MAX_ENTRIES > 0` (e.g. 10) it creates `PRESS_ITERS` fresh
folders (each `mkdir` is a synchronous Graph call, ~1.3 s). Wall-time is NOT
the signal (network-dominated): the daemon **global CPU and RSS** sampled
around the whole churn (which also captures the background TTL/size sweep) is.
Deltas below 100 ms of CPU are reported NEUTRO: that is the sampler's noise
floor (10 ms ticks) plus Graph latency.

```bash
CACHE_MAX_ENTRIES=10 PRESS_ITERS=40 ./run_fuse.sh pressure
```

Measured result (40 folders, `--cache-max-entries 10`):

| Metric | Baseline 0.1.4 | Current | Δ % |
|---|---:|---:|---:|
| Response (median wall/iter) | 1457 ms | 1563 ms | −7.2 (NEUTRO, red) |
| Daemon CPU (whole churn) | 560 ms | 530 ms | +5.4 (NEUTRO, <100 ms) |
| Daemon peak RSS | 22 392 KB | 24 448 KB | −9.2 (NEUTRO, muestra única) |

At this scale (~40 folders) the CPU/RSS deltas are inside the measurement
noise; the #66 win is only measurable at scale (tens of thousands of inodes),
where the in-process Go benchmarks in `internal/fs/cache_bench_test.go` prove
it (bucket sweep ~0.39 ms vs 7.5 ms full scan per tick; size heap ~72 ns/op
vs sorting all candidates). Warming tens of thousands of inodes through the
OneDrive Graph is not reproducible (hours of network).

Per iteration: client response time (ns timers) and daemon CPU ticks, max RSS
and char I/O deltas. 100 iterations per test by default.

## Reproducing

```bash
cd bench
make bins              # build v0.1.4 + current into bench/bin
./seed_data.sh         # deterministic committed dataset (verified)
./run_fuse.sh compare  # baseline + current + report
# or: ./run_fuse.sh baseline | current | report
```

Outputs:

- `bench/results/baseline.json`, `bench/results/current.json` — raw samples.
- `bench/results/summary.json` — computed statistics and verdicts.
- `docs/PERFORMANCE_REPORT.md` — the GitHub-ready report.

Make targets: `fuse-baseline`, `fuse-current`, `fuse-compare`, `fuse-report`.

## Analysis rules (QA spec)

- `delta_pct = (baseline_median - current_median) / baseline_median * 100`
  for lower-is-better metrics (time, CPU, memory, I/O bytes).
- Verdict thresholds: **MEJORA NOTABLE** ≥ 10 %, **MEJORA ACEPTABLE** ≥ 5 %,
  **NEUTRO** < 5 %, **DEGRADACIÓN** only for a consistent ≥ 5 % worsening of
  median and p95, **NO CONCLUYENTE** when variance is too high to decide.
- Statistics are computed with a script (`bench/fuse_report.py`), never by
  mental estimation. The top 1 % of samples per test are trimmed so a single
  network spike cannot dominate p95.
- Response time must be equivalent or better; a CPU/memory improvement is not
  reported as global success if the user-visible response regresses.

## Limitations

- FUSE reads are network-bound (OneDrive Graph): `cold_read` and occasional
  spikes in `metadata`/`mixed` are dominated by network latency, not the
  `InodeCache`. Tests with high variance are reported as NO CONCLUYENTE.
- The #66 wins only appear under cache **pressure** (tens of thousands of
  inodes with eviction). The default battery (~200 entries, default
  `--cache-max-*`) never forces eviction, so the hot path is identical between
  both binaries and dominated by FUSE+Graph cost.
- The battery requires the secondary account `paveryutu72@hotmail.com`
  authenticated and FUSE mount permissions; it never touches the primary
  account or the persisted production cache.
