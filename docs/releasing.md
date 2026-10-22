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

## GitHub Releasesへ公開する

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
