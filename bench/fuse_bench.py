#!/usr/bin/env python3
"""
FUSE cache benchmark for onecloudriver.

Measures the user-visible response time and the daemon-side CPU / RSS / I/O
for a fixed battery of cache workloads, against a given binary mounted with an
ISOLATED --cache-dir (so the persisted production cache is never touched).

Workloads (all deterministic, same order/sizes for every binary):

  cold_read        read each of {cold_count} distinct small files once (cache miss)
  warm_read        read the same small file {iters} times (cache hit)
  write_readback   {iters} x (write a fresh file then read it back)
  metadata         {iters} x stat(file) + listdir(dir-with-entries)
  mixed            {iters} x (stat + read small + listdir + touch + unlink)

For every iteration the *client* wall time is recorded. The FUSE daemon is
sampled before/after each iteration through /proc/<pid>/{stat,status,io} to
derive CPU ticks, max RSS and char I/O deltas.

Usage:
  fuse_bench.py --binary PATH --mount MP --cache-dir DIR
                [--iters N] [--cold-count N] [--cache-max-entries N]
                [--only test1,test2] [--out results.json]
"""
import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import time

PAGE_SIZE = os.sysconf("SC_PAGE_SIZE")


def run(cmd, timeout=120):
    return subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)


def find_daemon_pid(mp=None):
    """Return the PID of the process holding a /dev/fuse fd.

    When `mp` is given, only the process whose cmdline contains that mountpoint
    is returned (there may be other production mounts holding /dev/fuse too).
    """
    for pid in os.listdir("/proc"):
        if not pid.isdigit():
            continue
        try:
            fds = os.listdir(f"/proc/{pid}/fd")
        except (PermissionError, FileNotFoundError, ProcessLookupError):
            continue
        for fd in fds:
            try:
                if os.readlink(f"/proc/{pid}/fd/{fd}") != "/dev/fuse":
                    continue
                if mp is None:
                    return int(pid)
                try:
                    with open(f"/proc/{pid}/cmdline", "rb") as f:
                        cl = f.read().decode(errors="replace")
                except OSError:
                    continue
                if mp in cl:
                    return int(pid)
            except (OSError, FileNotFoundError):
                continue
    return None


def read_proc_stat(pid):
    """Return (utime_ticks, stime_ticks) for the process."""
    try:
        with open(f"/proc/{pid}/stat") as f:
            # comm can contain spaces/parens; parse after the last ')'
            s = f.read()
            idx = s.rfind(")")
            fields = s[idx + 2:].split()
            # fields after ')' field 3 is state, field 14 utime, 15 stime
            utime = int(fields[11])
            stime = int(fields[12])
            return utime, stime
    except (OSError, IndexError, ValueError):
        return None


def read_proc_io(pid):
    """Return dict of /proc/<pid>/io counters (best effort)."""
    out = {}
    try:
        with open(f"/proc/{pid}/io") as f:
            for line in f:
                k, _, v = line.partition(":")
                out[k.strip()] = int(v.strip())
    except OSError:
        pass
    return out


def read_proc_rss(pid):
    """Return current RSS in KB from /proc/<pid>/status VmRSS."""
    try:
        with open(f"/proc/{pid}/status") as f:
            for line in f:
                if line.startswith("VmRSS:"):
                    return int(line.split()[1])
    except OSError:
        pass
    return 0


def mount(binary, mp, cache_dir, log_path, max_entries=0, mount_extra=None):
    shutil.rmtree(cache_dir, ignore_errors=True)
    os.makedirs(cache_dir, exist_ok=True)
    cmd = [
        binary, "mount", mp,
        "-a", "paveryutu72@hotmail.com",
        "--cache-dir", cache_dir,
        "--pre-warm-depth", "1",
    ]
    if max_entries and max_entries > 0:
        cmd += ["--cache-max-entries", str(max_entries)]
    if mount_extra:
        cmd += mount_extra
    with open(log_path, "w") as logf:
        # fully detached so it survives the parent python process exit
        p = subprocess.Popen(
            cmd, stdout=logf, stderr=subprocess.STDOUT,
            stdin=subprocess.DEVNULL, start_new_session=True,
        )
    # wait until /proc/mounts reports it
    deadline = time.time() + 40
    while time.time() < deadline:
        try:
            with open("/proc/mounts") as f:
                if any(mp in line for line in f):
                    return p.pid
        except OSError:
            pass
        time.sleep(0.5)
    raise RuntimeError(f"mount did not appear in /proc/mounts; log: {log_path}")


def unmount(mp):
    # fusermount3 first, fall back to lazy
    r = subprocess.run(["fusermount3", "-u", mp], capture_output=True, text=True)
    if r.returncode != 0:
        time.sleep(0.5)
        subprocess.run(["fusermount3", "-z", mp], capture_output=True, text=True)


def wait_daemon(pid):
    """Wait until daemon pid FD is ready (short sleep after mount detect)."""
    time.sleep(2)
    return pid


def sample(pid):
    if pid is None:
        return None
    return {"stat": read_proc_stat(pid), "io": read_proc_io(pid), "rss": read_proc_rss(pid)}


def diff_metrics(pre, post):
    """Return (cpu_ticks_delta, rchar_delta, wchar_delta, syscr_delta, syscw_delta, rss_kb)."""
    out = {}
    if pre is None or post is None or pre["stat"] is None or post["stat"] is None:
        return None
    cpu = (post["stat"][0] + post["stat"][1]) - (pre["stat"][0] + pre["stat"][1])
    out["cpu_ticks"] = max(0, cpu)
    out["rss_kb"] = post["rss"]
    io_p, io_c = pre["io"], post["io"]
    if io_p and io_c:
        for k in ("rchar", "wchar", "syscr", "syscw", "read_bytes", "write_bytes"):
            out[k] = max(0, io_c.get(k, 0) - io_p.get(k, 0))
    return out


def parse_stat_size(path):
    r = run(["stat", "-c", "%s", path], timeout=20)
    try:
        return int(r.stdout.strip())
    except ValueError:
        return -1


def bench(binary, mp, cache_dir, iters, cold_count, max_entries, press_iters, only, out_path, describe):
    # The real daemon is the process holding /dev/fuse whose cmdline references
    # our mountpoint. A plain cmdline match would also hit this python process
    # (its own args contain "--mount <mp>"), and a plain /dev/fuse match could
    # hit another production mount (e.g. the primary account).
    pid = find_daemon_pid(mp)
    if pid is None:
        sys.exit(f"ERROR: could not find FUSE daemon (no /dev/fuse holder for {mp})")

    results = {
        "binary": binary,
        "binary_version": "",
        "mountpoint": mp,
        "cache_dir": cache_dir,
        "iters": iters,
        "cold_count": cold_count,
        "daemon_pid": pid,
        "timestamp": time.strftime("%Y-%m-%dT%H:%M:%S%z"),
        "battery": [],
    }
    v = run([binary, "--version"], timeout=20)
    results["binary_version"] = v.stdout.strip() or v.stderr.strip()

    base = mp + "/QA-Bench"

    def want(name):
        return (not only) or (name in only)

    def run_op(cmd, timeout=180):
        t0 = time.perf_counter_ns()
        pre = sample(pid)
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout,
                           preexec_fn=os.setsid)
        t1 = time.perf_counter_ns()
        post = sample(pid)
        m = diff_metrics(pre, post)
        return {
            "wall_ns": t1 - t0,
            "rc": r.returncode,
            "stderr_tail": (r.stderr or r.stdout)[-200:],
            "daemon": m,
        }

    # ---- cold read: each distinct file read once (content-cache miss) ----
    if want("cold_read"):
        cold = []
        for i in range(1, cold_count + 1):
            p = f"{base}/coldfile_{i}"
            cold.append(run_op(["bash", "-c", f"cat {p} > /dev/null"]))
        results["battery"].append({"name": "cold_read", "samples": cold})

    # ---- warm read: same small file repeated (cache hit) ----
    sm = f"{base}/small.txt"
    if want("warm_read"):
        for _ in range(3):  # warm up
            run_op(["bash", "-c", f"cat {sm} > /dev/null"])
        warm = [run_op(["bash", "-c", f"cat {sm} > /dev/null"]) for _ in range(iters)]
        results["battery"].append({"name": "warm_read", "samples": warm})

    # ---- write/read-back: each iter writes a fresh file then reads it back ----
    if want("write_readback"):
        wr = []
        for i in range(iters):
            p = f"{base}/wrb_{i}.tmp"
            s = {"write": run_op(["bash", "-c", f"printf '%04096d' {i} > {p}"]),
                 "read": run_op(["bash", "-c", f"cat {p} > /dev/null"])}
            wr.append(s)
        results["battery"].append({"name": "write_readback", "samples": wr})

    # ---- metadata: stat(file) + listdir(dir) ----
    md_file = f"{base}/small.txt"
    md_dir = f"{base}/metadir"
    if want("metadata"):
        meta = []
        for i in range(iters):
            meta.append(run_op(["bash", "-c", f"stat -c '%s' {md_file} >/dev/null && ls -1 {md_dir} >/dev/null"]))
        results["battery"].append({"name": "metadata", "samples": meta})

    # ---- mixed: stat + read small + listdir + touch + unlink ----
    if want("mixed"):
        mixed = []
        for i in range(iters):
            t = f"{base}/mix_{i}.tmp"
            c = (f"stat -c '%s' {md_file} >/dev/null && cat {sm} > /dev/null && "
                 f"ls -1 {md_dir} >/dev/null && touch {t} && rm {t}")
            mixed.append(run_op(["bash", "-c", c]))
        results["battery"].append({"name": "mixed", "samples": mixed})

    # ---- pressure_evict: forced size-eviction churn (issue #66) ----
    # Creates `iters` folders (each with one file) under QA-Bench/press with a
    # bounded --cache-max-entries, forcing the size-eviction path (old:
    # collect+sort all candidates; new: persistent min-heap). Every mkdir is a
    # sync Graph create (~0.5-1.5s), so wall-time is network-bound and NOT the
    # signal; the daemon CPU (utime+stime) aggregated over the churn is what
    # isolates the eviction cost. Cache state: cold -> warm churn.
    if want("pressure_evict"):
        press = []
        # Unique per-run dir: leftover dirs from a killed run would make mkdir
        # fail with EEXIST and contaminate the measurement.
        press_root = f"{base}/press_{os.getpid()}"
        run_op(["bash", "-c", f"rm -rf {press_root}"])
        run_op(["bash", "-c", f"mkdir -p {press_root}"])
        # Global daemon sample around the WHOLE churn captures the background
        # TTL/size sweep ticks too (per-op sampling misses the async sweep).
        g_pre = sample(pid)
        for i in range(press_iters):
            d = f"{press_root}/d{i:04d}"
            s = {
                "mkdir": run_op(["bash", "-c", f"mkdir {d}"]),
                "write": run_op(["bash", "-c", f"printf 'x' > {d}/f"]),
                "stat": run_op(["bash", "-c", f"stat -c '%s' {d}/f > /dev/null"]),
            }
            press.append(s)
        g_post = sample(pid)
        # Delete through the Graph CLI (one request, mount-independent): rm -rf
        # via the FUSE mount would be N Graph deletes and block for minutes.
        # Runs detached; the dir is unique per run so a leftover cannot
        # contaminate a later run.
        subprocess.Popen([binary, "rm", "-a", "paveryutu72@hotmail.com",
                          "--path", f"/QA-Bench/press_{os.getpid()}", "-f"],
                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        results["battery"].append({"name": "pressure_evict", "samples": press,
                                   "max_entries": max_entries,
                                   "global": diff_metrics(g_pre, g_post)})

    # cleanup write test artifacts (only the .tmp we created)
    for i in range(iters):
        for p in (f"{base}/wrb_{i}.tmp", f"{base}/mix_{i}.tmp"):
            try:
                os.unlink(p)
            except OSError:
                pass

    with open(out_path, "w") as f:
        json.dump(results, f, indent=2)
    print(f"wrote {out_path} (daemon pid {pid}, version {results['binary_version']})")
    print(f"battery: {[b['name']+':'+str(len(b['samples'])) for b in results['battery']]}")
    return results


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--binary", required=True)
    ap.add_argument("--mount", required=True)
    ap.add_argument("--cache-dir", required=True)
    ap.add_argument("--iters", type=int, default=100)
    ap.add_argument("--cold-count", type=int, default=100)
    ap.add_argument("--cache-max-entries", type=int, default=0,
                    help="Max folders with cached children (0 = persisted/default); "
                         "used to force size-eviction pressure (issue #66)")
    ap.add_argument("--press-iters", type=int, default=40,
                    help="Iterations for the pressure_evict test (each is a sync "
                         "Graph mkdir+write ~2.5s, so keep it well below --iters)")
    ap.add_argument("--only", default="",
                    help="Comma-separated subset of tests to run (default: all)")
    ap.add_argument("--out", required=True)
    ap.add_argument("--log", default="/tmp/ocr-bench/mount.log")
    ap.add_argument("--no-unmount", action="store_true")
    a = ap.parse_args()

    os.makedirs("/tmp/ocr-bench", exist_ok=True)
    # ALWAYS start from a clean state: force-unmount any existing mount at the
    # target mountpoint (even a stale rawBridge entry left by a dead daemon),
    # then mount fresh with the requested isolated --cache-dir. Reusing a live
    # instance would measure a stale/warm cache and invalidate cold_read.
    if any(a.mount in l for l in open("/proc/mounts")):
        unmount(a.mount)
        time.sleep(1.0)
    mount(a.binary, a.mount, a.cache_dir, a.log, max_entries=a.cache_max_entries)
    desc = {"binary": a.binary, "mount": a.mount, "cache_dir": a.cache_dir}
    try:
        only = [t.strip() for t in a.only.split(",") if t.strip()] if a.only else []
        bench(a.binary, a.mount, a.cache_dir, a.iters, a.cold_count,
              a.cache_max_entries, a.press_iters, only, a.out, desc)
    finally:
        if not a.no_unmount:
            unmount(a.mount)


if __name__ == "__main__":
    main()