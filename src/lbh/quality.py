"""Quality checks via llama-server, plus the AI-evaluation export."""

from __future__ import annotations

import base64
import csv
import json
import random
import subprocess
import time
import urllib.request
from pathlib import Path

from . import checks as checks_mod
from . import vram as vram_mod
from .config import Config
from .models import ModelEntry
from .util import find_executable, now_stamp, read_json, write_json

_KIND_BY_LEGACY = {"p1_summary": "text", "p2_code": "text", "p3_logic": "text",
                   "p4_long": "text", "default_sample": "default", "image_prompt": "image"}


def load_prompt_set(cfg: Config, explicit: str | None = None) -> tuple[list[dict], Path]:
    """Load a prompt set JSON. Accepts the tool format ({prompts: [...]}) and
    the legacy dict format used by the original scripts ({p1_summary: {...}}).
    Falls back to the bundled default set."""
    path: Path | None = None
    if explicit:
        path = Path(explicit)
    elif cfg.get("quality", "prompt_set", ""):
        path = Path(str(cfg.get("quality", "prompt_set")))
    if path is None or not path.is_file():
        path = Path(__file__).parent / "data" / "default_prompts.json"
    data = read_json(path)
    if isinstance(data, dict) and isinstance(data.get("prompts"), list):
        return data["prompts"], path
    prompts: list[dict] = []
    for key, e in (data or {}).items():
        if not isinstance(e, dict) or "text" not in e:
            continue
        prompts.append({
            "id": key, "title": e.get("title", key),
            "kind": _KIND_BY_LEGACY.get(key, "text"),
            "text": e["text"], "temperature": e.get("temperature", 0),
            "max_tokens": e.get("max_tokens", 1024), "checks": e.get("checks", []),
        })
    return prompts, path


class Server:
    def __init__(self, exe: Path, args: list[str], out_dir: Path, port: int):
        self.exe = exe
        self.args = args
        self.out_dir = out_dir
        self.port = port
        self.proc: subprocess.Popen | None = None

    def start(self) -> float:
        """Start llama-server; return load seconds until /health answers 200."""
        self.out_dir.mkdir(parents=True, exist_ok=True)
        stdout = open(self.out_dir / "server.stdout.log", "w", encoding="utf-8")
        stderr = open(self.out_dir / "server.stderr.log", "w", encoding="utf-8")
        cmd = [str(self.exe)] + self.args
        print("  $ " + " ".join(cmd), flush=True)
        t0 = time.monotonic()
        self.proc = subprocess.Popen(cmd, stdout=stdout, stderr=stderr)
        deadline = t0 + 600
        while time.monotonic() < deadline:
            if self.proc.poll() is not None:
                raise RuntimeError(f"llama-server exited early (code {self.proc.returncode}); see {self.out_dir}/server.stderr.log")
            try:
                with urllib.request.urlopen(f"http://127.0.0.1:{self.port}/health", timeout=2) as r:
                    if r.status == 200:
                        return time.monotonic() - t0
            except Exception:
                time.sleep(0.5)
        raise RuntimeError("llama-server did not become ready within 600 s")

    def stop(self) -> None:
        if self.proc and self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.proc.kill()
        time.sleep(1)


def chat(port: int, payload: dict, timeout: int = 1200) -> str:
    body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    req = urllib.request.Request(
        f"http://127.0.0.1:{port}/v1/chat/completions",
        data=body, headers={"Content-Type": "application/json; charset=utf-8"}, method="POST",
    )
    with urllib.request.urlopen(req, timeout=timeout) as r:
        data = json.load(r)
    msg = data["choices"][0]["message"]
    text = msg.get("content")
    if not text and msg.get("reasoning_content"):
        text = msg["reasoning_content"]
    return text or ""


def _save_exchange(path: Path, title: str, prompt: str, response: str) -> None:
    path.write_text(f"# {title}\n\n## Prompt\n{prompt}\n\n## Response\n{response}\n", encoding="utf-8")


def _code_exec_check(text: str) -> dict:
    """Opt-in: extract python code blocks and try to execute them (10 s cap).
    Model output is untrusted code; this only runs when explicitly enabled."""
    import re
    blocks = re.findall(r"```(?:python)?\n(.*?)```", text, re.S)
    if not blocks:
        return {"ran": False, "detail": "no code block found"}
    code = "\n\n".join(blocks)
    try:
        proc = subprocess.run(
            ["python3", "-c", code], capture_output=True, text=True, timeout=10,
        )
        return {"ran": True, "ok": proc.returncode == 0,
                "detail": (proc.stderr or proc.stdout or "").strip()[-300:]}
    except subprocess.TimeoutExpired:
        return {"ran": True, "ok": False, "detail": "timed out after 10 s"}
    except Exception as e:  # noqa: BLE001
        return {"ran": False, "detail": str(e)}


def run_quality(cfg: Config, models: list[ModelEntry], prompts_file: str | None = None,
                image_path: str | None = None, assume_yes: bool = False) -> Path:
    q = cfg.data["quality"]
    port = int(q["port"])
    ctx = int(q["context"])
    floor = int(q["max_tokens_floor"])
    prompts, used_path = load_prompt_set(cfg, prompts_file)
    print(f"Prompt set: {used_path} ({len(prompts)} prompts)")
    small = [p["id"] for p in prompts if int(p.get("max_tokens", 0)) < floor]
    if small:
        print(f"WARNING: max_tokens below the floor ({floor}) for: {', '.join(small)}. "
              "Thinking models may be cut off before finishing their answer.")

    img = image_path or (str(q["image_path"]) if q["image_path"] else None)
    server_exe = find_executable("llama-server", cfg.get("paths", "llama_bin", "") or None)
    if server_exe is None:
        raise RuntimeError("llama-server not found. Set paths.llama_bin in config.toml or add it to PATH.")

    results_dir = cfg.path("paths", "results_dir")
    run_dir = results_dir / f"{now_stamp()}-quality"
    qroot = run_dir / "quality"
    qroot.mkdir(parents=True, exist_ok=True)
    print(f"Run folder: {run_dir}")

    prompt_ids = [p["id"] for p in prompts]
    rows: list[dict] = []
    all_checks: dict[str, dict] = {}

    for m in models:
        print(f"\n== {m.file} ==")
        out_dir = qroot / m.key
        out_dir.mkdir(parents=True, exist_ok=True)
        row: dict = {"model": m.file, "status": "OK", "load_time_sec": "",
                     "vram_peak_mib": ""}
        for pid in prompt_ids:
            row[pid] = ""
        args = ["-m", str(m.path), "-ngl", "99", "-c", str(ctx),
                "-fa", "on", "-ctk", "q8_0", "-ctv", "q8_0",
                "--host", "127.0.0.1", "--port", str(port)] + list(m.extra_server_args)
        has_image_prompt = any(p["kind"] == "image" for p in prompts)
        if m.vision and m.mmproj_path and has_image_prompt:
            args += ["--mmproj", str(m.mmproj_path)]
        server = Server(server_exe, args, out_dir, port)
        logger = vram_mod.VramLogger(out_dir / "vram-server.csv")
        logger.start()
        try:
            load_sec = server.start()
            row["load_time_sec"] = round(load_sec, 1)
            print(f"  server ready in {load_sec:.1f} s")
            model_checks: dict[str, list] = {}
            for p in prompts:
                pid = p["id"]
                kind = p.get("kind", "text")
                if kind == "image":
                    if not m.vision:
                        row[pid] = "SKIPPED (model has no mmproj)"
                        continue
                    if not img:
                        row[pid] = "SKIPPED (no image given)"
                        continue
                payload: dict = {"model": "local", "max_tokens": int(p.get("max_tokens", 1024))}
                if kind != "default" and p.get("temperature") is not None:
                    payload["temperature"] = p["temperature"]
                if kind == "image":
                    ext = Path(img).suffix.lower()
                    mime = {".jpg": "image/jpeg", ".jpeg": "image/jpeg",
                            ".webp": "image/webp", ".bmp": "image/bmp"}.get(ext, "image/png")
                    b64 = base64.b64encode(Path(img).read_bytes()).decode("ascii")
                    content = [{"type": "text", "text": p["text"]},
                               {"type": "image_url", "image_url": {"url": f"data:{mime};base64,{b64}"}}]
                    payload["messages"] = [{"role": "user", "content": content}]
                    prompt_label = f"(image: {img}) {p['text']}"
                else:
                    payload["messages"] = [{"role": "user", "content": p["text"]}]
                    prompt_label = p["text"]
                try:
                    resp = chat(port, payload)
                    _save_exchange(out_dir / f"{m.key}-{pid}.txt", p.get("title", pid), prompt_label, resp)
                    results = checks_mod.run_checks(resp, p.get("checks", []))
                    model_checks[pid] = results
                    note = checks_mod.summarize(results)
                    if kind == "text" and cfg.get("quality", "code_exec_check", False) and "```" in resp:
                        ex = _code_exec_check(resp)
                        model_checks[pid] = results + [{"type": "code_exec", "ok": bool(ex.get("ok")),
                                                        "detail": ex.get("detail", ""), "needle": "", "value": None, "actual": ex.get("ran")}]
                        note += f"; code exec: {'OK' if ex.get('ok') else 'FAILED'}"
                    row[pid] = f"OK ({len(resp)} chars; {note})"
                    print(f"  {pid}: {row[pid]}")
                except Exception as e:  # noqa: BLE001
                    row[pid] = f"FAILED: {e}"
                    print(f"  {pid}: FAILED: {e}")
            all_checks[m.key] = model_checks
        except Exception as e:  # noqa: BLE001
            row["status"] = f"SERVER FAILED: {e}"
            print(f"  {row['status']}")
        finally:
            peak = logger.stop()
            row["vram_peak_mib"] = "" if peak is None else int(peak)
            server.stop()

        rows.append(row)

    fields = ["model", "status", "load_time_sec", "vram_peak_mib"] + prompt_ids
    with open(run_dir / "quality-summary.csv", "w", encoding="utf-8", newline="") as f:
        w = csv.DictWriter(f, fieldnames=fields)
        w.writeheader()
        w.writerows(rows)
    write_json(run_dir / "checks.json", all_checks)
    write_json(run_dir / "run-metadata.json", {
        "kind": "quality", "prompt_set": str(used_path), "port": port, "context": ctx,
        "prompts": [{k: p.get(k) for k in ("id", "title", "kind", "max_tokens")} for p in prompts],
    })
    print(f"\nSummary: {run_dir / 'quality-summary.csv'}")
    return run_dir


# ---------------------------------------------------------------------------
# AI evaluation export
# ---------------------------------------------------------------------------

_EVAL_INSTRUCTIONS = """# ローカルLLM 回答品質の評価依頼

あなたはローカルLLMの回答品質を評価する審査員です。以下に、複数のモデルへ同じプロンプトを投げた結果をまとめています。

## 評価の進め方

- プロンプトごとに「評価観点」が付いています。その観点と、正確さ・指示への従い方・文章の自然さから、各モデルの回答を 5 段階（5=最良、1=最低）で評価してください。
- 「機械チェック結果」が併記されている場合は、事実として扱ってください（例: 文字数や指定語の出現回数は数え直す必要はありません）。
- 回答が途中で切れている、思考過程だけが表示されている、といった不完全な出力は、その旨を指摘したうえで低く評価してください。

## 出力形式

最後に、結果を次の形式の Markdown 表でまとめてください。

| モデル | プロンプト | 正確さ | 指示追従 | 自然さ | コメント |
|---|---|---:|---:|---:|---|

その後に、モデルごとの総評（強み・弱みを 2〜3 文）と、用途別のおすすめを書いてください。
"""


def _check_lines(results: list[dict]) -> str:
    if not results:
        return "（機械チェックなし）"
    lines = []
    for r in results:
        mark = "OK" if r["ok"] else "NG"
        lines.append(f"- [{mark}] {r['type']}: {r['detail']}")
    return "\n".join(lines)


def export_ai_eval(run_dir: Path, anonymize: bool = False) -> tuple[Path, Path | None]:
    """Build ai-eval-bundle.md from a quality run folder."""
    run_dir = Path(run_dir)
    qroot = run_dir / "quality"
    meta = read_json(run_dir / "run-metadata.json") if (run_dir / "run-metadata.json").is_file() else {}
    checks_all = read_json(run_dir / "checks.json") if (run_dir / "checks.json").is_file() else {}

    # Recover the prompt texts from the saved exchanges.
    model_keys = sorted(d.name for d in qroot.iterdir() if d.is_dir()) if qroot.is_dir() else []
    prompt_ids: list[str] = []
    prompts_by_id: dict[str, dict] = {}
    responses: dict[str, dict[str, str]] = {}
    for mk in model_keys:
        responses[mk] = {}
        for f in sorted((qroot / mk).glob(f"{mk}-*.txt")):
            pid = f.stem[len(mk) + 1:]
            text = f.read_text(encoding="utf-8")
            parts = text.split("## Response", 1)
            prompt_part = parts[0].split("## Prompt", 1)[1].strip() if "## Prompt" in parts[0] else ""
            title = parts[0].splitlines()[0].lstrip("# ").strip() if parts[0].splitlines() else pid
            resp = parts[1].strip() if len(parts) > 1 else ""
            responses[mk][pid] = resp
            if pid not in prompts_by_id:
                prompt_ids.append(pid)
                prompts_by_id[pid] = {"title": title, "text": prompt_part}

    labels = list(model_keys)
    mapping: dict[str, str] | None = None
    if anonymize:
        shuffled = list(model_keys)
        random.Random(20261104).shuffle(shuffled)
        mapping = {}
        labels = []
        for i, mk in enumerate(shuffled):
            label = f"Model {chr(ord('A') + i)}"
            mapping[label] = mk
            labels.append(label)
        label_of = {mk: lab for lab, mk in mapping.items()}
    else:
        label_of = {mk: mk for mk in model_keys}

    out: list[str] = [_EVAL_INSTRUCTIONS]
    for pid in prompt_ids:
        info = prompts_by_id[pid]
        out.append(f"\n## プロンプト: {info['title']}（{pid}）\n")
        out.append("### プロンプト文\n")
        out.append(info["text"])
        out.append("\n### 評価観点\n")
        out.append("- 正確さ: 内容が正しく、求められた情報を落としていないか")
        out.append("- 指示追従: 形式や制約（文数・文字数・構成など）を守れているか")
        out.append("- 自然さ: 文章として自然で読みやすいか")
        for mk in model_keys:
            resp = responses.get(mk, {}).get(pid)
            if resp is None:
                continue
            out.append(f"\n### {label_of[mk]} の回答\n")
            out.append("```")
            out.append(resp)
            out.append("```")
            out.append("\n機械チェック結果:")
            out.append(_check_lines(checks_all.get(mk, {}).get(pid, [])))
    bundle = run_dir / "ai-eval-bundle.md"
    bundle.write_text("\n".join(out) + "\n", encoding="utf-8")
    mapping_path: Path | None = None
    if mapping is not None:
        mapping_path = run_dir / "ai-eval-mapping.json"
        write_json(mapping_path, mapping)
    return bundle, mapping_path
