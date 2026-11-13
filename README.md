# llama-bench-helper (lbh)

[English](README.en.md) | 日本語

<a href="https://ko-fi.com/tayura" target="_blank"><img height="36" style="border:0px;height:36px;" src="https://storage.ko-fi.com/cdn/kofi2.png?v=3" border="0" alt="Buy Me a Coffee at ko-fi.com" /></a>

llama.cpp 向けのベンチマーク補助 CLI です。モデルの入手、速度計測、品質確認を、予め設定した内容で回し、結果を出力します。

**v2.0.0 から Go 製の単一実行ファイルになりました。Python のインストールは不要です。** 設定ファイルや結果フォルダの形式は Python 版（v0.1.0）と互換で、そのまま引き継げます。

## できること

1. **モデルダウンロード**（`lbh download`）
   - Hugging Face の URL を渡すとファイル一覧とサイズを表示し、選んだファイルをそのままダウンロード（`hf` CLI は不要。lbh 本体が取得します）
   - 進捗表示つき。中断しても `.part` ファイルから再開できます（同じコマンドを再実行）
   - GGUF が含まれないリポジトリでは「llama.cpp ではそのまま使えません」と警告
   - mmproj（画像入力用ファイル）があれば一緒の取得を提案、取得後はサイズ検証と SHA256 をマニフェストに記録
   - 限定公開リポジトリには環境変数 `HF_TOKEN` が使えます
2. **ベンチマーク**（`lbh bench`）
   - 複数モデルを連続で llama-bench 実行。pp/tg のトークン数、繰り返し回数、`-ngl`、コンテキストなどを指定可能
   - モデル別の追加引数（例: `-ncmoe`）は models.toml で自動適用
   - 実行前に VRAM の見積もりが GPU 総量を超えそうな場合は警告
   - 実行中は VRAM を 1 秒間隔で記録し、結果は `samples_ts` の中央値で集計（結果は日時フォルダに保存）
3. **品質測定**（`lbh quality`）
   - 共通プロンプト集（同梱の既定セットは「です・ます要約」「コード生成」「論理パズル」「制約つき長文」「既定温度サンプル」「画像読み取り」）を全モデルに同じ条件で実行
   - プロンプトは JSON で差し替え・編集可能。文字数や指定語の出現回数などの**機械チェック**を定義しておくと自動で合否が付きます
   - 画像プロンプトは mmproj が対応付いたモデルでのみ実行
   - 終了後に **AI 評価用バンドル**（`ai-eval-bundle.md`）を自動生成。指示文・評価観点・全モデルの回答・機械チェック結果が 1 ファイルにまとまるので、そのまま ChatGPT や Claude に渡して採点してもらえます（モデル名の匿名化オプション付き）

## 前提

- llama.cpp のビルド済みバイナリ（`llama-bench` / `llama-server`）
- NVIDIA GPU + `nvidia-smi`（VRAM 記録を使う場合。無くても計測自体は動きます）
- Windows / Linux / macOS

## 導入

[Releases](https://github.com/iiiorz/llama-bench-helper/releases) から、自分の環境に合った zip をダウンロードして展開してください。

| 環境 | ファイル |
|---|---|
| Windows (x64) | `lbh-2.0.0-windows-amd64.zip` |
| Windows (ARM64) | `lbh-2.0.0-windows-arm64.zip` |
| macOS (Apple Silicon) | `lbh-2.0.0-darwin-arm64.zip` |
| Linux (x64) | `lbh-2.0.0-linux-amd64.zip` |

```powershell
# Windows (x64) の例。展開したフォルダで:
.\lbh.exe init   # config.toml と models.toml の雛形を作成
```

※ Windows では、署名なし exe のため SmartScreen の警告が出る場合があります。

`config.toml` の各項目にはコメントで説明が付いています。まず `[paths]` の `llama_bin`（llama.cpp のバイナリがあるフォルダ）と `models_dir` を自分の環境に合わせてください。

ソースからビルドする場合（Go 1.23 以上）と Python 版については、末尾の「開発者向け」を参照してください。

## 使い方

引数なしで `lbh` と打つと対話メニューが開きます。サブコマンドでも直接実行できます（Windows では `lbh.exe`）。

```powershell
# モデルのダウンロード（URL を貼る → 一覧から選択）
lbh download https://huggingface.co/Qwen/Qwen3-14B-GGUF

# 検出されたモデルの一覧
lbh models

# ベンチマーク（複数モデルをカンマ区切りで指定可）
lbh bench --models Qwen3-14B-Q4_K_M,gpt-oss-20b-MXFP4

# 品質測定（--image は画像テスト用。VL モデルだけに使われます）
lbh quality --models Qwen3-14B-Q4_K_M --image samples/vl-test-image.png

# AI 評価用バンドルの作り直し（匿名化つき。品質測定の最終フォルダに作成）
lbh export --anonymize

# 最新の実行結果から Markdown レポートを生成（品質測定の最終フォルダに作成）
lbh report
```

## 設定ファイル

- `config.toml` … パス、ベンチの既定条件、品質測定のポートや `max_tokens` の警告下限など全体設定
- `models.toml` … モデル別の差分だけを書く任意ファイル。mmproj の対応付け、部分オフロード用の引数、既知モデルのプリセット（gpt-oss の `--jinja` は自動で付与）

雛形は `config.example.toml` / `models.example.toml` を参照してください。全項目にコメントで説明が付いています。

パスの区切りは `/` でも `\` でも構いません。Windows のパスは `'C:\tools\llama.cpp'` のようにシングルクォートで書くのが確実です（TOML のダブルクォート内では `\` が特殊な意味を持ちます。生の `\` を書いてしまった場合も、読み込み時に自動修復します）。

## プロンプトセットの形式

```json
{
  "name": "my prompts",
  "prompts": [
    {
      "id": "p1", "title": "要約", "kind": "text",
      "text": "次の文章を3文以内・200文字以内で要約してください。…",
      "temperature": 0, "max_tokens": 2048,
      "checks": [
        {"type": "max_chars", "value": 200},
        {"type": "sentence_max", "value": 3}
      ]
    }
  ]
}
```

- `kind` は `text`（LLM温度を指定）／`default`（LLM温度を送らない）／`image`（画像つき、mmproj があるモデルだけ実行）
- 機械チェックの種類: `max_chars`、`min_chars`、`sentence_max`（「。」の数）、`count_equals` / `count_max`（指定語の出現回数）、`contains`、`not_contains`、`bullets_exactly`（`section` で見出し範囲を指定可）、`regex_count`
- `max_tokens` は思考型モデルでも「思考＋回答」が収まる余裕を持たせてください。小さすぎる値にはツール側でも警告が出ます（既定の下限は 1024、config で変更可）

## 結果ファイル

`results_dir` の下に実行ごとのフォルダができます。

- `<日時>-bench/bench-summary.csv` … pp / tg 中央値と VRAM ピーク
- `<日時>-quality/quality-summary.csv` … ロード時間、VRAM ピーク、プロンプトごとの状態
- `<日時>-quality/quality/<モデル>/` … プロンプトと回答の全文
- `<日時>-quality/ai-eval-bundle.md` … 他の AI に採点を頼むための一式
- `run-metadata.json` … 実行条件の記録（再現性のため）

## 開発者向け

Go 版のソースは `go/` にあります（ビルド方法や Python 版との差分は [go/README.md](go/README.md) を参照）。

```powershell
cd go
go test ./...                # 単体テスト
python stub_e2e.py ./lbh     # ダミーの llama-bench 等を使った通し確認
```

Python 版（v0.1.0 系）のソースも `src/` に残っています。使う場合は Python 3.11 以上で:

```powershell
pip install .
python -m unittest discover -s tests
```

## 注意
- 動作を保証するものではありません。
- VRAM使用量の事前見積もりは概算です。実測で収まる場合にも警告が出ることがあります。
- 品質測定の機械チェックは「数えられる条件」だけを対象にしています。内容の正しさは AIや目視で確認してください。

## 支援

このツールが役に立ったら、[Ko-fi](https://ko-fi.com/tayura) で開発支援をいただけると嬉しいです。

## ライセンス

MIT
