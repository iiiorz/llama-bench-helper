"""Markdown report generation from a run folder."""

from __future__ import annotations

import csv
from pathlib import Path


def _csv_table(path: Path) -> str:
    with open(path, encoding="utf-8", newline="") as f:
        rows = list(csv.reader(f))
    if not rows:
        return "(empty)"
    head, body = rows[0], rows[1:]
    lines = ["| " + " | ".join(head) + " |",
             "|" + "|".join("---" for _ in head) + "|"]
    for r in body:
        lines.append("| " + " | ".join(r) + " |")
    return "\n".join(lines)


def make_report(run_dir: Path) -> Path:
    run_dir = Path(run_dir)
    parts = [f"# lbh report: {run_dir.name}\n"]
    bench = run_dir / "bench-summary.csv"
    if bench.is_file():
        parts.append("## Benchmark (llama-bench)\n")
        parts.append(_csv_table(bench))
    quality = run_dir / "quality-summary.csv"
    if quality.is_file():
        parts.append("\n## Quality (llama-server)\n")
        parts.append(_csv_table(quality))
    if len(parts) == 1:
        parts.append("No summary CSV found in this folder.")
    out = run_dir / "report.md"
    out.write_text("\n".join(parts) + "\n", encoding="utf-8")
    return out


def latest_run(results_dir: Path, kind: str | None = None) -> Path | None:
    if not results_dir.is_dir():
        return None
    dirs = [d for d in results_dir.iterdir() if d.is_dir()]
    if kind:
        dirs = [d for d in dirs if d.name.endswith(f"-{kind}")]
    if not dirs:
        return None
    return sorted(dirs)[-1]
