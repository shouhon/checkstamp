# checkstamp

検証コマンドの結果と、明示したファイルの状態を結び付けて保存する、Windows向けの非対話型CLIです。仕様の正本は[要件定義書](要件定義書.md)です。

現在はGoの開発基盤を整えた段階です。日本語の `--help` と `--version` を実装しています。`run`・`status`、入力の収集、記録、JSON出力は[開発計画](docs/development-plan.md)に沿って実装します。現在の `run` は125、`status` は2で終了し、検証成功とは扱いません。

## 開発環境

- Windows 11・PowerShell 7。
- Go 1.27.0以上。同じ条件での再現には、今回検証したGo 1.27.2を使用します。
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

## 構成

| 対象 | 役割 |
| --- | --- |
| `cmd/checkstamp` | OSの入出力と終了コードをCLIへ接続する入口 |
| `internal/cli` | 日本語の案内、引数エラー、未実装コマンドの診断とテスト |
| `internal/version/VERSION` | CLIの版番号の正本。ビルド時に埋め込む |
| `scripts` | PowerShellによる検査とビルドの入口 |
| `docs/development-plan.md` | 実装順序、未決事項、受入確認 |
| `AGENTS.md` | 変更時に守る実装・検査・版管理の規則 |
| `dist` | ローカルでビルドする実行ファイル。Git管理対象外 |
| `.cache` | 作業環境で必要な場合の一時ファイル。Git管理対象外 |

外部依存がないため `go.sum` はありません。ソースは [GitHubリポジトリ](https://github.com/shouhon/checkstamp)で管理します。CLIの実行にはGitを必要としません。公開用のライセンスは未選択です。

## 版番号と配布

CLIは単一の実行ファイルとしてビルドします。版番号は `internal/version/VERSION` の1か所だけで管理し、`--version` は埋め込んだ値を表示します。`build.ps1` が正本と実行ファイルの一致を確認します。開発用成果物のファイル名は固定です。公開・署名・配布の自動処理はありません。

現在の版は `0.1.0` です。更新条件は [AGENTS.md](AGENTS.md#版管理)を参照してください。要件書の文書版とCLIの版番号は別に管理します。保存・JSON形式の初版は、実装時に要件どおり `schema_version: 1` とします。

## 有効性の範囲

完成後の判定も、指定した入力と記録できた実行条件に限ります。未指定のファイル、環境変数、外部サービスの変化までは確認しません。引数・出力・環境変数・入力ファイルの本文は記録に保存しない設計です。詳細と例外は[要件定義書](要件定義書.md)を参照してください。

## 構成の根拠

Go公式の[モジュール構成](https://go.dev/doc/modules/layout)、[go.modの仕様](https://go.dev/doc/modules/gomod-ref)、[Goコマンド](https://pkg.go.dev/cmd/go@go1.27.2)を確認し、単一モジュールと `internal` による構成を採用しています。
