# サンプル

## VL テスト画像（画像読み取りプロンプト用）

`vl-test-image.png.b64` は、品質測定の画像プロンプトで使うテスト画像（PNG）を Base64 エンコードしたものです。図形の数や書かれている文字がプログラムで描画した通りに確定しているため、正解表（`vl-test-image-ground-truth.md`）と照らして読み取り精度を採点できます。

> なぜ .b64 形式なのか: このリポジトリへのファイル追加に使っている GitHub API 経路がテキストしか扱えないためです。復元は 1 コマンドででき、復元後の PNG は原本とバイト単位で一致します。

復元方法（samples フォルダ内で実行）:

```powershell
python -c "import base64; open('vl-test-image.png','wb').write(base64.b64decode(open('vl-test-image.png.b64','rb').read()))"
```

使い方:

```powershell
lbh quality --models <VLモデル> --image samples\vl-test-image.png
```
