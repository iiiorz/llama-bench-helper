"""Hugging Face model download: list files via the API, download via hf CLI."""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import urllib.error
import urllib.request
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path

from .util import confirm, fmt_bytes, read_json, run_streaming, sha256_file, write_json

API = "https://huggingface.co/api/models/"


@dataclass
class RepoFile:
    path: str
    size: int | None
    sha256: str | None

    @property
    def is_gguf(self) -> bool:
        return self.path.lower().endswith(".gguf")

    @property
    def is_mmproj(self) -> bool:
        return Path(self.path).name.lower().startswith("mmproj")


def parse_repo_input(text: str) -> tuple[str, str | None]:
    """Accept a full HF URL or a bare 'user/repo'. Returns (repo_id, file_hint).

    Handles /tree/<rev>, /blob/<rev>/<file>, /resolve/<rev>/<file> forms.
    """
    t = text.strip()
    file_hint: str | None = None
    if "huggingface.co" in t:
        rest = t.split("huggingface.co", 1)[1].strip("/")
        parts = [p for p in rest.split("/") if p]
        if len(parts) < 2:
            raise ValueError(f"could not parse a repository from: {text}")
        repo = "/".join(parts[:2])
        if len(parts) >= 4 and parts[2] in ("tree", "blob", "resolve"):
            if parts[2] in ("blob", "resolve"):
                file_hint = "/".join(parts[4:])
        return repo, file_hint
    parts = [p for p in t.strip("/").split("/") if p]
    if len(parts) != 2:
        raise ValueError(f"expected 'user/repo' or a huggingface.co URL, got: {text}")
    return t.strip("/"), None


def list_repo_files(repo: str) -> list[RepoFile]:
    url = API + repo + "?blobs=true"
    req = urllib.request.Request(url, headers={"User-Agent": "llama-bench-helper"})
    token = os.environ.get("HF_TOKEN")
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            data = json.load(r)
    except urllib.error.HTTPError as e:  # type: ignore[attr-defined]
        if e.code == 404:
            raise ValueError(f"repository not found: {repo}") from e
        if e.code in (401, 403):
            raise ValueError(
                f"access denied for {repo}. If it is gated/private, set the HF_TOKEN environment variable."
            ) from e
        raise
    files: list[RepoFile] = []
    for s in data.get("siblings", []):
        lfs = s.get("lfs") or {}
        files.append(RepoFile(
            path=s.get("rfilename", ""),
            size=s.get("size", lfs.get("size")),
            sha256=lfs.get("oid"),
        ))
    return files


def find_hf_cli() -> str | None:
    return shutil.which("hf") or shutil.which("huggingface-cli")


def ensure_hf_cli(assume_yes: bool = False) -> str:
    exe = find_hf_cli()
    if exe:
        return exe
    print("The Hugging Face CLI ('hf') was not found.")
    print('Install it with:  pip install "huggingface_hub[cli]"')
    if confirm("Install it now with pip?", default=False, assume_yes=assume_yes):
        rc = run_streaming([sys.executable, "-m", "pip", "install", "huggingface_hub[cli]"])
        if rc != 0:
            raise RuntimeError("pip install failed; please install huggingface_hub manually.")
        exe = find_hf_cli()
        if exe:
            return exe
    raise RuntimeError("hf CLI is required for downloads. Install huggingface_hub and retry.")


def download_files(repo: str, files: list[RepoFile], dest: Path, assume_yes: bool = False) -> Path:
    dest.mkdir(parents=True, exist_ok=True)
    total = sum(f.size or 0 for f in files)
    free = shutil.disk_usage(dest).free
    print(f"Repository : {repo}")
    print(f"Files      : {len(files)} ({fmt_bytes(total)})")
    print(f"Destination: {dest} (free: {fmt_bytes(free)})")
    if total and free < total:
        print("WARNING: not enough free space for the selected files.")
        if not confirm("Continue anyway?", default=False, assume_yes=assume_yes):
            raise RuntimeError("aborted by user")
    hf = ensure_hf_cli(assume_yes=assume_yes)
    cmd = [hf, "download", repo] + [f.path for f in files] + ["--local-dir", str(dest)]
    rc = run_streaming(cmd)
    if rc != 0:
        raise RuntimeError(f"hf download failed with exit code {rc}")

    # Verify sizes and record a manifest with hashes.
    manifest_path = dest / ".lbh-manifest.json"
    manifest: list[dict] = []
    if manifest_path.is_file():
        try:
            manifest = read_json(manifest_path)
        except Exception:
            manifest = []
    now = datetime.now(timezone.utc).astimezone().isoformat(timespec="seconds")
    for f in files:
        p = dest / f.path
        if not p.is_file():
            print(f"  WARNING: not found after download: {f.path}")
            continue
        actual = p.stat().st_size
        ok = (f.size is None) or (actual == f.size)
        print(f"  {'OK ' if ok else 'SIZE MISMATCH '} {f.path} ({fmt_bytes(actual)})")
        digest = sha256_file(p)
        if f.sha256 and digest != f.sha256:
            print(f"  WARNING: sha256 differs from the repository record for {f.path}")
        manifest = [m for m in manifest if not (m.get("repo") == repo and m.get("file") == f.path)]
        manifest.append({
            "repo": repo, "file": f.path, "size": actual,
            "sha256": digest, "downloaded_at": now,
        })
    write_json(manifest_path, manifest)
    print(f"Manifest updated: {manifest_path}")
    return dest
