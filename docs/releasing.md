# リリース手順

## 配布先と形式

- ソースコード: [GitHubリポジトリ](https://github.com/shouhon/checkstamp)。
- 実行ファイル: [GitHub Releases](https://github.com/shouhon/checkstamp/releases)。
- 対象: Windows 11（x64）。PowerShell 7を使用する。
- ライセンス: [MIT](../LICENSE)。ZIPには実行ファイルとライセンスを同梱する。
- 導入方法: ScoopによるURL指定、またはZIPの手動展開。

版番号の正本は `internal/version/VERSION`、タグ名は `v<版番号>` とする。更新条件は [AGENTS.md](../AGENTS.md#版管理)に従い、同じ版の公開済みファイルを別内容で置き換えない。保存・JSON形式の `schema_version` は製品の版と独立して管理する。

## 手元で配布物を作成する

公開対象のソースで、Windows/amd64用のGoとPowerShell 7を使用して実行する。

```powershell
pwsh -NoProfile -File .\scripts\package.ps1
```

スクリプトは最初に `scripts/check.ps1` の必須検査とビルドを実行し、成功した場合だけ次のファイルを `dist/release/<版番号>/` に生成する。Goのビルド対象がWindows/amd64以外なら停止する。

| ファイル | 内容 |
| --- | --- |
| `checkstamp_<版番号>_windows_amd64.zip` | `checkstamp.exe` と `LICENSE` |
| `checkstamp.json` | 同じ版のZIPのURL・SHA-256・実行ファイル名を指定するScoop定義 |
| `checksums.txt` | ZIPとScoop定義のSHA-256 |

版番号とハッシュは生成時に読み取り・算出するため、Scoop定義を手作業で同期する必要はない。生成物はGit管理対象外の `dist/` に置く。スクリプトはタグの作成、署名、アップロード、Releaseの公開を行わない。

公開前にZIPを別ディレクトリへ展開し、ライセンスの同梱、`--version` と `--help`、実際の `run` と `status` を確認する。Scoop定義と `checksums.txt` のハッシュが生成したファイルに一致することも確認する。配布物は署名していない。異なる環境・再ビルド間でのハッシュ一致は保証しない。

## mainへのpushで自動公開する

[GitHub Actionsのワークフロー](../.github/workflows/ci.yml)は、`main` へのpushと `main` 宛てのプルリクエストでWindows上の必須検査と配布準備を実行する。Goの版は `go.mod` から取得し、検査には既存の `scripts/package.ps1` を使用する。

`main` へのpushで、push前後の `internal/version/VERSION` の値が変わった場合だけReleaseを公開する。複数コミットをまとめてpushした場合も、最後のコミットだけでなくpush全体の前後を比較する。改行だけの変更は版更新と扱わない。初回pushや版番号ファイルの新規追加では現在の版を公開対象とする。比較元を取得できない場合や版番号が不正な場合は停止する。

1. 製品の変更と、規則に従った `internal/version/VERSION` の更新を同じpushに含める。
2. `main` にpushする。プルリクエストの場合は `main` へのマージ後のpushが公開の契機となる。
3. [Actions](https://github.com/shouhon/checkstamp/actions)の「検査とリリース」で、検査と公開の成功を確認する。
4. [Releases](https://github.com/shouhon/checkstamp/releases)で、`v<版番号>` と3つの添付ファイルを確認する。

公開ジョブは同じ実行で検査・生成した配布物を受け取り、検査したコミットにタグを固定する。Releaseを下書きで作成し、ZIP、`checkstamp.json`、`checksums.txt` の添付に成功してから公開する。説明はGitHubの変更履歴から自動生成する。最新版はGitHubの [`make_latest=legacy`](https://docs.github.com/en/rest/releases/releases#update-a-release)による作成日時・版番号の判定を使う。

公開処理は[同時実行の待ち行列](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency)で直列化し、進行中の公開を次のpushで取り消さない。`queue: max` により最大100件の待機を許容する。検査ジョブの権限は `contents: read`、公開ジョブだけは `contents: write` とする。認証にはGitHubが発行する `GITHUB_TOKEN` を使い、個人のアクセストークンや追加の秘密値は不要。GitHub側でActionsの実行とワークフローによる書き込みが許可されている必要がある。

### 失敗と再実行

- 検査・配布物の保存・取得に失敗した場合、公開へ進まない。
- 添付に失敗したReleaseは下書きのまま残す。同じ実行を再実行すると、同じコミットのタグ・下書きを再利用して添付をやり直す。
- 既に同じコミットで公開済みの版は変更しない。同じタグが別コミットを指す場合は停止する。公開済みの訂正には新しい版番号を使う。
- 版番号を変えない次のpushでは、前の公開失敗を再試行しない。失敗したActionsの実行を再実行する。配布物の保存期間は7日間のため、期限後はすべてのジョブを再実行して配布物を作り直す。
- 強制pushなどで比較元コミットを取得できなければ自動公開は停止する。履歴と版番号を確認し、必要なら以下の手動手順を使う。

このワークフローによる `main` の版更新時のタグ作成・配布物の添付・Release公開は、自動公開の依頼に基づく運用範囲とする。ワークフロー導入だけでは版番号を上げず、既存Releaseを再公開しない。

## GitHub Releasesへ手動で公開する

タグの作成・プッシュ、Releaseへのアップロード・公開は、対象と操作が明示的に認可されたときに実行する。

1. 検査済みのソースをコミットし、公開するコミットと版番号を確認する。
2. そのコミットに `v<版番号>` タグを作成し、GitHubへプッシュする。
3. 同じタグのReleaseを作成し、生成したZIP、`checkstamp.json`、`checksums.txt` の3ファイルを添付する。
4. 変更内容、対応環境、既知の制約をReleaseの説明へ記載する。
5. Releaseを公開し、最新版への導入で利用する場合はGitHubの最新Releaseに設定する。
6. 公開URLからファイルを取得できることと、[READMEのScoop導入手順](../README.md#scoop)が成功することを確認する。

Scoopは[URLで公開された定義ファイル](https://github.com/ScoopInstaller/Scoop/wiki/Creating-an-app-manifest)から導入できる。定義は[公式の形式](https://github.com/ScoopInstaller/Scoop/wiki/App-Manifests)に従い、ダウンロード先を版ごとのURLへ固定し、SHA-256で検証する。READMEに記載する `releases/latest/download/checkstamp.json` は、Releaseの公開と同名のファイル添付が済んでから利用できる。

## 以前の版へ戻す

以前の版が公開されている場合は、[Releases](https://github.com/shouhon/checkstamp/releases)からその版のZIPを取得し、別ディレクトリへ展開する。その実行ファイルをパスで直接指定して使う。各版のZIP・ハッシュとタグは保持する。

作業ディレクトリの `.checkstamp` は保持する。以前の版で読めない保存形式は `unknown` として扱い、有効と推定しない。公開内容の訂正は、新しい版を作成して配布する。
