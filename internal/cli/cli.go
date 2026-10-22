// Package cli は、日本語の案内とCLIの終了コードを扱う。
package cli

import (
	"fmt"
	"io"

	"github.com/shouhon/checkstamp/internal/version"
)

const helpText = `checkstamp - 検証結果と指定した入力の状態を結び付けるCLI

現在は入力収集と記録保存の内部基盤まで実装しています。
--help と --version を利用できます。run と status の本体は未実装です。
CLIから検証の実行や記録の保存は行いません。

使い方:
  checkstamp --help
  checkstamp --version

実装予定の使い方:
  checkstamp run --label <名前> --input <対象> [--input <対象> ...] [--json] -- <コマンド> [引数...]
  checkstamp status <実行ID> [--json]

判定の有効範囲:
  指定した入力と記録できた実行条件だけを確認します。
  未指定のファイル、環境変数や外部サービスの同一性は保証しません。
  実行中に変更されて元に戻った内容の検出も保証しません。

実装予定の記録先:
  作業ディレクトリの .checkstamp/runs/<実行ID>.json
  .git の管理領域と .checkstamp は入力の収集対象から除外します。
  Gitで管理する場合は、必要に応じて .gitignore に /.checkstamp/ を追加してください。
  checkstamp 自身が .gitignore を変更することはありません。

仕様は 要件定義書.md、実装順は docs/development-plan.md を参照してください。
`

// Run は案内を出力して終了コードを返す。呼び出し元のプロセスは終了しない。
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return invalidArguments(stderr)
	}

	switch args[0] {
	case "--help":
		if len(args) != 1 {
			return invalidArguments(stderr)
		}
		return writeOutput(stdout, stderr, helpText)
	case "--version":
		if len(args) != 1 {
			return invalidArguments(stderr)
		}
		return writeOutput(stdout, stderr, "checkstamp "+version.String()+"\n")
	case "run":
		fmt.Fprintln(stderr, "checkstamp: run は未実装です。コマンドは起動していません。")
		return 125
	case "status":
		fmt.Fprintln(stderr, "checkstamp: status は未実装です。記録の有効性は確認できません。")
		return 2
	default:
		return invalidArguments(stderr)
	}
}

func invalidArguments(stderr io.Writer) int {
	// 未知の引数には機密値が含まれ得るため、生の値を診断へ転記しない。
	fmt.Fprintln(stderr, "checkstamp: 引数が正しくありません。使い方は --help で確認してください。")
	return 64
}

func writeOutput(stdout, stderr io.Writer, value string) int {
	if _, err := io.WriteString(stdout, value); err != nil {
		fmt.Fprintln(stderr, "checkstamp: 標準出力へ書き込めませんでした。")
		return 125
	}
	return 0
}
