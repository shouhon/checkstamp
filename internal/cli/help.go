package cli

const runHelpText = `checkstamp run - 検証コマンドを実行して入力の状態と結果を記録する

使い方:
  checkstamp run --label <名前> --input <対象> [--input <対象> ...] [--exclude <パターン> ...] [--json | --json-summary | --json-changes] -- <コマンド> [引数...]
  checkstamp run --help / -h

オプション:
  --label <名前>       必須。空でないラベル。同じラベルでも記録を上書きしません。
  --input <対象>       必須。作業ディレクトリ内の相対パス。複数指定できます。
  --exclude <パターン> 任意。作業ディレクトリ基準の / 区切り。複数指定できます。
                      *・?・文字クラスと、構成要素全体の ** が使えます。
                      明示入力自身・祖先の除外と、探索する項目のリンクは拒否します。
  --json              入力一覧と差分を含む詳細JSONを返します。
  --json-summary      入力一覧や差分の明細を含めず、判定・件数・問題を返します。
  --json-changes      要約と変更の明細だけを返します。現在の差分はnullです。
  JSONモードは併用・重複できません。どの表示でも詳細な記録を保存します。

使用例:
  checkstamp run --label unit --input cmd --input internal --input go.mod -- go test ./...

-- 以降の --help・-h を含むすべての引数は、子コマンドへそのまま渡します。
実行ファイル（.exe / .com）を直接起動します。スクリプトはpwshなどへ渡してください。
通常は子の出力先を保ち、実行IDと判定は標準エラーへ出します。
全JSONモードでは子の両出力を標準エラーへ流し、標準出力へJSON1件を返します。
起動後は子の終了コードを保ちます。起動前の失敗・起動不能は125、引数エラーは64です。
Ctrl+C / Ctrl+Breakで中断を通知し、2秒後または再度の中断で子孫を終了します。
記録先は .checkstamp/runs/<実行ID>.json です。現在の有効性は status で確認してください。
引数・環境変数・出力・入力本文の生値は保存しません。
確認範囲は明示した入力と記録できた実行条件です。未指定入力や外部条件は保証しません。
ヘルプは単独で指定します。コマンドの実行や記録の作成・更新は行いません。
`

const statusHelpText = `checkstamp status - 保存記録と現在の入力を照合する

使い方:
  checkstamp status <実行ID> [--json | --json-summary | --json-changes]
  checkstamp status --help / -h

引数とオプション:
  <実行ID>        run が表示した小文字16進数32文字のID。list でも探せます。
  --json          保存時と現在の入力一覧・差分を含む詳細JSONを返します。
  --json-summary  判定・子の終了コード・実行前後と現在の変更件数・問題を返します。
  --json-changes  要約と changes（実行前後）・current_changes（現在）を返します。
  JSONモードはいずれか1回だけ、実行IDの後ろに指定します。
  確認済みで変更がなければ []、未確認の差分・件数・終了コードはnullです。

使用例:
  checkstamp status 0123456789abcdef0123456789abcdef --json-summary

記録を作った作業ディレクトリから実行します。入力・除外は保存時の条件を使います。
子コマンドの再実行、記録や入力の書換えは行いません。標準出力へ結果を返します。
終了コード: 0=valid、1=stale / failed / unstable / interrupted、2=unknown、64=引数エラー。
複数の状態は unknown → interrupted → failed → unstable → stale → valid の順に判定します。
確認範囲は明示した入力と記録できた実行条件です。未指定入力や外部条件は保証しません。
ヘルプは単独で指定します。記録や現在の入力は読み取りません。
`

const listHelpText = `checkstamp list - 保存済みの実行IDを探す

使い方:
  checkstamp list [--label <名前>] [--limit <件数>] [--json-summary]
  checkstamp list --help / -h

オプション:
  --label <名前>  任意。ラベルの完全一致で絞ります。省略時は全ラベルです。
  --limit <件数>  任意。1～1000件、既定20件。開始日時の新しい順で返します。
  --json-summary 入力一覧やハッシュを含まないJSONオブジェクト1件を返します。
  各オプションは1回だけ指定できます。

使用例:
  checkstamp list --label unit --limit 10 --json-summary

保存時の実行ID・ラベル・開始と終了のUTC日時・判定・子の終了コード・問題を返します。
日時不明の記録は先に、同時刻は実行IDの昇順に並べます。未取得情報はnullです。
ラベル不明の記録も表示し、読取不成立件数には絞り込み・件数制限の外も含めます。
JSONの status は一覧の読取結果（complete / partial / unknown / invalid_arguments）です。
runs の recorded_status は保存時の判定で、現在の有効性を表しません。
has_more は表示対象が残るか、unavailable_count は読取不成立件数です。未確認はnullです。
記録なしは空配列です。破損・未完了・未対応形式・読取不能を記録なしとは扱いません。
終了コード: 0=一覧読取完了、2=未完了・読取不能など、64=引数エラー。
` + listNotice + `
記録先・入力の作成や更新、入力の再収集、子コマンドの再実行は行いません。
ヘルプは単独で指定します。記録は読み取りません。
`
