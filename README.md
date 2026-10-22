# checkstamp

検証コマンドの結果と、明示したファイルの状態を結び付けて保存する、Windows向けの非対話型CLIです。仕様の正本は[要件定義書](docs/requirements.md)です。

`run` で検証コマンドを実行・記録し、`status` で現在の入力と照合できます。日本語の案内とJSON出力、Windowsでの中断と子孫プロセスの管理を実装しています。実装範囲と受入確認は[開発計画](docs/development-plan.md)にまとめています。

## 開発環境

- Windows 11・PowerShell 7。
- Go 1.27.0以上。同じ条件での再現には、今回検証したGo 1.27.3を使用します。
- 外部のGoモジュール、追加の開発ツール、環境変数の設定は不要です。

`go.mod` の `go` 指定は最低対応版です。既存のGoを使用しており、セットアップのためのインストールは行っていません。モジュール名は、[GitHubリポジトリ](https://github.com/shouhon/checkstamp)に対応する `github.com/shouhon/checkstamp` です。

## 検査と実行

プロジェクトのルートで実行します。

```powershell
go version
pwsh -NoProfile -File .\scripts\check.ps1
.\dist\checkstamp.exe --help
.\dist\checkstamp.exe --version
```

`check.ps1` は次を順に実行し、失敗した時点で停止します。検査中にソースを整形し直すことはありません。

1. `go mod tidy -diff` によるモジュール定義の整合性確認。
2. パッケージ内のGoファイルに対する `gofmt -l` の確認。
3. `go vet ./...` による静的検査。
4. `go test -count=1 ./...` によるテスト。
5. 実行ファイルのビルド、版番号の照合、ヘルプ表示の確認。

ビルドだけを行う場合は次を使います。成果物は `dist/checkstamp.exe` です。

```powershell
pwsh -NoProfile -File .\scripts\build.ps1
```

ソースから案内を表示することもできます。

```powershell
go run ./cmd/checkstamp --help
go run ./cmd/checkstamp --version
```

Goファイルを整形するときは `gofmt -w ./cmd ./internal` を使用します。終了コードを調べる試験では、`go run` が子の終了コードをそのまま返すとは限らないため、ビルドした実行ファイルを直接呼び出します。

## 検証の実行と記録

検証したい作業ディレクトリで、入力と実行ファイルを明示します。次はcheckstamp自身のソースを検証する例です。

```powershell
.\dist\checkstamp.exe run --label unit --input cmd --input internal --input go.mod -- go test ./...
```

通常は子プロセスの標準出力と標準エラーを元の出力先へ流し、実行IDと判定を標準エラーへ出します。`--json` を `--` より前へ付けると、子の両出力を標準エラーへ流し、標準出力へ結果JSONオブジェクト1件を出します。

```powershell
$result = .\dist\checkstamp.exe run --label unit --input cmd --input internal --input go.mod --json -- go test ./... | ConvertFrom-Json
.\dist\checkstamp.exe status $result.run_id --json
```

`--label` は空でない名前、`--input` は1件以上の相対パスが必須です。`--` 以降を子のコマンドと引数として渡します。通常の実行ファイル（`.exe` / `.com`）を直接起動し、シェルを暗黙に補いません。PowerShellスクリプトは、実行するプログラムとして `pwsh -NoProfile -File` を明示してください。

起動後は子の終了コードをそのまま返します。起動前の入力読取・記録保存の失敗や起動不能は125、引数の誤りは64です。起動後の保存・再読取に失敗しても子の終了コードは変えず、判定は `unknown` とします。終了コード0だけで記録の成立を判断せず、`status` で確認してください。

Ctrl+C / Ctrl+Breakで中断を受けると子のプロセス群へ通知し、2秒後または再度の中断でジョブ全体を終了します。通常終了でも残った子孫を終了し、その終了を確認してから入力を再収集します。checkstampが強制終了された場合もジョブの子孫を終了し、開始記録は `unknown` のまま残します。[実行と中断の方式](docs/input-and-record.md#実行と中断)に制約を記載しています。

## 保存済み記録の照合

記録を作成した作業ディレクトリで、実行IDを指定します。次はビルドした実行ファイルのパスを先に保持する例です。実行IDは保存済みの小文字16進数32文字に置き換えます。

```powershell
# checkstampのリポジトリで実行します。
$checkstamp = (Resolve-Path .\dist\checkstamp.exe).Path

# 記録を作成した作業ディレクトリへ移動してから実行します。
$runId = '保存済みの実行ID'
& $checkstamp status $runId
& $checkstamp status $runId --json
```

`status` は検証コマンドを再実行せず、入力や記録を変更しません。現在の入力が記録後に変わっていれば `stale`、削除ではなく読み取りができない場合は `unknown` となります。実行時に記録した失敗・中断・不安定な入力は、現在の内容が一致しても成功へ変わりません。

終了コードは `valid` が0、`stale`・`failed`・`unstable`・`interrupted` が1、`unknown` が2、引数の誤りが64です。`--json` では標準出力にJSONオブジェクト1件を出し、確認不能や引数エラーの診断は標準エラーへ出します。結果の項目と差分の基準は[入力と記録の実装](docs/input-and-record.md#現在の入力との照合)を参照してください。

## 構成

| 対象 | 役割 |
| --- | --- |
| `cmd/checkstamp` | OSの入出力と終了コードをCLIへ接続する入口 |
| `internal/cli` | 日本語の案内、`run`・`status` の引数、実行と照合、結果出力と終了コード |
| `internal/input` | 明示した入力の収集、種類とSHA-256、追加・削除・変更の差分 |
| `internal/record` | 保存先の所有確認、開始・完了記録の保存と形式検査 |
| `internal/process` | Windowsのジョブ内での起動、中断通知、子孫の終了確認 |
| `internal/workspace` | Windowsの相対パス、再解析ポイント、作業範囲内の読取 |
| `internal/version/VERSION` | CLIの版番号の正本。ビルド時に埋め込む |
| `scripts` | PowerShellによる検査とビルドの入口 |
| `docs/development-plan.md` | 実装順序、未決事項、受入確認 |
| `AGENTS.md` | 変更時に守る実装・検査・版管理の規則 |
| `dist` | ローカルでビルドする実行ファイル。Git管理対象外 |
| `.cache` | 作業環境で必要な場合の一時ファイル。Git管理対象外 |

外部依存がないため `go.sum` はありません。ソースは [GitHubリポジトリ](https://github.com/shouhon/checkstamp)で管理します。CLIの実行にはGitを必要としません。公開用のライセンスは未選択です。

## 版番号と配布

CLIは単一の実行ファイルとしてビルドします。版番号は `internal/version/VERSION` の1か所だけで管理し、`--version` は埋め込んだ値を表示します。`build.ps1` が正本と実行ファイルの一致を確認します。開発用成果物のファイル名は固定です。公開・署名・配布の自動処理はありません。

現在の版は `0.4.0` です。更新条件は [AGENTS.md](AGENTS.md#版管理)を参照してください。要件書の文書版とCLIの版番号は別に管理します。保存・JSON形式は要件どおり `schema_version: 1` です。収集・保存・実行・照合方式と現在の制約は[入力と記録の実装](docs/input-and-record.md)にまとめています。

## 有効性の範囲

判定は、指定した入力と記録できた実行条件に限ります。未指定のファイル、環境変数、外部サービスの変化までは確認しません。引数・出力・環境変数・入力ファイルの本文は記録に保存しません。詳細と例外は[要件定義書](docs/requirements.md)を参照してください。

## 構成の根拠

Go公式の[モジュール構成](https://go.dev/doc/modules/layout)、[go.modの仕様](https://go.dev/doc/modules/gomod-ref)、[Goコマンド](https://pkg.go.dev/cmd/go@go1.27.2)を確認し、単一モジュールと `internal` による構成を採用しています。
