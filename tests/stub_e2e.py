"""End-to-end smoke test with stub executables.

Creates fake `llama-bench`, `llama-server`, `nvidia-smi` and `hf` commands on
PATH, then drives the real CLI (models / bench / quality / export / report)
in a temporary workspace. Run:  python3 tests/stub_e2e.py
Exits non-zero on the first failed expectation.
"""

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).parent.parent

LLAMA_BENCH = """#!/usr/bin/env python3
import json, sys
for nt, samples in ((512, [3500.0, 3600.0, 3550.0]), (0, [60.0, 62.0, 61.0])):
    rec = {"build_number": 11960, "gpu_info": "Stub GPU", "model_type": "stub",
           "n_prompt": nt, "n_gen": 0 if nt else 128,
           "avg_ts": sum(samples) / len(samples), "samples_ts": samples}
    print(json.dumps(rec))
"""

LLAMA_SERVER = """#!/usr/bin/env python3
import json, sys
from http.server import BaseHTTPRequestHandler, HTTPServer

port = 8080
args = sys.argv[1:]
if "--port" in args:
    port = int(args[args.index("--port") + 1])

TEXT = "## メリット\\n・通勤時間の削減でリラックスできる\\n\\n## 注意点\\n・集中力の維持が難しい場合がある\\n\\n在宅勤務はメリットも多いが、自発的な管理が重要である。"

class H(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass
    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(b"ok")
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(n) or b"{}")
        content = body["messages"][0]["content"]
        is_image = isinstance(content, list)
        text = "赤い円は5個、青い四角は3個、緑の三角は4個、星は7個あります。" if is_image else TEXT
        out = {"choices": [{"message": {"role": "assistant", "content": text}}]}
        data = json.dumps(out, ensure_ascii=False).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers(); self.wfile.write(data)

HTTPServer(("127.0.0.1", port), H).serve_forever()
"""

NVIDIA_SMI = """#!/usr/bin/env python3
import sys
q = " ".join(sys.argv)
if "memory.total" in q:
    print("16384")
elif "memory.used" in q:
    print("9000")
elif "name" in q:
    print("Stub GPU")
"""

HF = """#!/usr/bin/env python3
import sys
from pathlib import Path
args = sys.argv[1:]
if args and args[0] == "download":
    dest = Path(args[args.index("--local-dir") + 1])
    dest.mkdir(parents=True, exist_ok=True)
    repo = args[1]
    files = [a for a in args[2:] if not a.startswith("--") and a != str(dest)]
    for f in files:
        p = dest / f
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(b"stub-gguf-bytes")
    print("downloaded", len(files))
"""


def check(cond: bool, label: str) -> None:
    print(("PASS " if cond else "FAIL ") + label)
    if not cond:
        sys.exit(1)


def main() -> int:
    tmp = Path(tempfile.mkdtemp(prefix="lbh-e2e-"))
    bin_dir = tmp / "bin"
    bin_dir.mkdir()
    for name, body in (("llama-bench", LLAMA_BENCH), ("llama-server", LLAMA_SERVER),
                       ("nvidia-smi", NVIDIA_SMI), ("hf", HF)):
        p = bin_dir / name
        p.write_text(body, encoding="utf-8")
        p.chmod(0o755)

    work = tmp / "work"
    (work / "models").mkdir(parents=True)
    (work / "models" / "StubModel-Q4_K_M.gguf").write_bytes(b"x" * 2048)
    (work / "models" / "StubVL-Q4_K_M.gguf").write_bytes(b"y" * 1024)
    (work / "models" / "mmproj-StubVL-F16.gguf").write_bytes(b"z" * 512)
    (work / "config.toml").write_text(
        '[paths]\nllama_bin = ""\nmodels_dir = "models"\nresults_dir = "results"\n'
        '[quality]\nport = 18099\nimage_path = ""\n', encoding="utf-8")
    (work / "models.toml").write_text(
        '[[model]]\nfile = "StubVL-Q4_K_M.gguf"\nmmproj = "mmproj-StubVL-F16.gguf"\nvision = true\n',
        encoding="utf-8")
    (work / "test.png").write_bytes(b"\x89PNG\r\n\x1a\n")

    env = dict(os.environ)
    env["PATH"] = f"{bin_dir}:{env['PATH']}"
    env["PYTHONPATH"] = str(ROOT / "src")

    def run(*argv: str) -> subprocess.CompletedProcess:
        return subprocess.run([sys.executable, "-m", "lbh", *argv],
                              cwd=work, env=env, capture_output=True, text=True, timeout=120)

    r = run("models")
    check(r.returncode == 0 and "StubModel-Q4_K_M.gguf" in r.stdout, "models lists stub models")
    check("vision" in r.stdout, "models marks the VL model as vision")

    r = run("--yes", "bench", "--models", "StubModel,StubVL")
    check(r.returncode == 0, f"bench exits 0 ({r.stderr.strip()[-200:]})")
    bench_dirs = list((work / "results").glob("*-bench"))
    check(len(bench_dirs) == 1, "bench run folder created")
    summary = (bench_dirs[0] / "bench-summary.csv").read_text(encoding="utf-8")
    check("3550.0" in summary and "61.0" in summary, "bench summary has stub medians")
    check((bench_dirs[0] / "report.md").is_file(), "bench report.md generated")

    r = run("--yes", "quality", "--models", "StubModel,StubVL", "--image", "test.png")
    check(r.returncode == 0, f"quality exits 0 ({r.stderr.strip()[-200:]})")
    q_dirs = list((work / "results").glob("*-quality"))
    check(len(q_dirs) == 1, "quality run folder created")
    qd = q_dirs[0]
    check((qd / "ai-eval-bundle.md").is_file(), "AI eval bundle generated")
    bundle = (qd / "ai-eval-bundle.md").read_text(encoding="utf-8")
    check("審査員" in bundle and "通勤時間の削減" in bundle, "bundle contains instructions and responses")
    vl_image = qd / "quality" / "StubVL-Q4_K_M" / "StubVL-Q4_K_M-image.txt"
    check(vl_image.is_file() and "赤い円は5個" in vl_image.read_text(encoding="utf-8"),
          "vision model got the image prompt")
    plain_image = qd / "quality" / "StubModel-Q4_K_M" / "StubModel-Q4_K_M-image.txt"
    check(not plain_image.exists(), "non-vision model skipped the image prompt")
    qsum = (qd / "quality-summary.csv").read_text(encoding="utf-8")
    check("StubModel-Q4_K_M.gguf" in qsum, "quality summary lists models")

    r = run("export", "--anonymize")
    check(r.returncode == 0 and (qd / "ai-eval-mapping.json").is_file(), "anonymized export + mapping file")
    bundle2 = (qd / "ai-eval-bundle.md").read_text(encoding="utf-8")
    check("Model A" in bundle2, "anonymized bundle uses Model A/B labels")

    print("\nAll stub end-to-end checks passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
