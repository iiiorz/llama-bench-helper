"""llama-bench runner: sequential multi-model runs with VRAM logging."""

from __future__ import annotations

import csv
import json
import subprocess
from pathlib import Path

from . import vram as vram_mod
from .config import Config
from .models import ModelEntry
from .util import confirm, find_executable, median, now_stamp, write_json


def parse_bench_jsonl(path: Path) -> dict:
    """Compute pp / tg medians from llama-bench -o jsonl output.

    llama-bench emits one JSON object per test: pp records have n_prompt > 0
    and n_gen == 0; tg records the reverse. The sheet value is the median of
    samples_ts (NOT avg_ts).
    """
    pp_samples: list[float] = []
    tg_samples: list[float] = []
    meta: dict = {}
    for line in Path(path).read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            rec = json.loads(line)
        except json.JSONDecodeError:
            continue
        for k in ("build_number", "build_commit", "gpu_info", "cpu_info", "model_type"):
            if k in rec:
                meta[k] = rec[k]
        samples = [float(x) for x in rec.get("samples_ts", [])]
        if rec.get("n_prompt", 0) and not rec.get("n_gen", 0):
            pp_samples.extend(samples)
        elif rec.get("n_gen", 0) and not rec.get("n_prompt", 0):
            tg_samples.extend(samples)
    return {
        "pp_median": median(pp_samples),
        "tg_median": median(tg_samples),
        "pp_samples": pp_samples,
        "tg_samples": tg_samples,
        "meta": meta,
    }


def run_bench(cfg: Config, models: list[ModelEntry], assume_yes: bool = False,
              overrides: dict | None = None) -> Path:
    ov = overrides or {}
    b = cfg.data["bench"]
    p_tok = int(ov.get("prompt_tokens", b["prompt_tokens"]))
    g_tok = int(ov.get("gen_tokens", b["gen_tokens"]))
    reps = int(ov.get("reps", b["reps"]))
    ngl = int(ov.get("ngl", b["ngl"]))
    fa = bool(ov.get("flash_attn", b["flash_attn"]))
    kv = str(ov.get("kv_quant", b["kv_quant"]))
    ctx = int(ov.get("context", b["context"]))

    bench_exe = find_executable("llama-bench", cfg.get("paths", "llama_bin", "") or None)
    if bench_exe is None:
        raise RuntimeError("llama-bench not found. Set paths.llama_bin in config.toml or add it to PATH.")

    results_dir = cfg.path("paths", "results_dir")
    run_dir = results_dir / f"{now_stamp()}-bench"
    run_dir.mkdir(parents=True, exist_ok=True)
    print(f"llama-bench: {bench_exe}")
    print(f"Run folder : {run_dir}")

    rows: list[dict] = []
    metadata = {
        "kind": "bench",
        "started_at": run_dir.name,
        "gpu": vram_mod.gpu_name(),
        "vram_total_mib": vram_mod.total_vram_mib(),
        "conditions": {"prompt_tokens": p_tok, "gen_tokens": g_tok, "reps": reps,
                       "ngl": ngl, "flash_attn": fa, "kv_quant": kv, "context_for_estimate": ctx},
        "models": [],
    }

    for m in models:
        print(f"\n== {m.file} ==")
        warn = vram_mod.vram_warning(m.size_bytes, ctx, kv)
        status = "OK"
        if warn:
            print(f"  VRAM WARNING: {warn}")
            if not confirm("  Run this model anyway?", default=False, assume_yes=assume_yes):
                status = "SKIPPED (VRAM warning)"
                rows.append(_row(m, status, None, None, None))
                continue
        jsonl = run_dir / f"{m.key}.jsonl"
        vram_file = run_dir / f"vram-{m.key}.csv"
        cmd = [str(bench_exe), "-m", str(m.path),
               "-p", str(p_tok), "-n", str(g_tok), "-r", str(reps),
               "-ngl", str(ngl), "-fa", "on" if fa else "off",
               "-ctk", kv, "-ctv", kv, "-o", "jsonl"] + list(m.extra_bench_args)
        print("  $ " + " ".join(cmd), flush=True)
        logger = vram_mod.VramLogger(vram_file)
        logger.start()
        try:
            with open(jsonl, "w", encoding="utf-8") as out:
                proc = subprocess.run(cmd, stdout=out)
            rc = proc.returncode
        finally:
            peak = logger.stop()
        if rc != 0 or not jsonl.is_file():
            status = f"FAILED (exit {rc})"
            rows.append(_row(m, status, None, None, peak))
            print(f"  {status}; VRAM peak: {peak}")
            continue
        parsed = parse_bench_jsonl(jsonl)
        if parsed["pp_median"] is None and parsed["tg_median"] is None:
            status = "FAILED (no records in jsonl)"
        rows.append(_row(m, status, parsed["pp_median"], parsed["tg_median"], peak))
        metadata["models"].append({"file": m.file, **parsed["meta"]})
        print(f"  pp median: {parsed['pp_median']}  tg median: {parsed['tg_median']}  VRAM peak: {peak} MiB")

    summary = run_dir / "bench-summary.csv"
    with open(summary, "w", encoding="utf-8", newline="") as f:
        w = csv.DictWriter(f, fieldnames=["model", "status", "pp_median_tok_s", "tg_median_tok_s", "vram_peak_mib"])
        w.writeheader()
        w.writerows(rows)
    write_json(run_dir / "run-metadata.json", metadata)
    print(f"\nSummary: {summary}")
    return run_dir


def _row(m: ModelEntry, status: str, pp, tg, peak) -> dict:
    return {
        "model": m.file,
        "status": status,
        "pp_median_tok_s": "" if pp is None else round(pp, 2),
        "tg_median_tok_s": "" if tg is None else round(tg, 2),
        "vram_peak_mib": "" if peak is None else int(peak),
    }
