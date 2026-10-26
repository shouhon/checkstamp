# checkstamp

検証コマンドの結果と、そのときのファイルの状態を結び付けて保存する、Windows向けのコマンドラインツールです。

テストに成功した後でコードを修正すると、以前の検証結果は現在のコードに対応しなくなります。checkstampは、明示したファイルのSHA-256と実行結果を記録し、後から現在のファイルと照合できます。

```powershell
# Goプロジェクトのルートで、テストを実行して記録します。
checkstamp run --label unit --input cmd --input internal --input go.mod -- go test ./...

# 表示された実行IDで、現在のファイルと照合します。
$runId = 'ここを表示された実行IDに置き換える'
checkstamp status $runId
```

## 特徴

- 検証の前後と照合時のファイルを比較し、追加・削除・変更を検出します。
- `status` は検証コマンドを再実行せず、ファイルと記録を読み取ります。
- 子プロセスの終了コードを保ち、日本語の案内とJSON出力に対応します。
- Windowsの中断通知と子孫プロセスの終了を扱います。
- 引数の生値、環境変数の値、標準出力・標準エラー、入力ファイルの本文を記録へ保存しません。
- 単体の実行ファイルで動作し、checkstamp自体の実行にGoやGitは不要です。

## 動作環境

- Windows 11（x64）
- PowerShell 7
- 実行する検証コマンドと、その実行環境

## インストール

ソースコードは[GitHub](https://github.com/shouhon/checkstamp)、実行ファイルは[GitHub Releases](https://github.com/shouhon/checkstamp/releases)で配布します。

Scoopと手動導入には、配布ファイルを添付した公開Releaseが必要です。公開前は[ソースからビルド](#開発)してください。

### Scoop

[Scoop](https://scoop.sh/)を導入済みのPowerShellで実行します。Releaseに添付する定義ファイルのURLを指定するため、追加のバケット登録は不要です。[URLを指定する導入方法](https://github.com/ScoopInstaller/Scoop/wiki/Creating-an-app-manifest)はScoopの公式文書を参照してください。

```powershell
scoop install https://github.com/shouhon/checkstamp/releases/latest/download/checkstamp.json
checkstamp --version
```

更新とアンインストールは次のとおりです。

```powershell
scoop update checkstamp
scoop uninstall checkstamp
```

### 手動ダウンロード

1. [Releases](https://github.com/shouhon/checkstamp/releases)から `checkstamp_<版番号>_windows_amd64.zip` をダウンロードします。
2. ZIPを展開し、`checkstamp.exe` と `LICENSE` を同じディレクトリに置きます。
3. 配置先を `PATH` に追加するか、実行ファイルのパスを直接指定します。

```powershell
.\checkstamp.exe --version
.\checkstamp.exe --help
```

同じReleaseの `checksums.txt` と、`Get-FileHash -Algorithm SHA256 <ZIPのパス>` の結果で、ダウンロードしたZIPの整合性を確認できます。

## 使い方

### 検証を実行して記録する

検証したいプロジェクトのルートで、入力と実行するコマンドを明示します。次は、このリポジトリのソースを検証する例です。

```powershell
checkstamp run --label unit --input cmd --input internal --input go.mod -- go test ./...
```

`--label` は空でない名前、`--input` は1件以上の相対パスが必須です。ファイルまたはディレクトリを指定できます。`--` 以降を子のコマンドと引数として渡します。別のプロジェクトでは、その検証に必要な入力へ置き換えてください。

通常は子プロセスの標準出力と標準エラーを元の出力先へ流し、実行IDと判定を標準エラーへ表示します。記録は、実行時の作業ディレクトリの `.checkstamp/runs/<実行ID>.json` に保存します。同じラベルでも過去の記録を上書きしません。

実行ファイル（`.exe` / `.com`）を直接起動します。PowerShellスクリプトを実行する場合は、`-- pwsh -NoProfile -File .\scripts\test.ps1` のようにPowerShellを明示します。

### 現在の入力と照合する

記録を作成した作業ディレクトリで、表示された実行IDを指定します。

```powershell
$runId = 'ここを表示された実行IDに置き換える'
checkstamp status $runId
```

| 判定 | 意味 | `status` の終了コード |
| --- | --- | --- |
| `valid` | 検証に成功し、実行前後と現在の入力が一致している | 0 |
| `stale` | 記録後に入力が変わった | 1 |
| `failed` | 検証コマンドが0以外で終了した | 1 |
| `unstable` | 検証の実行前後で入力が変わった | 1 |
| `interrupted` | 中断を記録した | 1 |
| `unknown` | 記録が未完了・破損・未対応、または入力などを読み取れない | 2 |

複数の状態が重なる場合は、`unknown` → `interrupted` → `failed` → `unstable` → `stale` → `valid` の順で判定します。引数の誤りは64です。

`run` は起動後の子プロセスの終了コードをそのまま返します。起動前の入力読取・記録保存の失敗や起動不能は125、引数の誤りは64です。起動後の記録失敗でも子の終了コードを変えないため、記録が成立したかは `status` で確認してください。

### JSONで受け取る

入力が多い場合は `--json-summary` を使うと、入力一覧や差分の明細を含めない短いJSONオブジェクトを1件受け取れます。`run` の子プロセスの標準出力・標準エラーは、どちらも標準エラーへ流します。

```powershell
$result = checkstamp run --label unit --input cmd --input internal --input go.mod --json-summary -- go test ./... | ConvertFrom-Json
checkstamp status $result.run_id --json-summary
```

照合時の要約は次の形式です。

```json
{"schema_version":1,"command":"status","run_id":"0123456789abcdef0123456789abcdef","status":"valid","child_exit_code":0,"change_count":0,"current_change_count":0,"issues":[]}
```

`change_count` は実行前後、`current_change_count` は最後の記録から現在までの変更件数です。変更されたパスをファイル・ディレクトリとも各1件として数え、未確認の件数と取得できない終了コードは `null` です。`run` は現在との再照合を行わないため、`current_change_count` は常に `null` です。問題は `issues` の識別子と日本語の説明で確認できます。

実行前後の詳しい入力一覧と差分は、要約モードでも `.checkstamp/runs/<実行ID>.json` に保存します。現在の一覧や差分の明細も必要な場合は、従来の `checkstamp status $result.run_id --json` を使ってください。`run --json` の詳細出力も維持しています。`--json` と `--json-summary` は併用できません。

### 中断と記録の管理

Ctrl+C / Ctrl+Breakで子のプロセス群へ中断を通知し、2秒後または再度の中断でジョブ全体を終了します。通常終了でも残った子孫を終了し、その終了を確認してから入力を再収集します。checkstampが強制終了された場合は、開始記録を `unknown` として残します。方式と制約は[実行と中断](docs/input-and-record.md#実行と中断)を参照してください。

Gitで管理するプロジェクトでは、必要に応じて `.gitignore` に `/.checkstamp/` を追加してください。checkstamp自身が `.gitignore` やプロジェクト設定を変更することはありません。

## 制約

- 判定は、指定した入力と記録できた実行条件に限ります。未指定のファイル、環境変数、外部サービスの変化は確認しません。
- 依存入力の自動推定、結果を使った実行省略、自動再検証は行いません。
- 実行中に変更され、元に戻った内容の検出は保証しません。
- 入力内のシンボリックリンクや特殊ファイルは未対応です。作業ディレクトリの範囲外も指定できません。
- `.git` の管理領域と `.checkstamp` は入力の収集対象から除外します。

詳しい条件は[要件定義書](docs/requirements.md)と[入力・記録の仕様](docs/input-and-record.md)を参照してください。

## 開発

Windows 11・PowerShell 7・Go 1.27.0以上・Gitを使用します。確認済みのGo版は1.27.3です。外部のGoモジュールは使用していません。

```powershell
git clone https://github.com/shouhon/checkstamp.git
cd checkstamp
pwsh -NoProfile -File .\scripts\check.ps1
.\dist\checkstamp.exe --help
```

`check.ps1` はモジュール整合性、整形、静的検査、テスト、自動リリースの判定・失敗処理、ビルド、版表示、ヘルプ表示を確認し、失敗時に停止します。自動リリースの試験は一時的なGitリポジトリとGitHub CLIの代替関数を使い、外部へ書き込みません。ソースを自動で整形し直すことはありません。ビルドだけなら次を実行します。

```powershell
pwsh -NoProfile -File .\scripts\build.ps1
```

成果物は `dist/checkstamp.exe` です。Goファイルの整形には `gofmt -w ./cmd ./internal` を使用します。終了コードの確認には、`go run` ではなくビルドした実行ファイルを使用してください。

版番号の正本は `internal/version/VERSION`、保存・JSON形式は `schema_version: 1` です。配布物の作成方法は[リリース手順](docs/releasing.md)、実装と受入確認は[開発計画](docs/development-plan.md)、変更時の規則は[AGENTS.md](AGENTS.md)にまとめています。

GitHub Actionsは `main` へのpushと `main` 宛てのプルリクエストで検査・配布準備を実行します。`main` へのpushで版番号の値が変わった場合は、検査成功後にタグとGitHub Releaseを作成し、ZIP・Scoop定義・ハッシュ一覧を公開します。公開済みの同じ版は上書きしません。失敗時の再実行は[自動公開の手順](docs/releasing.md#mainへのpushで自動公開する)を参照してください。

## 不具合報告・改善提案

[GitHub Issues](https://github.com/shouhon/checkstamp/issues)で受け付けます。`checkstamp --version`、Windows・PowerShellの版、再現手順、期待した結果と実際の結果を添えてください。引数、出力、記録に含まれる機密情報は除いてください。

## ライセンス

[MIT License](LICENSE)
