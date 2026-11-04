"""Model discovery and per-model settings (models.toml)."""

from __future__ import annotations

import tomllib
from dataclasses import dataclass, field
from pathlib import Path


@dataclass
class ModelEntry:
    file: str                       # file name (as written in models.toml / on disk)
    path: Path                      # full path to the .gguf
    size_bytes: int = 0
    mmproj: str | None = None       # file name of the paired mmproj, if any
    mmproj_path: Path | None = None
    vision: bool = False
    extra_bench_args: list[str] = field(default_factory=list)
    extra_server_args: list[str] = field(default_factory=list)

    @property
    def key(self) -> str:
        """Short identifier used for output folders (file stem, sanitized)."""
        stem = Path(self.file).stem
        return "".join(c if (c.isalnum() or c in "-_.") else "_" for c in stem)


# Known presets: file-name fragment -> extra server args that are required.
SERVER_PRESETS: list[tuple[str, list[str]]] = [
    ("gpt-oss", ["--jinja"]),  # harmony chat template needs the jinja engine
]


def load_models_config(path: Path) -> list[dict]:
    if not path.is_file():
        return []
    with open(path, "rb") as f:
        data = tomllib.load(f)
    return list(data.get("model", []))


def _match_mmproj(model_file: str, mmproj_files: list[Path]) -> Path | None:
    """Heuristic pairing: mmproj-Qwen3VL-8B-Instruct-F16.gguf pairs with
    Qwen3VL-8B-Instruct-Q4_K_M.gguf via the shared middle part."""
    stem = Path(model_file).stem.lower()
    for mp in mmproj_files:
        name = mp.stem.lower()
        core = name
        for pre in ("mmproj-", "mmproj_"):
            if core.startswith(pre):
                core = core[len(pre):]
        for suf in ("-f16", "-f32", "-q8_0", "-bf16"):
            if core.endswith(suf):
                core = core[: -len(suf)]
        if core and core in stem:
            return mp
    return None


def scan_models(models_dir: Path, models_cfg: list[dict]) -> list[ModelEntry]:
    """List usable models: files from models.toml first (in order), then any
    other .gguf found in models_dir."""
    entries: list[ModelEntry] = []
    seen: set[str] = set()

    all_gguf: list[Path] = []
    if models_dir.is_dir():
        all_gguf = sorted(models_dir.glob("*.gguf"))
    mmproj_files = [p for p in all_gguf if p.name.lower().startswith("mmproj")]

    def build(file_name: str, cfg: dict) -> ModelEntry | None:
        p = Path(file_name)
        full = p if p.is_absolute() else (models_dir / p.name)
        if not full.is_file():
            full = p if p.is_file() else full
        if not full.is_file():
            print(f"  WARNING: model file not found, skipped: {file_name}")
            return None
        mm_name = cfg.get("mmproj")
        mm_path: Path | None = None
        if mm_name:
            cand = models_dir / Path(str(mm_name)).name
            mm_path = cand if cand.is_file() else None
        if mm_path is None:
            mm_path = _match_mmproj(full.name, mmproj_files)
        vision = bool(cfg.get("vision", False)) or mm_path is not None
        extra_server = [str(x) for x in cfg.get("extra_server_args", [])]
        low = full.name.lower()
        for frag, args in SERVER_PRESETS:
            if frag in low:
                for a in args:
                    if a not in extra_server:
                        extra_server.append(a)
        return ModelEntry(
            file=full.name,
            path=full,
            size_bytes=full.stat().st_size,
            mmproj=mm_path.name if mm_path else None,
            mmproj_path=mm_path,
            vision=vision,
            extra_bench_args=[str(x) for x in cfg.get("extra_bench_args", [])],
            extra_server_args=extra_server,
        )

    for cfg in models_cfg:
        f = str(cfg.get("file", "")).strip()
        if not f:
            continue
        e = build(f, cfg)
        if e is not None:
            entries.append(e)
            seen.add(e.path.name.lower())

    for p in all_gguf:
        if p.name.lower().startswith("mmproj"):
            continue
        if p.name.lower() in seen:
            continue
        e = build(p.name, {})
        if e is not None:
            entries.append(e)
            seen.add(p.name.lower())
    return entries


def select_models(entries: list[ModelEntry], wanted: list[str]) -> list[ModelEntry]:
    """Filter entries by file name / stem fragments (already comma-split)."""
    if not wanted:
        return list(entries)
    out: list[ModelEntry] = []
    for w in wanted:
        lw = w.lower()
        hit = [e for e in entries if lw in e.file.lower() or lw == e.key.lower()]
        if not hit:
            print(f"  WARNING: no model matches '{w}'")
        for e in hit:
            if e not in out:
                out.append(e)
    return out
