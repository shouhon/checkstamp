package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shouhon/checkstamp/internal/record"
)

const listNotice = "一覧は保存時の情報です。現在の有効性は同じ作業ディレクトリで status <実行ID> を実行して確認してください。"

type listOptions struct {
	label string
	limit int
	json  bool
}

type listResult struct {
	SchemaVersion    int              `json:"schema_version"`
	Command          string           `json:"command"`
	Status           string           `json:"status"`
	Runs             []record.Summary `json:"runs"`
	HasMore          *bool            `json:"has_more"`
	UnavailableCount *int             `json:"unavailable_count"`
	Issues           []record.Issue   `json:"issues"`
	Notice           string           `json:"notice"`
}

func parseList(args []string) (options listOptions, valid bool) {
	options.limit = 20
	for _, arg := range args {
		if arg == "--json-summary" {
			options.json = true
		}
	}
	seen := make(map[string]bool)
	for i := 0; i < len(args); i++ {
		flag := args[i]
		if seen[flag] {
			return options, false
		}
		seen[flag] = true
		switch flag {
		case "--json-summary":
		case "--label", "--limit":
			if i+1 >= len(args) {
				return options, false
			}
			i++
			value := args[i]
			if flag == "--label" {
				if strings.TrimSpace(value) == "" || strings.HasPrefix(value, "--") || strings.ContainsRune(value, '\x00') {
					return options, false
				}
				options.label = value
			} else {
				for _, c := range value {
					if c < '0' || c > '9' {
						return options, false
					}
				}
				limit, err := strconv.Atoi(value)
				if err != nil || limit < 1 || limit > 1000 {
					return options, false
				}
				options.limit = limit
			}
		default:
			return options, false
		}
	}
	return options, true
}

func runList(args []string, stdout, stderr io.Writer) int {
	options, valid := parseList(args)
	result := listResult{SchemaVersion: record.SchemaVersion, Command: "list", Status: "unknown", Issues: []record.Issue{}, Notice: listNotice}
	code := 2
	if !valid {
		result.Status = "invalid_arguments"
		result.Issues = append(result.Issues, record.Issue{Code: "invalid_arguments", Description: "引数が正しくありません。使い方は list --help で確認してください。"})
		code = 64
	} else {
		checkList(&result, options)
		if result.Status == "complete" {
			code = 0
		}
	}
	for _, issue := range result.Issues {
		fmt.Fprintln(stderr, "checkstamp:", issue.Description)
	}
	if options.json {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			fmt.Fprintln(stderr, "checkstamp: 標準出力へ書き込めませんでした。")
			if code != 64 {
				return 2
			}
		}
	} else if code != 64 && !writeListText(stdout, stderr, result) {
		return 2
	}
	return code
}

func checkList(result *listResult, options listOptions) {
	directory, err := os.Getwd()
	if err != nil {
		result.Issues = append(result.Issues, record.Issue{Code: "working_directory_unavailable", Description: "作業ディレクトリを確認できませんでした。"})
		return
	}
	listing := record.Listing{Runs: []record.Summary{}}
	store, err := record.OpenReadOnly(directory)
	if err != nil && !errors.Is(err, record.ErrNoStorage) {
		result.Issues = append(result.Issues, record.Issue{Code: "storage_unavailable", Description: "専用の記録先を読み取り用に開けませんでした。同じ作業ディレクトリから実行してください。"})
		return
	}
	if store != nil {
		defer store.Close()
		listing, err = store.List(options.label, options.limit)
		if err != nil {
			result.Issues = append(result.Issues, record.Issue{Code: "record_list_failed", Description: "記録の一覧を十分に読み取れませんでした。"})
			return
		}
	}
	result.Runs, result.HasMore, result.UnavailableCount = listing.Runs, &listing.HasMore, &listing.UnavailableCount
	result.Status = "complete"
	if listing.UnavailableCount != 0 {
		result.Status = "partial"
		result.Issues = append(result.Issues, record.Issue{Code: "records_unavailable", Description: "未完了・破損・未対応形式・読取不能、または名前が不正な記録があります。件数は絞り込み・表示制限の外も含みます。"})
	}
}

func writeListText(stdout, stderr io.Writer, result listResult) bool {
	var text strings.Builder
	fmt.Fprintf(&text, "%s\n一覧の読取結果: %s\n", result.Notice, result.Status)
	if result.UnavailableCount != nil && *result.UnavailableCount != 0 {
		fmt.Fprintf(&text, "読取不成立: %d件（絞り込み・表示制限の外も含む）\n", *result.UnavailableCount)
	}
	if result.Runs != nil && len(result.Runs) == 0 {
		fmt.Fprintln(&text, "表示対象の記録はありません。")
	}
	for _, run := range result.Runs {
		label, started, finished, status, exit := "未取得", "未取得", "未取得", "未取得", "未取得"
		if run.Label != nil {
			label = strconv.Quote(*run.Label)
		}
		if run.StartedAt != nil {
			started = run.StartedAt.Format(time.RFC3339Nano)
		}
		if run.FinishedAt != nil {
			finished = run.FinishedAt.Format(time.RFC3339Nano)
		}
		if run.RecordedStatus != nil {
			status = *run.RecordedStatus
		}
		if run.ChildExitCode != nil {
			exit = strconv.Itoa(*run.ChildExitCode)
		}
		fmt.Fprintf(&text, "実行ID: %s\n  ラベル: %s / 開始日時（UTC）: %s / 終了日時（UTC）: %s / 保存時の判定: %s / 子の終了コード: %s\n", run.RunID, label, started, finished, status, exit)
		for _, issue := range run.Issues {
			fmt.Fprintf(&text, "  確認事項: %s\n", issue.Description)
		}
	}
	if result.HasMore != nil && *result.HasMore {
		fmt.Fprintln(&text, "表示件数を超える記録があります。--limit で件数を増やせます。")
	}
	if _, err := io.WriteString(stdout, text.String()); err != nil {
		fmt.Fprintln(stderr, "checkstamp: 標準出力へ書き込めませんでした。")
		return false
	}
	return true
}
