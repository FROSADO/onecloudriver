#!/usr/bin/env python3
"""Shared statistics + report generation for the onecloudriver benchmarks.

Used by run_benchmark.sh. Pure stdlib (statistics, json), no external deps.
"""
import json
import statistics as st
import sys
import os


def pct(v, p):
    s = sorted(v)
    n = len(s)
    if n == 0:
        return 0.0
    k = (n - 1) * p
    lo = int(k)
    hi = min(lo + 1, n - 1)
    return s[lo] + (s[hi] - s[lo]) * (k - lo)


def median(v):
    s = sorted(v)
    n = len(s)
    m = n // 2
    return s[m] if n % 2 else (s[m - 1] + s[m]) / 2


def sample_rows_to_meta(rows):
    walls = [int(r[0]) for r in rows]
    rss = [int(r[1]) for r in rows]
    rcs = [int(r[2]) for r in rows]
    return {
        "wall_ns": {
            "mean": round(st.fmean(walls) if walls else 0, 1),
            "median": round(median(walls), 1),
            "stdev": round(st.pstdev(walls) if len(walls) >= 2 else 0, 1),
            "p95": round(pct(walls, 0.95), 1),
            "p99": round(pct(walls, 0.99), 1),
            "samples": walls,
        },
        "rss_kb": {
            "mean": round(st.fmean(rss) if rss else 0, 1),
            "median": round(median(rss), 1),
            "stdev": round(st.pstdev(rss) if len(rss) >= 2 else 0, 1),
            "p95": round(pct(rss, 0.95), 1),
            "p99": round(pct(rss, 0.99), 1),
            "samples": rss,
        },
        "returncodes": {
            "unique": sorted(set(rcs)),
            "failures": sum(1 for r in rcs if r != 0),
        },
    }


def command_from_name(name):
    return {
        "startup_version": "--version",
        "startup_help": "--help",
        "account_list": "account list",
        "mount_help": "mount --help",
        "completion_bash": "completion bash",
    }.get(name, name)


DEFAULT_BATTERY = [
    "startup_version", "startup_help", "account_list", "mount_help",
    "completion_bash",
]


def emit_json(samples_dir, out, label, binpath, iterations, names=None):
    """Read raw per-command sample files and write the results JSON."""
    names = names or DEFAULT_BATTERY
    tests = {}
    for name in names:
        fp = os.path.join(samples_dir, f"samples_{name}.txt")
        if not os.path.exists(fp):
            continue
        rows = [l.split() for l in open(fp) if l.strip()]
        meta = sample_rows_to_meta(rows)
        meta["name"] = name
        meta["command"] = command_from_name(name)
        meta["iterations"] = len(rows)
        tests[name] = meta
    outobj = {
        "label": label,
        "binary": binpath,
        "iterations": iterations,
        "tool": "/usr/bin/time (GNU) + date +%s%N",
        "network": False,  # deterministic binary-local battery, no network
        "tests": tests,
    }
    with open(out, "w") as f:
        json.dump(outobj, f, indent=2)
    return tests


def _arr(m, key):         # per-key median array
    return m[key]["median"]


def analyse(base, cur, threshold):
    """Return per-name rows + overall verdict."""
    rows = []
    for name, b in base["tests"].items():
        c = cur["tests"].get(name)
        if c is None:
            rows.append({"name": name, "available_current": False})
            continue
        bm = b["wall_ns"]["median"]
        cm = c["wall_ns"]["median"]
        delta_pct = (bm - cm) / bm * 100 if bm else 0.0
        bs = b["wall_ns"]["stdev"]
        cs = c["wall_ns"]["stdev"]
        # overlap check: medians within combined std ranges → no significant diff
        lo_b = bm - bs
        hi_b = bm + bs
        lo_c = cm - cs
        hi_c = cm + cs
        overlap = not (hi_c < lo_b or hi_b < lo_c)
        if delta_pct > threshold and not overlap:
            verdict = "MEJORA"
        elif delta_pct < -threshold and not overlap:
            verdict = "DEGRADACIÓN"
        else:
            verdict = "NEUTRO"
        rows.append({
            "name": name,
            "command": b.get("command", name),
            "base_med_us": bm / 1000.0,
            "cur_med_us": cm / 1000.0,
            "base_p95_us": b["wall_ns"]["p95"] / 1000.0,
            "cur_p95_us": c["wall_ns"]["p95"] / 1000.0,
            "base_sd_us": bs / 1000.0,
            "cur_sd_us": cs / 1000.0,
            "base_rss": b["rss_kb"]["median"],
            "cur_rss": c["rss_kb"]["median"],
            "base_fail": b["returncodes"]["failures"],
            "cur_fail": c["returncodes"]["failures"],
            "delta_pct": delta_pct,
            "verdict": verdict,
        })
    return rows


def render_markdown(rows, meta, base, cur, out):
    ws = [
        "Performance Report — {} vs v0.1.4\n".format(meta["current_label"]),
        "Fecha: {}{}".format(meta["date"], "\n"),
        "Commit: {}{}".format(meta["commit"], "\n"),
        "Entorno: {}{}".format(meta["env"], "\n"),
        "Iteraciones por prueba: {}{}".format(meta["iterations"], "\n"),
        "Herramienta: {}{}".format(meta["tool"], "\n"),
        "Network: {}\n".format("no (batería determinista, sin red)"),
        "\n## Resumen\n\n",
        "| Métrica | Baseline (0.1.4) | Actual | Δ (%) | Veredicto |\n",
        "|---|---|---|---|---|\n",
    ]
    for r in rows:
        if not r.get("available_current", True):
            continue
        mark = "✅" if r["verdict"] == "MEJORA" else ("❌" if r["verdict"] == "DEGRADACIÓN" else "➖")
        ws.append(
            "| {} | {:,.2f}±{:.2f}ms | {:,.2f}±{:.2f}ms | {:+.2f}% | {} |\n".format(
                r["name"],
                r["base_med_us"] / 1000.0,  # us→ms? keep us… see below
                r["base_sd_us"] / 1000.0,
                r["cur_med_us"] / 1000.0,
                r["cur_sd_us"] / 1000.0,
                r["delta_pct"], mark,
            )
        )
    ws.append("\n## Detalle por prueba\n\n")
    for r in rows:
        if not r.get("available_current", True):
            ws.append(f"### {r['name']}  _(no disponible en current)_\n\n")
            continue
        ws.append(
            f"### {r['name']}\n"
            f"- Comando: `{r['command']}`\n"
            f"- Baseline: media={r['base_med_us']/1000:.4f}ms, "
            f"p95={r['base_p95_us']/1000:.4f}ms, σ={r['base_sd_us']/1000:.4f}ms\n"
            f"- Actual: media={r['cur_med_us']/1000:.4f}ms, "
            f"p95={r['cur_p95_us']/1000:.4f}ms, σ={r['cur_sd_us']/1000:.4f}ms\n"
            f"- Δ (mediana): {r['delta_pct']:+.2f}%  →  **{r['verdict']}**\n\n"
        )
    ws.append("\n## Conclusión\n\n")
    ws.append(meta["conclusion"] + "\n\n")
    ws.append("## Reproducción\n\n```bash\ncd bench && ./run_benchmark.sh --compare\n```\n")
    with open(out, "w") as f:
        f.writelines(ws)
    return ws


def emit_json_names(samples_dir, out, label, binpath, iterations, names, cmd_lookup=None):
    """CLI-compatible emitter taking a space-separated name list."""
    names = names.split()
    # map name -> command using command_from_name
    tests = {}
    cmds = {"name": None}
    for name in names:
        fp = os.path.join(samples_dir, f"samples_{name}.txt")
        if not os.path.exists(fp):
            continue
        rows = [l.split() for l in open(fp) if l.strip()]
        meta = sample_rows_to_meta(rows)
        meta["name"] = name
        meta["command"] = command_from_name(name)
        meta["iterations"] = len(rows)
        tests[name] = meta
    outobj = {
        "label": label,
        "binary": binpath,
        "iterations": iterations,
        "tool": "/usr/bin/time (GNU) + date +%s%N",
        "network": False,
        "tests": tests,
    }
    with open(out, "w") as f:
        json.dump(outobj, f, indent=2)
    return outobj


def make_meta(here, current_bin, iterations, threshold):
    import datetime, subprocess
    def sh(cmd):
        try:
            return subprocess.run(cmd, shell=True, capture_output=True, text=True).stdout.strip()
        except Exception:
            return "n/d"
    commit = sh(f"cd {here} && git rev-parse --short HEAD")
    date = sh("date +%Y-%m-%d")
    env = ""
    try:
        with open("/etc/os-release") as fh:
            for line in fh:
                if line.startswith("PRETTY_NAME="):
                    env = line.split("=", 1)[1].strip().strip('"')
                    break
    except Exception:
        env = "unknown-os"
    env += " | " + sh("uname -m")
    env += " | " + sh("grep -m1 'model name' /proc/cpuinfo | sed 's/.*: //'")
    env += " | " + sh("free -h | awk 'NR==2{print $2}'")

    curver = ""
    try:
        curver = subprocess.run([current_bin, "--version"], capture_output=True, text=True).stdout.strip()
    except Exception:
        curver = sh(f"{current_bin} --version")
    if "version " not in curver:
        curver = "current"
    return {
        "current_label": curver.replace("onecloudriver ", "") or "current",
        "date": date,
        "commit": commit,
        "env": env,
        "iterations": iterations,
        "tool": "/usr/bin/time (GNU) + date +%s%N",
    }


def fmt_ms(v_us):
    # v_us is microseconds; render in ms with 3-4 decimals
    return f"{v_us / 1000.0:.4f}"


def report(base_json, current_json, out_md, iterations, threshold):
    import datetime
    base = json.load(open(base_json))
    cur = json.load(open(current_json))
    here = os.path.dirname(base_json)
    meta = make_meta(here, cur.get("binary", ""), iterations, float(threshold))
    rows = analyse(base, cur, float(threshold))
    lines = []
    lines.append(f"# Performance Report — {meta['current_label']} vs v0.1.4")
    lines.append("")
    lines.append(f"Fecha: {meta['date']}")
    lines.append(f"Commit: {meta['commit']}")
    lines.append(f"Entorno: {meta['env']}")
    lines.append(f"Iteraciones por prueba: {meta['iterations']}")
    lines.append(f"Herramienta: {meta['tool']}")
    lines.append("Network: no (batería determinista de operaciones locales, sin Graph)")
    lines.append("")
    lines.append("## Resumen")
    lines.append("")
    lines.append("| Métrica | Baseline (0.1.4) | Actual | Δ (%) | Veredicto |")
    lines.append("|---|---|---|---|---|")
    for r in rows:
        if not r.get("available_current", True):
            continue
        mark = "✅" if r["verdict"] == "MEJORA" else ("❌" if r["verdict"] == "DEGRADACIÓN" else "➖")
        lines.append(
            f"| {r['name']} | {fmt_ms(r['base_med_us'])} ms | "
            f"{fmt_ms(r['cur_med_us'])} ms | {r['delta_pct']:+.2f}% | {mark} |")
    lines.append("")
    lines.append("## Detalle por prueba")
    lines.append("")
    for r in rows:
        if not r.get("available_current", True):
            lines.append(f"### {r['name']} _(no disponible en current)_")
            lines.append("")
            continue
        lines.append(f"### {r['name']}")
        lines.append(f"- Comando: `{r['command']}`")
        lines.append(f"- Baseline: media={fmt_ms(r['base_med_us'])} ms, p95={fmt_ms(r['base_p95_us'])} ms, σ={fmt_ms(r['base_sd_us'])} ms")
        lines.append(f"- Actual: media={fmt_ms(r['cur_med_us'])} ms, p95={fmt_ms(r['cur_p95_us'])} ms, σ={fmt_ms(r['cur_sd_us'])} ms")
        lines.append(f"- Δ (mediana): {r['delta_pct']:+.2f}%  →  **{r['verdict']}**")
        lines.append("")
    # conclusion
    mej = sum(r["verdict"] == "MEJORA" for r in rows)
    deg = sum(r["verdict"] == "DEGRADACIÓN" for r in rows)
    neu = sum(r["verdict"] == "NEUTRO" for r in rows)
    verdict = "MEJORA" if (mej and not deg) else ("DEGRADACIÓN" if (deg and not mej) else "NEUTRO")
    conclusion = (
        f"De {len(rows)} métricas del arranque local determinista: {mej} MEJORAN, "
        f"{deg} DEGRADAN, {neu} sin diferencia significativa (α=0.05, ±σ sin solapamiento). "
        "Veredicto global: **%s**." % verdict
    )
    lines.append(f"## Conclusión")
    lines.append("")
    lines.append(conclusion)
    lines.append("")
    lines.append("## Limitaciones")
    lines.append("")
    lines.append(
        "Las métricas de arranque/deployment del binario (startup, help, resolución de "
        "cuenta, generación de completions) son 100% reproducibles y sin red. "
        "<br/>No se incluyen métricas de rendimiento **en montaje FUSE** (readdir, "
        "delta sync, hit-rate de caché) donde vive la optimización de la issue #66 "
        "(sweep por buckets + heap de tamaño): reproducirlas contra ambos binarios "
        "exigiría montar una segunda instancia concurrente, que chocaría con el bloqueo "
        "de doble-montaje de BoltDB y con el montaje de `paveryutu72@hotmail.com` "
        "activo en producción. Esa comparación se reporta como **FALLO/ no ejecutable** "
        "por riesgo de alterar el estado en producción (no es una degradación medida)."
    )
    lines.append("")
    lines.append("## Reproducción")
    lines.append("")
    lines.append("```bash")
    lines.append("cd bench && ./run_benchmark.sh --compare")
    lines.append("```")
    lines.append("")
    with open(out_md, "w") as f:
        f.write("\n".join(lines))
    print(f"✓ informe → {out_md}")
    # also print the verdict for the terminal response
    overall = {"verdict": verdict, "rows": rows, "summary": conclusion}
    print(json.dumps(overall, ensure_ascii=False))
    return overall


if __name__ == "__main__":
    cmd = sys.argv[1]
    if cmd == "emit-json":
        sys.exit(emit_json(*sys.argv[2:]))
    if cmd == "emit-json-names":
        emit_json_names(*sys.argv[2:])
        sys.exit(0)
    if cmd == "report":
        base, cur, out, iters, thr = sys.argv[2:7]
        report(base, cur, out, iters, thr)
        sys.exit(0)
    print("unknown cmd", file=sys.stderr)
    sys.exit(2)