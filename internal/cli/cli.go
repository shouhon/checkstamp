// Package cli は、日本語の案内とCLIの終了コードを扱う。
package cli

import (
	"fmt"
	"io"

	"github.com/shouhon/checkstamp/internal/version"
)

const helpText = `checkstamp - 検証結果と指定した入力の状態を結び付けるCLI

検証コマンドを run で実行・記録し、現在の入力を status で照合できます。

使い方:
  checkstamp --help
  checkstamp --version
  checkstamp status <実行ID> [--json]
  checkstamp run --label <名前> --input <対象> [--input <対象> ...] [--json] -- <コマンド> [引数...]

run の終了コードと出力:
  起動後は子プロセスの終了コードをそのまま返します。
  起動前の失敗・起動不能は125、引数の誤りは64です。
  通常は子の出力先を保ち、実行IDと判定を標準エラーへ出します。
  --json は子の両出力を標準エラーへ流し、標準出力へ結果JSON1件を出します。
  実行ファイル（.exe / .com）を直接起動します。スクリプトは明示的にpwshなどへ渡してください。
  Ctrl+C / Ctrl+Breakで中断を通知し、2秒後または再度の中断で子孫を終了します。
  起動後の記録失敗でも終了コードを変えません。記録の成立はstatusで確認してください。

status の終了コード:
  0: valid（指定範囲で有効）
  1: stale / failed / unstable / interrupted
  2: unknown（未完了・破損・未対応・読取不能など）
  64: 引数の誤り
  同じ作業ディレクトリから照合します。再実行や記録の書換えは行いません。
  --json は結果JSONオブジェクト1件を標準出力へ出します。

判定の有効範囲:
  指定した入力と記録できた実行条件だけを確認します。
  未指定のファイル、環境変数や外部サービスの同一性は保証しません。
  実行中に変更されて元に戻った内容の検出も保証しません。

記録先:
  作業ディレクトリの .checkstamp/runs/<実行ID>.json
  .git の管理領域と .checkstamp は入力の収集対象から除外します。
  Gitで管理する場合は、必要に応じて .gitignore に /.checkstamp/ を追加してください。
  checkstamp 自身が .gitignore を変更することはありません。

仕様は docs/requirements.md、実装順は docs/development-plan.md を参照してください。
`

// Run は案内、検証コマンドの実行、記録の照合を行い終了コードを返す。自身は終了しない。
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
		return runCommand(args[1:], stdout, stderr)
	case "status":
		return runStatus(args[1:], stdout, stderr)
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
