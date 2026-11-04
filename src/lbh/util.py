"""Shared small utilities for lbh (no third-party dependencies)."""

from __future__ import annotations

import hashlib
import json
import os
import shutil
import statistics
import subprocess
import sys
from datetime import datetime
from pathlib import Path


def now_stamp() -> str:
    return datetime.now().strftime("%Y%m%d-%H%M%S")


def fmt_bytes(n: float | int | None) -> str:
    if n is None:
        return "-"
    units = ["B", "KiB", "MiB", "GiB", "TiB"]
    v = float(n)
    for u in units:
        if v < 1024 or u == units[-1]:
            return f"{v:.2f} {u}" if u != "B" else f"{int(v)} B"
        v /= 1024
    return f"{v:.2f} TiB"


def median(values: list[float]) -> float | None:
    if not values:
        return None
    return float(statistics.median(values))


def find_executable(name: str, search_dir: str | os.PathLike | None = None) -> Path | None:
    """Find an executable by base name, OS-aware (.exe on Windows).

    If search_dir is given and points to a file, it is used as-is; if it is a
    directory, the executable is looked up inside it first, then on PATH.
    """
    candidates: list[Path] = []
    if search_dir:
        p = Path(search_dir)
        if p.is_file():
            return p
        if p.is_dir():
            candidates.append(p)
    found = shutil.which(name)
    exe_names = [name]
    if os.name == "nt" and not name.lower().endswith(".exe"):
        exe_names = [name + ".exe", name]
    for d in candidates:
        for en in exe_names:
            cand = d / en
            if cand.is_file():
                return cand
    if found:
        return Path(found)
    return None


def run_streaming(cmd: list[str], cwd: str | None = None) -> int:
    """Run a command with output streamed to the terminal. Returns exit code."""
    print("  $ " + " ".join(str(c) for c in cmd), flush=True)
    try:
        proc = subprocess.run([str(c) for c in cmd], cwd=cwd)
        return proc.returncode
    except FileNotFoundError:
        print(f"  ERROR: executable not found: {cmd[0]}", file=sys.stderr)
        return 127


def sha256_file(path: Path, chunk: int = 1024 * 1024) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        while True:
            b = f.read(chunk)
            if not b:
                break
            h.update(b)
    return h.hexdigest()


def write_json(path: Path, data) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)


def read_json(path: Path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def split_list(values: list[str] | None) -> list[str]:
    """Normalize list-like CLI values.

    A value such as "a,b,c" may arrive as ONE element (a common shell /
    wrapper behaviour), so every element is split on commas and trimmed.
    """
    out: list[str] = []
    for v in values or []:
        for part in str(v).split(","):
            part = part.strip()
            if part:
                out.append(part)
    return out


def ask(prompt: str, default: str | None = None) -> str:
    suffix = f" [{default}]" if default else ""
    try:
        ans = input(f"{prompt}{suffix}: ").strip()
    except EOFError:
        return default or ""
    return ans or (default or "")


def confirm(prompt: str, default: bool = False, assume_yes: bool = False) -> bool:
    if assume_yes:
        return True
    hint = "Y/n" if default else "y/N"
    try:
        ans = input(f"{prompt} ({hint}): ").strip().lower()
    except EOFError:
        return default
    if not ans:
        return default
    return ans in ("y", "yes")
