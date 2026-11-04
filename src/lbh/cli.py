"""llama-bench-helper (lbh): CLI entry point."""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

from . import bench as bench_mod
from . import hf as hf_mod
from . import quality as quality_mod
from . import report as report_mod
from .config import init_files, load_config, models_config_path
from .models import load_models_config, scan_models, select_models
from .util import ask, confirm, fmt_bytes, split_list


def _models(cfg, wanted: list[str]):
    entries = scan_models(cfg.path("paths", "models_dir"), load_models_config(models_config_path(cfg)))
    return select_models(entries, split_list(wanted))


def _pick_models_interactive(cfg):
    entries = scan_models(cfg.path("paths", "models_dir"), load_models_config(models_config_path(cfg)))
    if not entries:
        print("No models found. Check paths.models_dir in config.toml.")
        return []
    for i, e in enumerate(entries, 1):
        extra = " [vision]" if e.vision else ""
        print(f"  {i}. {e.file} ({fmt_bytes(e.size_bytes)}){extra}")
    ans = ask("Models to use (numbers, comma separated; empty = all)")
    if not ans:
        return entries
    picked = []
    for part in split_list([ans]):
        if part.isdigit() and 1 <= int(part) <= len(entries):
            picked.append(entries[int(part) - 1])
    return picked


# ---------------------------------------------------------------------------

def cmd_init(args) -> int:
    dest = Path(args.dir) if args.dir else Path.cwd()
    init_files(dest, force=args.force)
    return 0


def cmd_models(args) -> int:
    cfg = load_config(args.config)
    entries = scan_models(cfg.path("paths", "models_dir"), load_models_config(models_config_path(cfg)))
    if not entries:
        print("No models found.")
        return 0
    for e in entries:
        line = f"{e.file}  ({fmt_bytes(e.size_bytes)})"
        if e.vision:
            line += f"  vision, mmproj: {e.mmproj}"
        if e.extra_bench_args or e.extra_server_args:
            line += f"  extra bench: {e.extra_bench_args} server: {e.extra_server_args}"
        print(line)
    return 0


def cmd_download(args) -> int:
    cfg = load_config(args.config)
    target = args.url or ask("Hugging Face URL or user/repo")
    if not target:
        print("Nothing to do.")
        return 1
    repo, hint = hf_mod.parse_repo_input(target)
    files = hf_mod.list_repo_files(repo)
    if not files:
        print("No files found in this repository.")
        return 1
    ggufs = [f for f in files if f.is_gguf]
    print(f"\nRepository: {repo} — {len(files)} files, {len(ggufs)} GGUF")
    for i, f in enumerate(files, 1):
        tag = " [mmproj]" if f.is_mmproj else (" [gguf]" if f.is_gguf else "")
        print(f"  {i:>3}. {f.path}  ({fmt_bytes(f.size)}){tag}")
    if not ggufs:
        print("\nWARNING: this repository contains no .gguf files. llama.cpp cannot use it "
              "as-is (a format conversion would be needed).")
        if not confirm("Continue anyway?", default=False, assume_yes=args.yes):
            return 1
    chosen: list[hf_mod.RepoFile]
    if args.files:
        wanted = split_list(args.files)
        chosen = [f for f in files if f.path in wanted or Path(f.path).name in wanted]
        missing = [w for w in wanted if not any(f.path == w or Path(f.path).name == w for f in files)]
        for w in missing:
            print(f"  WARNING: no such file in repository: {w}")
    elif hint:
        chosen = [f for f in files if f.path == hint]
    else:
        ans = ask("Files to download (numbers, 'gguf' for all GGUF, or 'all')", "gguf")
        if ans.lower() == "all":
            chosen = list(files)
        elif ans.lower() == "gguf":
            chosen = list(ggufs)
        else:
            chosen = []
            for part in split_list([ans]):
                if part.isdigit() and 1 <= int(part) <= len(files):
                    chosen.append(files[int(part) - 1])
    mm = [f for f in chosen if f.is_mmproj]
    main_gguf = [f for f in chosen if f.is_gguf and not f.is_mmproj]
    repo_mm = [f for f in files if f.is_mmproj]
    if main_gguf and repo_mm and not mm:
        if confirm("This repository also has an mmproj (vision) file. Download it too?",
                   default=True, assume_yes=args.yes):
            chosen += repo_mm
    if not chosen:
        print("No files selected.")
        return 1
    dest = Path(args.dest) if args.dest else (cfg.path("download", "dest") if str(cfg.get("download", "dest", "")) else cfg.path("paths", "models_dir"))
    hf_mod.download_files(repo, chosen, dest, assume_yes=args.yes)
    return 0


def cmd_bench(args) -> int:
    cfg = load_config(args.config)
    models = _pick_models_interactive(cfg) if args.interactive or not args.models else _models(cfg, args.models)
    if not models:
        print("No models selected.")
        return 1
    ov = {}
    if args.prompt_tokens: ov["prompt_tokens"] = args.prompt_tokens
    if args.gen_tokens: ov["gen_tokens"] = args.gen_tokens
    if args.reps: ov["reps"] = args.reps
    if args.ngl is not None: ov["ngl"] = args.ngl
    if args.context: ov["context"] = args.context
    run_dir = bench_mod.run_bench(cfg, models, assume_yes=args.yes, overrides=ov)
    report_mod.make_report(run_dir)
    return 0


def cmd_quality(args) -> int:
    cfg = load_config(args.config)
    models = _pick_models_interactive(cfg) if args.interactive or not args.models else _models(cfg, args.models)
    if not models:
        print("No models selected.")
        return 1
    run_dir = quality_mod.run_quality(cfg, models, prompts_file=args.prompts,
                                      image_path=args.image, assume_yes=args.yes)
    bundle, mapping = quality_mod.export_ai_eval(run_dir, anonymize=args.anonymize)
    print(f"AI evaluation bundle: {bundle}")
    if mapping:
        print(f"Anonymization mapping (keep private): {mapping}")
    report_mod.make_report(run_dir)
    return 0


def cmd_export(args) -> int:
    cfg = load_config(args.config)
    run_dir = Path(args.run) if args.run else report_mod.latest_run(cfg.path("paths", "results_dir"), "quality")
    if not run_dir:
        print("No quality run found.")
        return 1
    bundle, mapping = quality_mod.export_ai_eval(run_dir, anonymize=args.anonymize)
    print(f"AI evaluation bundle: {bundle}")
    if mapping:
        print(f"Anonymization mapping (keep private): {mapping}")
    return 0


def cmd_report(args) -> int:
    cfg = load_config(args.config)
    run_dir = Path(args.run) if args.run else report_mod.latest_run(cfg.path("paths", "results_dir"))
    if not run_dir:
        print("No run folders found.")
        return 1
    out = report_mod.make_report(run_dir)
    print(f"Report: {out}")
    return 0


# ---------------------------------------------------------------------------

def _menu(args) -> int:
    print("llama-bench-helper (lbh)")
    print("  1) Download models from Hugging Face")
    print("  2) Run llama-bench")
    print("  3) Run quality checks")
    print("  4) Make a report from the latest run")
    print("  5) List detected models")
    print("  0) Exit")
    choice = ask("Select", "0")
    ns = argparse.Namespace(**vars(args))
    if choice == "1":
        ns.url = None; ns.files = None; ns.dest = None
        return cmd_download(ns)
    if choice == "2":
        ns.models = None; ns.interactive = True
        ns.prompt_tokens = None; ns.gen_tokens = None; ns.reps = None; ns.ngl = None; ns.context = None
        return cmd_bench(ns)
    if choice == "3":
        ns.models = None; ns.interactive = True; ns.prompts = None; ns.image = None; ns.anonymize = False
        return cmd_quality(ns)
    if choice == "4":
        ns.run = None
        return cmd_report(ns)
    if choice == "5":
        return cmd_models(ns)
    return 0


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(prog="lbh", description="llama.cpp benchmark helper")
    ap.add_argument("--config", help="path to config.toml")
    ap.add_argument("--yes", action="store_true", help="answer yes to confirmations")
    sub = ap.add_subparsers(dest="cmd")

    p = sub.add_parser("init", help="write config.toml / models.toml templates")
    p.add_argument("--dir")
    p.add_argument("--force", action="store_true")
    p.set_defaults(fn=cmd_init)

    p = sub.add_parser("models", help="list detected models")
    p.set_defaults(fn=cmd_models)

    p = sub.add_parser("download", help="download GGUF files from Hugging Face")
    p.add_argument("url", nargs="?", help="HF URL or user/repo")
    p.add_argument("--files", help="file names to download (comma separated)")
    p.add_argument("--dest", help="destination folder")
    p.set_defaults(fn=cmd_download)

    p = sub.add_parser("bench", help="run llama-bench for selected models")
    p.add_argument("--models", nargs="*", help="model file names or fragments (comma separated allowed)")
    p.add_argument("--interactive", action="store_true")
    p.add_argument("-p", dest="prompt_tokens", type=int)
    p.add_argument("-n", dest="gen_tokens", type=int)
    p.add_argument("--reps", type=int)
    p.add_argument("--ngl", type=int)
    p.add_argument("--context", type=int, help="context used for the VRAM estimate")
    p.set_defaults(fn=cmd_bench)

    p = sub.add_parser("quality", help="run shared-prompt quality checks via llama-server")
    p.add_argument("--models", nargs="*")
    p.add_argument("--interactive", action="store_true")
    p.add_argument("--prompts", help="prompt set JSON (default: bundled set)")
    p.add_argument("--image", help="test image for vision models")
    p.add_argument("--anonymize", action="store_true", help="anonymize model names in the AI eval bundle")
    p.set_defaults(fn=cmd_quality)

    p = sub.add_parser("export", help="(re)build the AI evaluation bundle for a quality run")
    p.add_argument("--run", help="run folder (default: latest quality run)")
    p.add_argument("--anonymize", action="store_true")
    p.set_defaults(fn=cmd_export)

    p = sub.add_parser("report", help="build report.md for a run folder")
    p.add_argument("--run")
    p.set_defaults(fn=cmd_report)

    args = ap.parse_args(argv)
    if not getattr(args, "fn", None):
        return _menu(args)
    try:
        return args.fn(args)
    except (RuntimeError, ValueError) as e:
        print(f"ERROR: {e}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
