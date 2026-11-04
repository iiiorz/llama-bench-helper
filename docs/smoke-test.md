# 実機スモークテスト手順書（Windows）

開発環境（Linux + ダミー実行ファイル）では全経路の動作を確認済みです。この手順は、実機（Windows + 本物の llama.cpp）で初めて通すときの確認手順です。小さいモデルで全機能を一巡させてから、本番条件で使ってください。

所要時間の目安: 手順 2 のダウンロード（約 640 MB、回線速度に依存）を除けば 10 分程度です。

## 0. 前提の確認

PowerShell で次を確認します。

```powershell
python --version        # 3.11 以上（py --version でも可）
hf --version            # 表示されなければ手順 2 の途中で案内が出ます
nvidia-smi              # GPU 名と VRAM 総量が見えること
```

llama.cpp は記事シリーズで導入した `C:\tools\llama.cpp\b11960` を使います（`llama-bench.exe` と `llama-server.exe` があること）。

## 1. インストールと設定

```powershell
cd C:\tools
git clone https://github.com/iiiorz/llama-bench-helper.git
cd llama-bench-helper
python -m pip install .
lbh init
```

`lbh init` でできた `config.toml` を開き、`[paths]` を実機の値に直します。

```toml
llama_bin = "C:/tools/llama.cpp/b11960"
models_dir = "C:/tools/llama.cpp/models"
results_dir = "C:/tools/llama.cpp/results"
```

同じフォルダの `models.toml` には、既存 5 モデルのうち差分が要る 2 つを登録します（ファイル名は実物どおり）。

```toml
[[model]]
file = "Qwen3-Coder-30B-A3B-Instruct-Q4_K_M.gguf"
extra_bench_args = ["-ncmoe", "12"]
extra_server_args = ["-ncmoe", "12"]

[[model]]
file = "Qwen3VL-8B-Instruct-Q4_K_M.gguf"
mmproj = "mmproj-Qwen3VL-8B-Instruct-F16.gguf"
vision = true
```

確認:

```powershell
lbh models
```

合格条件: 既存の 5 モデルが並び、Qwen3VL の行に `vision` と mmproj 名が出ること。gpt-oss の行には server 側の追加引数として `--jinja` が自動で付いていること。

## 2. ダウンロードのスモーク（小さいモデル）

スモーク専用の小さいモデルを取得します。

```powershell
lbh download https://huggingface.co/Qwen/Qwen3-0.6B-GGUF
```

操作: ファイル一覧が出たら、`Qwen3-0.6B-Q8_0.gguf` の番号を入力して Enter。

合格条件:

- ダウンロード後に `OK Qwen3-0.6B-Q8_0.gguf (610.76 MiB)` のようにサイズ検証が OK になる
- `C:\tools\llama.cpp\models\.lbh-manifest.json` に repo 名・サイズ・sha256 が記録される
- `lbh models` に Qwen3-0.6B が増えている

※ `hf` が入っていない場合は、その場で「pip でインストールしますか？」と聞かれます。`y` で進めて問題ありません。

## 3. ベンチのスモーク

```powershell
lbh bench --models Qwen3-0.6B --reps 1
```

合格条件:

- `C:\tools\llama.cpp\results\<日時>-bench\bench-summary.csv` ができ、Qwen3-0.6B の行に pp / tg の数値と VRAM ピークが入っている
- 同フォルダに `report.md` が生成されている
- VRAM 警告が出ないこと（0.6B なので出るはずがありません。出たら報告してください）

## 4. 品質測定のスモーク（画像なし）

```powershell
lbh quality --models Qwen3-0.6B
```

合格条件:

- llama-server が自動で起動し、`server ready in ... s` と表示される
- p1〜p4 と default が `OK (... chars; ... checks passed)` 形式で表示される（チェックの合否そのものはモデルの実力次第なので、ここでは「実行できて記録される」ことが合格条件です）
- image の行が `SKIPPED (model has no mmproj)` になる（対象外モデルで画像が実行されないことの確認）
- 終了後にサーバーが止まる（タスクマネージャーに llama-server が残らない）
- `<日時>-quality\ai-eval-bundle.md` が生成され、プロンプトと回答が入っている

## 5. 品質測定のスモーク（画像あり、VL のみ）

記事シリーズで使ったテスト画像を指定します（パスは実機の置き場所に合わせてください）。

```powershell
lbh quality --models Qwen3VL --image C:\tools\llama.cpp\results\vl-test-image.png
```

合格条件: image の行が SKIPPED ではなく `OK` になり、回答ファイル `quality\Qwen3VL-8B-Instruct-Q4_K_M\Qwen3VL-8B-Instruct-Q4_K_M-image.txt` に図形の数が書かれていること。

## 6. エクスポートとレポートの確認

```powershell
lbh export --anonymize
lbh report
```

合格条件: 最新の quality 実行フォルダに `ai-eval-bundle.md`（Model A / B 表記）と `ai-eval-mapping.json` ができ、`report.md` に bench / quality の表が出ること。バンドルをそのまま ChatGPT や Claude に貼ると、モデルごとの採点表が返ってくることも確認してみてください。

## スモーク合格後: 本番条件での実行

```powershell
lbh bench --models Qwen3-14B-Q4_K_M,Qwen3-14B-Q8_0,gpt-oss-20b,Qwen3-Coder,Qwen3VL
lbh quality --models Qwen3-14B-Q4_K_M,Qwen3-14B-Q8_0,gpt-oss-20b,Qwen3-Coder,Qwen3VL --image C:\tools\llama.cpp\results\vl-test-image.png
```

注意:

- Coder は models.toml の `-ncmoe 12` が効いていないと VRAM 警告が出ます。警告が出たら無理に進めず、まず models.toml を確認してください
- `--yes` を付けると確認を飛ばして警告でも実行します。初回は付けないでください
- 記事の数値と比較する場合、条件（pp512 / tg128 / 3 回、-c 8192、KV q8_0）が既定で一致しています

## つまずいたとき

| 症状 | 対処 |
|---|---|
| `llama-bench not found` | config.toml の `paths.llama_bin` が b11960 フォルダを指しているか確認 |
| `hf CLI is required` | `python -m pip install "huggingface_hub[cli]"` を実行してから再試行 |
| サーバーが起動しない / ポート競合 | config.toml の `quality.port` を 18080 など空いている番号に変更 |
| 画面の日本語が文字化けする | Windows Terminal を使う、または環境変数 `PYTHONUTF8=1` を設定してから実行 |
| `python` が Microsoft Store に飛ぶ | Python を python.org から入れるか、`python` の代わりに `py` を使う（`py -m pip install .`） |
| VRAM 警告が想定外に出る | モデルファイルのサイズと GPU 総量を確認。見積もりは安全側に寄せてあるので、実測で収まる場合も出ます。その場合は内容を読んで判断してください |
