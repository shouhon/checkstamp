package cli

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/shouhon/checkstamp/internal/input"
	"github.com/shouhon/checkstamp/internal/record"
	"github.com/shouhon/checkstamp/internal/version"
)

// 読取不成立の記録から取得できない値は、ゼロ日時や空の実行条件で補わずnullにする。
type statusResult struct {
	SchemaVersion  int                `json:"schema_version"`
	Tool           record.Tool        `json:"tool"`
	Command        string             `json:"command"`
	RunID          string             `json:"run_id"`
	Label          *string            `json:"label"`
	Status         string             `json:"status"`
	RecordStatus   *string            `json:"record_status"`
	RecordTool     *record.Tool       `json:"record_tool"`
	ChildExitCode  *int               `json:"child_exit_code"`
	StartedAt      *time.Time         `json:"started_at"`
	FinishedAt     *time.Time         `json:"finished_at"`
	Conditions     *record.Conditions `json:"conditions"`
	Coverage       record.Coverage    `json:"coverage"`
	Before         *input.Snapshot    `json:"before"`
	After          *input.Snapshot    `json:"after"`
	Current        *input.Snapshot    `json:"current"`
	Changes        []input.Change     `json:"changes"`
	CurrentChanges []input.Change     `json:"current_changes"`
	Issues         []record.Issue     `json:"issues"`
}

func runStatus(args []string, stdout, stderr io.Writer) int {
	result := newStatusResult("")
	jsonOutput, jsonSummary, jsonChanges := false, false, false
	for _, arg := range args {
		if arg == "--json" || arg == "--json-summary" || arg == "--json-changes" {
			jsonOutput = true
		}
		if arg == "--json-summary" {
			jsonSummary = true
		}
		if arg == "--json-changes" {
			jsonChanges = true
		}
	}
	idCount := len(args)
	if idCount > 0 && (args[idCount-1] == "--json" || args[idCount-1] == "--json-summary" || args[idCount-1] == "--json-changes") {
		idCount--
	}
	valid := idCount > 0
	for _, id := range args[:idCount] {
		valid = valid && validRunID(id)
	}
	if !valid {
		result.Status = "invalid_arguments"
		result.Issues = append(result.Issues, record.Issue{Code: "invalid_arguments", Description: "引数が正しくありません。使い方は --help で確認してください。"})
		invalidArguments(stderr)
		if jsonOutput {
			writeStatusJSON(stdout, stderr, result, jsonSummary, jsonChanges)
		}
		return 64
	}
	if idCount > 1 {
		return runMultipleStatus(args[:idCount], stdout, stderr, jsonOutput, jsonSummary, jsonChanges)
	}
	result.RunID = args[0]
	checkStatus(&result)
	reportUnknownStatus(stderr, result)
	if jsonOutput {
		if !writeStatusJSON(stdout, stderr, result, jsonSummary, jsonChanges) {
			return 2
		}
	} else if !writeStatusText(stdout, stderr, result) {
		return 2
	}
	return statusExitCode(result.Status)
}

func newStatusResult(id string) statusResult {
	return statusResult{
		SchemaVersion: record.SchemaVersion, Tool: record.Tool{Name: "checkstamp", Version: version.String()},
		Command: "status", RunID: id, Status: "unknown", Coverage: record.CoverageLimits(),
		Changes: []input.Change{}, CurrentChanges: []input.Change{}, Issues: []record.Issue{},
	}
}

func statusExitCode(status string) int {
	switch status {
	case "valid":
		return 0
	case "unknown":
		return 2
	default:
		return 1
	}
}

func reportUnknownStatus(stderr io.Writer, result statusResult) {
	if result.Status != "unknown" {
		return
	}
	fmt.Fprintln(stderr, "checkstamp: 記録の有効性を確認できませんでした。")
	for _, issue := range result.Issues {
		fmt.Fprintln(stderr, "checkstamp:", issue.Description)
	}
}

func validRunID(id string) bool {
	if len(id) != 32 || id != strings.ToLower(id) {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func checkStatus(result *statusResult) {
	directory, err := os.Getwd()
	if err != nil {
		result.Issues = append(result.Issues, record.Issue{Code: "working_directory_unavailable", Description: "作業ディレクトリを確認できませんでした。"})
		return
	}
	store, err := record.OpenReadOnly(directory)
	if err != nil {
		result.Issues = append(result.Issues, record.Issue{Code: "storage_unavailable", Description: "専用の記録先を読み取り用に開けませんでした。同じ作業ディレクトリから実行してください。"})
		return
	}
	defer store.Close()
	saved, err := store.Load(result.RunID)
	if err != nil && !errors.Is(err, record.ErrIncomplete) {
		issue := record.Issue{Code: "record_read_failed", Description: "記録を読み取れませんでした。"}
		switch {
		case errors.Is(err, record.ErrCorrupt):
			issue = record.Issue{Code: "record_corrupt", Description: "記録が破損しているか、必須情報が一致しません。"}
		case errors.Is(err, record.ErrSchema):
			issue = record.Issue{Code: "unsupported_schema", Description: "記録の形式の版に対応していません。"}
		}
		result.Issues = append(result.Issues, issue)
		return
	}
	result.Label, result.RecordStatus = &saved.Label, &saved.Status
	result.RecordTool = &saved.Tool
	result.ChildExitCode = saved.ChildExitCode
	result.StartedAt, result.FinishedAt = &saved.StartedAt, saved.FinishedAt
	result.Conditions, result.Coverage = &saved.Conditions, saved.Coverage
	result.Before, result.After = &saved.Before, saved.After
	result.Changes, result.Issues = saved.Changes, saved.Issues
	result.Status = saved.Status

	current, err := input.Recollect(directory, saved.Before.Inputs, saved.Before.ExcludePatterns...)
	if err != nil {
		result.Status = "unknown"
		result.Issues = append(result.Issues, record.Issue{Code: "current_input_read_failed", Description: "現在の入力の種類または内容を十分に読み取れませんでした。"})
		return
	}
	result.Current = &current
	// 終了時の一覧がなければ開始時との差分を示すが、未完了という判定は維持する。
	baseline := saved.Before
	if saved.After != nil {
		baseline = *saved.After
	}
	result.CurrentChanges = input.Diff(baseline, current)
	if len(result.CurrentChanges) != 0 {
		result.Issues = append(result.Issues, record.Issue{Code: "current_input_changed", Description: "最後に記録した一覧から現在の入力が変わっています。"})
		if result.Status == "valid" {
			result.Status = "stale"
		}
	}
}

func writeStatusJSON(stdout, stderr io.Writer, result statusResult, summary, changes bool) bool {
	var output any = result
	if summary || changes {
		short := summaryResult{
			SchemaVersion: result.SchemaVersion, Command: result.Command, RunID: result.RunID,
			Status: result.Status, ChildExitCode: result.ChildExitCode,
			ChangeCount:        changeCount(result.Changes, result.After != nil),
			CurrentChangeCount: changeCount(result.CurrentChanges, result.Current != nil), Issues: result.Issues,
			Exclusions: exclusions(result.Before),
		}
		output = short
		if changes && !summary {
			output = changesResult{
				summaryResult: short, Changes: availableChanges(result.Changes, result.After != nil),
				CurrentChanges: availableChanges(result.CurrentChanges, result.Current != nil),
			}
		}
	}
	if err := json.NewEncoder(stdout).Encode(output); err != nil {
		fmt.Fprintln(stderr, "checkstamp: 標準出力へ書き込めませんでした。")
		return false
	}
	return true
}

func writeStatusText(stdout, stderr io.Writer, result statusResult) bool {
	descriptions := map[string]string{
		"valid": "指定範囲で有効", "stale": "記録後に入力が変更済み", "failed": "検証コマンドが失敗",
		"unstable": "実行前後で入力が変更", "interrupted": "中断を記録", "unknown": "確認不能",
	}
	var text strings.Builder
	fmt.Fprintf(&text, "実行ID: %s\n判定: %s（%s）\n", result.RunID, result.Status, descriptions[result.Status])
	if result.ChildExitCode != nil {
		fmt.Fprintf(&text, "子プロセスの終了コード: %d\n", *result.ChildExitCode)
	}
	if scope := exclusions(result.Before); scope != nil {
		fmt.Fprintf(&text, "除外条件（作業ディレクトリ基準）: %q\n", scope.Patterns)
	}
	for _, issue := range result.Issues {
		fmt.Fprintf(&text, "確認事項: %s\n", issue.Description)
	}
	for _, group := range []struct {
		name    string
		changes []input.Change
	}{{"実行前後の差分", result.Changes}, {"最後の記録から現在の差分", result.CurrentChanges}} {
		fmt.Fprintf(&text, "%s: %d件\n", group.name, len(group.changes))
		for _, change := range group.changes {
			kind := map[string]string{"added": "追加", "removed": "削除", "modified": "変更"}[change.Kind]
			fmt.Fprintf(&text, "  %s: %q\n", kind, change.Path)
		}
	}
	fmt.Fprintf(&text, "保存しない情報: %s\n未確認の条件: %s\n", strings.Join(result.Coverage.Omitted, "、"), strings.Join(result.Coverage.Unverified, "、"))
	if _, err := io.WriteString(stdout, text.String()); err != nil {
		fmt.Fprintln(stderr, "checkstamp: 標準出力へ書き込めませんでした。")
		return false
	}
	return true
}
