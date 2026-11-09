# lbh Go 版（v2.0.0）

Python 版 lbh を Go で書き直した正式版です。**単一の実行ファイル（Windows は `lbh.exe`）だけで動き、Python のインストールは不要**になりました。

## Python 版との互換性

- コマンド構成は同じ：`init / models / download / bench / quality / export / report`、引数なしで対話メニュー
- `config.toml` / `models.toml` の形式、プロンプトセット JSON、結果フォルダの形式（`bench-summary.csv` / `quality-summary.csv` / `report.md` / `ai-eval-bundle.md` / `.lbh-manifest.json`）は互換を維持
- Python 版で作った設定ファイルと結果フォルダをそのまま使えます

- **パス区切りは `/` でも `\` でも OK**：config.toml / models.toml のパス値と `--dest` などのパス引数は、内部で OS の区切りに正規化されます。混在（`C:\tools/llama.cpp`）も可
  - 書き方のおすすめは `'C:\tools\llama.cpp'` のように**シングルクォート**（TOML のリテラル文字列）。ダブルクォートの中では `\` が特殊な意味を持ちます
  - ダブルクォートに生の `\` を書いてしまった場合も、読み込み時に自動修復して読みます（修復時は NOTE を表示）。`\t` がタブに化けるタイプも検出します

## Python 版からの変更点（改善）

- **ダウンロードが Go ネイティブ化**：`hf` CLI が不要になりました。Hugging Face API でファイル一覧を取得し、そのまま HTTPS でダウンロードします
  - 進捗表示（%、転送量、速度）つき
  - 中断しても `.part` ファイルから再開（レジューム）できます。再度同じコマンドを実行してください
  - サイズ照合と SHA256 マニフェスト記録は従来どおり
  - 限定公開リポジトリには環境変数 `HF_TOKEN` が使えます
- 起動が速くなりました

## 既知の差分・制限

- `--models` はカンマ区切りの1引数で渡してください（例：`--models StubModel,StubVL`）。Python 版のようなスペース区切りの複数値には未対応です
- 匿名化エクスポート（`export --anonymize`）の Model A/B への割り当て順は Python 版と一致しません（固定シードのシャッフルですが、乱数アルゴリズムが異なるため）
- Windows では `llama-server` の終了が強制終了（Kill）になります（Unix の SIGTERM 相当が無いため）。実用上の問題は想定していませんが、実機で確認をお願いします
- 署名なし exe のため、Windows SmartScreen の警告が出る場合があります

## 使い方

```
lbh.exe init                # config.toml / models.toml の雛形を生成
lbh.exe models              # 検出されたモデル一覧
lbh.exe download <HFのURL or user/repo>
lbh.exe bench --models <モデル名の断片>
lbh.exe quality --models <モデル名の断片> [--image 画像パス]
lbh.exe export [--anonymize]
lbh.exe report
lbh.exe                     # 対話メニュー
lbh.exe version
```

引き続き、別途 llama.cpp の `llama-bench.exe` / `llama-server.exe` が必要です（`config.toml` の `paths.llama_bin` か PATH で指定）。

## ビルド方法（ソースから）

要 Go 1.23 以上。

```
cd go
go build -o lbh ./cmd/lbh                        # 実行環境向け
GOOS=windows GOARCH=amd64 go build -o lbh.exe ./cmd/lbh   # Windows 向けクロスビルド
go test ./...                                     # 単体テスト
python3 stub_e2e.py ./lbh                         # スタブ実行ファイルでの通し検証
```
