"""Configuration handling: config.toml loading and `lbh init` templates.

Search order for the config file:
  1. --config <path>
  2. ./config.toml (current directory)
  3. ~/.lbh/config.toml
Falling back to built-in defaults when no file exists.
"""

from __future__ import annotations

import os
import tomllib
from pathlib import Path

DEFAULTS: dict = {
    "paths": {
        "llama_bin": "",
        "models_dir": "models",
        "results_dir": "results",
    },
    "bench": {
        "prompt_tokens": 512,
        "gen_tokens": 128,
        "reps": 3,
        "ngl": 99,
        "flash_attn": True,
        "kv_quant": "q8_0",
        "context": 8192,
    },
    "quality": {
        "prompt_set": "",
        "port": 8080,
        "context": 8192,
        "max_tokens_floor": 1024,
        "image_path": "",
        "code_exec_check": False,
    },
    "download": {
        "dest": "",  # empty = use paths.models_dir
    },
}


def _deep_merge(base: dict, override: dict) -> dict:
    out = dict(base)
    for k, v in (override or {}).items():
        if isinstance(v, dict) and isinstance(out.get(k), dict):
            out[k] = _deep_merge(out[k], v)
        else:
            out[k] = v
    return out


class Config:
    def __init__(self, data: dict, source: Path | None):
        self.data = data
        self.source = source

    def get(self, section: str, key: str, default=None):
        return self.data.get(section, {}).get(key, default)

    def path(self, section: str, key: str) -> Path:
        return Path(str(self.get(section, key, ""))).expanduser()


def find_config_file(explicit: str | None = None) -> Path | None:
    if explicit:
        p = Path(explicit)
        return p if p.is_file() else None
    env = os.environ.get("LBH_CONFIG")
    if env and Path(env).is_file():
        return Path(env)
    cwd = Path.cwd() / "config.toml"
    if cwd.is_file():
        return cwd
    home = Path.home() / ".lbh" / "config.toml"
    if home.is_file():
        return home
    return None


def load_config(explicit: str | None = None) -> Config:
    src = find_config_file(explicit)
    data = _deep_merge({}, DEFAULTS)
    if src is not None:
        with open(src, "rb") as f:
            user = tomllib.load(f)
        data = _deep_merge(data, user)
    return Config(data, src)


def models_config_path(cfg: Config) -> Path:
    """models.toml sits next to config.toml, else in the current directory."""
    if cfg.source is not None:
        return cfg.source.parent / "models.toml"
    return Path.cwd() / "models.toml"


CONFIG_TEMPLATE = '''# ============================================================
# llama-bench-helper (lbh) 設定ファイル
# 各項目の上のコメントが、その設定の意味と既定値の説明です。
# パスは絶対パスでも相対パス（このファイルからの相対ではなく
# 実行したフォルダからの相対）でも書けます。
# ============================================================

[paths]
# llama-bench.exe / llama-server.exe があるフォルダ。
# 空欄なら PATH から探します。バージョンを固定したい場合は
# ビルドごとのフォルダ（例: C:/tools/llama.cpp/b11960）を指定。
llama_bin = ""

# GGUF モデルを置くフォルダ。ダウンロードの保存先の既定にもなります。
models_dir = "models"

# 計測結果を保存するフォルダ。実行ごとに日時名のサブフォルダが作られ、
# 過去の結果が上書きされることはありません。
results_dir = "results"

[bench]
# プロンプト処理の計測トークン数（pp の数字。pp512 なら 512）。
prompt_tokens = 512

# 生成の計測トークン数（tg の数字。tg128 なら 128）。
gen_tokens = 128

# 各モデル・各項目の繰り返し回数。結果は中央値を使います。
reps = 3

# GPU に載せるレイヤー数（-ngl）。99 で全レイヤーを GPU へ。
ngl = 99

# Flash Attention を使うかどうか。
flash_attn = true

# KV キャッシュの量子化（"q8_0" / "f16" など）。VRAM 節約に効きます。
kv_quant = "q8_0"

# VRAM 使用量を見積もるときの想定コンテキスト長。
context = 8192

[quality]
# 使うプロンプトセット（JSON）のパス。空欄なら同梱の既定セット
# （今回のベンチマークで使った共通プロンプト）を使います。
prompt_set = ""

# llama-server が使うポート番号。他と競合する場合は変えてください。
port = 8080

# 品質測定のコンテキスト長（-c）。
context = 8192

# プロンプトの max_tokens がこの値未満だと警告します。
# 思考型モデルは「思考＋回答」でトークンを多く使うため、
# 小さすぎると回答が途中で切れます。既定は 1024 です。
max_tokens_floor = 1024

# 画像プロンプトで使うテスト画像のパス。空欄なら画像プロンプトは
# 対象モデルでもスキップされます（--image でも指定できます）。
image_path = ""

# 回答に含まれる Python コードを自動で実行して検証するかどうか。
# モデルの出力は信頼できないコードなので、既定は false です。
code_exec_check = false

[download]
# ダウンロードの保存先。空欄なら paths.models_dir と同じ場所です。
dest = ""
'''

MODELS_TEMPLATE = '''# ============================================================
# モデル別の設定（任意）
# models_dir 内の GGUF は自動で検出されるので、ここへの登録は
# 「モデルごとに違う指定が必要なとき」だけ要ります。
# 1 モデル = [[model]] ブロック 1 つ。file はファイル名だけでも、
# パスでも構いません。
# ============================================================

# 例: 部分オフロードが必要なモデルには追加引数を指定します。
# [[model]]
# file = "Qwen3-Coder-30B-A3B-Instruct-Q4_K_M.gguf"
# extra_bench_args = ["-ncmoe", "12"]
# extra_server_args = ["-ncmoe", "12"]

# 例: 画像入力に対応するモデルは mmproj を対応付けます。
# mmproj が登録されたモデルだけ、品質測定で画像プロンプトが実行されます。
# [[model]]
# file = "Qwen3VL-8B-Instruct-Q4_K_M.gguf"
# mmproj = "mmproj-Qwen3VL-8B-Instruct-F16.gguf"
# vision = true
'''


def init_files(dest_dir: Path, force: bool = False) -> list[Path]:
    """Write config.toml / models.toml templates into dest_dir."""
    dest_dir.mkdir(parents=True, exist_ok=True)
    written: list[Path] = []
    for name, body in (("config.toml", CONFIG_TEMPLATE), ("models.toml", MODELS_TEMPLATE)):
        p = dest_dir / name
        if p.exists() and not force:
            print(f"  exists, skipped: {p}")
            continue
        p.write_text(body, encoding="utf-8")
        written.append(p)
        print(f"  wrote: {p}")
    return written
