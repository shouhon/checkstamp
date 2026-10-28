package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/shouhon/checkstamp/internal/input"
	"github.com/shouhon/checkstamp/internal/process"
	"github.com/shouhon/checkstamp/internal/record"
	"github.com/shouhon/checkstamp/internal/version"
)

type runOptions struct {
	label           string
	inputs, command []string
	json            bool
	jsonSummary     bool
}

type runResult struct {
	SchemaVersion int                `json:"schema_version"`
	Tool          record.Tool        `json:"tool"`
	Command       string             `json:"command"`
	RunID         string             `json:"run_id"`
	Label         *string            `json:"label"`
	Status        string             `json:"status"`
	ChildExitCode *int               `json:"child_exit_code"`
	StartedAt     *time.Time         `json:"started_at"`
	FinishedAt    *time.Time         `json:"finished_at"`
	Conditions    *record.Conditions `json:"conditions"`
	Coverage      record.Coverage    `json:"coverage"`
	Before        *input.Snapshot    `json:"before"`
	After         *input.Snapshot    `json:"after"`
	Changes       []input.Change     `json:"changes"`
	Issues        []record.Issue     `json:"issues"`
	Interrupted   bool               `json:"interrupted"`
}

func parseRun(args []string) (options runOptions, valid bool) {
	// --以降の子の引数は、checkstampのオプションとして解釈しない。
	boundary := -1
	for i, arg := range args {
		if arg == "--" {
			boundary = i
			break
		}
	}
	flags := args
	if boundary >= 0 {
		flags, options.command = args[:boundary], args[boundary+1:]
	}
	for _, arg := range flags {
		if arg == "--json" || arg == "--json-summary" {
			options.json = true
		}
		if arg == "--json-summary" {
			options.jsonSummary = true
		}
	}
	seenLabel, seenJSON := false, false
	for i := 0; i < len(flags); i++ {
		switch flags[i] {
		case "--label", "--input":
			if i+1 >= len(flags) || flags[i+1] == "" || strings.ContainsRune(flags[i+1], '\x00') || strings.HasPrefix(flags[i+1], "--") {
				return options, false
			}
			i++
			if flags[i-1] == "--label" {
				if seenLabel || strings.TrimSpace(flags[i]) == "" {
					return options, false
				}
				seenLabel, options.label = true, flags[i]
			} else {
				options.inputs = append(options.inputs, flags[i])
			}
		case "--json", "--json-summary":
			if seenJSON {
				return options, false
			}
			seenJSON = true
		default:
			return options, false
		}
	}
	if boundary < 0 || !seenLabel || len(options.inputs) == 0 || len(options.command) == 0 || options.command[0] == "" {
		return options, false
	}
	for _, arg := range options.command {
		if strings.ContainsRune(arg, '\x00') {
			return options, false
		}
	}
	return options, true
}

func runCommand(args []string, stdout, stderr io.Writer) int {
	options, valid := parseRun(args)
	result := runResult{
		SchemaVersion: record.SchemaVersion, Tool: record.Tool{Name: "checkstamp", Version: version.String()},
		Command: "run", Status: "unknown", Coverage: record.CoverageLimits(),
		Changes: []input.Change{}, Issues: []record.Issue{},
	}
	finish := func(code int) int {
		if options.json {
			var output any = result
			if options.jsonSummary {
				output = summaryResult{
					SchemaVersion: result.SchemaVersion, Command: result.Command, RunID: result.RunID,
					Status: result.Status, ChildExitCode: result.ChildExitCode,
					ChangeCount: changeCount(result.Changes, result.After != nil), Issues: result.Issues,
				}
			}
			if err := json.NewEncoder(stdout).Encode(output); err != nil {
				fmt.Fprintln(stderr, "checkstamp: 標準出力へ書き込めませんでした。")
			}
		} else {
			fmt.Fprintf(stderr, "checkstamp: 判定: %s\n", result.Status)
			fmt.Fprintf(stderr, "checkstamp: 保存しない情報: %s\ncheckstamp: 未確認の条件: %s\n", strings.Join(result.Coverage.Omitted, "、"), strings.Join(result.Coverage.Unverified, "、"))
		}
		for _, issue := range result.Issues {
			fmt.Fprintln(stderr, "checkstamp:", issue.Description)
		}
		return code
	}
	fail := func(code int, issue, description string) int {
		if issue == "invalid_arguments" {
			result.Status = "invalid_arguments"
		}
		result.Issues = append(result.Issues, record.Issue{Code: issue, Description: description})
		return finish(code)
	}
	if !valid {
		return fail(64, "invalid_arguments", "引数が正しくありません。使い方は --help で確認してください。")
	}
	result.Label = &options.label
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	directory, err := os.Getwd()
	if err != nil {
		return fail(125, "working_directory_unavailable", "作業ディレクトリを確認できませんでした。")
	}
	before, err := input.Collect(directory, options.inputs)
	if err != nil {
		description := inputFailureDescription(err)
		if errors.Is(err, input.ErrArguments) || errors.Is(err, input.ErrExcluded) {
			return fail(64, "invalid_arguments", description)
		}
		return fail(125, "input_collection_failed", description)
	}
	result.Before = &before
	executable, err := process.Resolve(options.command[0])
	if err != nil {
		return fail(125, "executable_unavailable", "通常の実行ファイルを解決できませんでした。スクリプトは明示的に実行プログラムへ渡してください。")
	}
	store, err := record.Open(directory)
	if err != nil {
		return fail(125, "storage_unavailable", "専用の記録先を保存用に開けませんでした。既存の別用途の .checkstamp は利用できません。")
	}
	defer store.Close()
	run, err := store.Start(options.label, executable, options.command[1:], before)
	if err != nil {
		return fail(125, "record_start_failed", "開始記録を保存できませんでした。子プロセスは起動していません。")
	}
	result.RunID = run.ID()
	fmt.Fprintln(stderr, "checkstamp: 実行ID:", run.ID())
	childOut := stdout
	if options.json {
		childOut = stderr
	}
	child := process.Execute(executable, options.command[1:], directory, childOut, stderr, signals)
	// 起動できなかった場合や終了コードを取得できない場合、開始記録を成功へ置換しない。
	if !child.Started || child.ExitCode == nil {
		if saved, err := store.Load(run.ID()); err == record.ErrIncomplete {
			result = resultFromRecord(saved)
		}
		result.Interrupted = child.Interrupted
		if child.Interrupted {
			result.Issues = append(result.Issues, record.Issue{Code: "interrupted", Description: "中断を受けた事実を確認しました。"})
		}
		return fail(125, "child_start_or_wait_failed", "子プロセスを起動できないか、終了コードを取得できませんでした。完了記録は保存していません。")
	}
	if child.CleanupFailed {
		if saved, err := store.Load(run.ID()); errors.Is(err, record.ErrIncomplete) {
			result = resultFromRecord(saved)
		}
		result.ChildExitCode, result.Interrupted = child.ExitCode, child.Interrupted
		if child.Interrupted {
			result.Issues = append(result.Issues, record.Issue{Code: "interrupted", Description: "中断を受けた事実を確認しました。"})
		}
		if *child.ExitCode != 0 {
			result.Issues = append(result.Issues, record.Issue{Code: "child_failed", Description: "子プロセスが0以外の終了コードで終了しました。"})
		}
		return fail(*child.ExitCode, "process_cleanup_failed", "管理対象プロセスの終了を確認できませんでした。完了記録は保存していません。")
	}
	after, readErr := input.Recollect(directory, before.Inputs)
	var finalInput *input.Snapshot
	if readErr == nil {
		finalInput = &after
	}
	select {
	case <-signals:
		child.Interrupted = true
	default:
	}
	saved, saveErr := run.Complete(*child.ExitCode, finalInput, child.Interrupted)
	if saved.RunID != "" {
		result = resultFromRecord(saved)
	} else {
		result.ChildExitCode = child.ExitCode
		result.Status = "unknown"
	}
	if saveErr != nil && saved.RunID == "" {
		result.Issues = append(result.Issues, record.Issue{Code: "record_write_failed", Description: "完了記録を保存できませんでした。"})
	}
	if child.OutputFailed {
		fmt.Fprintln(stderr, "checkstamp: 子プロセスの出力を転送できませんでした。")
	}
	return finish(*child.ExitCode)
}

func inputFailureDescription(err error) string {
	subject := "入力"
	var failure *input.Error
	if errors.As(err, &failure) {
		subject = fmt.Sprintf("%d番目の --input ", failure.Index)
	}
	var reason string
	switch {
	case errors.Is(err, input.ErrArguments):
		reason = "は作業ディレクトリ内の相対パスで指定してください。"
	case errors.Is(err, input.ErrExcluded):
		reason = "に管理領域を直接指定することはできません。"
	case errors.Is(err, input.ErrMissing):
		reason = "の対象が存在しません。"
	case errors.Is(err, input.ErrUnsupported):
		reason = "に未対応の入力があります。リンク・再解析ポイント・特殊ファイル、または大文字小文字だけが異なる名前には対応していません。"
	case errors.Is(err, input.ErrRead):
		reason = "の種類または内容を読み取れませんでした。"
	default:
		reason = "を収集できませんでした。"
	}
	return subject + reason + "子プロセスは起動していません。"
}

func resultFromRecord(saved record.Record) runResult {
	return runResult{
		SchemaVersion: saved.SchemaVersion, Tool: saved.Tool, Command: saved.Command, RunID: saved.RunID,
		Label: &saved.Label, Status: saved.Status, ChildExitCode: saved.ChildExitCode,
		StartedAt: &saved.StartedAt, FinishedAt: saved.FinishedAt, Conditions: &saved.Conditions,
		Coverage: saved.Coverage, Before: &saved.Before, After: saved.After,
		Changes: saved.Changes, Issues: saved.Issues, Interrupted: saved.Interrupted,
	}
}
