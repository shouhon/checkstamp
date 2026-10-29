package cli_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/shouhon/checkstamp/internal/cli"
	"github.com/shouhon/checkstamp/internal/input"
	"github.com/shouhon/checkstamp/internal/record"
	"github.com/shouhon/checkstamp/internal/version"
)

const privateStatusValue = "照合試験用の非公開値"

type statusOutput struct {
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

func statusFixture(t *testing.T) (string, *record.Run, input.Snapshot) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "日本語 空白")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	writeStatusInput(t, dir, "入力", privateStatusValue)
	before, err := input.Collect(dir, []string{"src"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := record.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// 誤って再実行すると印を作る実行条件。statusでは起動されてはならない。
	args := []string{"-test.run=^TestStatusMustNotExecute$", "--", "--status-child", filepath.Join(dir, "誤った再実行"), privateStatusValue}
	run, err := store.Start("照合の試験", executable, args, before)
	if err != nil {
		t.Fatal(err)
	}
	return dir, run, before
}

func TestStatusMustNotExecute(t *testing.T) {
	for i, arg := range os.Args {
		if arg == "--status-child" && i+1 < len(os.Args) {
			if err := os.WriteFile(os.Args[i+1], []byte("誤った再実行"), 0600); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
}

func writeStatusInput(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "src", name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

type statusFileState struct {
	Mode     fs.FileMode
	Modified time.Time
	SHA256   [32]byte
}

func statusTree(t *testing.T, dir string) map[string]statusFileState {
	t.Helper()
	state := make(map[string]statusFileState)
	err := filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		// ディレクトリは構成と種類を比較し、通常ファイルは本文と更新日時も比較する。
		item := statusFileState{Mode: info.Mode()}
		if info.Mode().IsRegular() {
			item.Modified = info.ModTime()
			data, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			item.SHA256 = sha256.Sum256(data)
		}
		state[name] = item
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func invokeStatus(t *testing.T, executable, dir string, args ...string) (int, string, string) {
	t.Helper()
	command := exec.Command(executable, args...)
	command.Dir = dir
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatal(err)
		}
	}
	for _, output := range []string{stdout.String(), stderr.String()} {
		if strings.Contains(output, privateStatusValue) {
			t.Fatal("引数または入力の本文が出力されています")
		}
	}
	return command.ProcessState.ExitCode(), stdout.String(), stderr.String()
}

func decodeStatus(t *testing.T, output string) statusOutput {
	t.Helper()
	var result statusOutput
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatal("標準出力がJSONオブジェクト1件ではありません")
	}
	if result.SchemaVersion != record.SchemaVersion || result.Tool.Name != "checkstamp" || result.Tool.Version != version.String() || result.Command != "status" || len(result.Coverage.Omitted) == 0 || len(result.Coverage.Unverified) == 0 || result.Changes == nil || result.CurrentChanges == nil || result.Issues == nil {
		t.Fatalf("結果の共通情報が不足しています: %+v", result)
	}
	return result
}

func TestStatusExecutable(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "checkstamp.exe")
	build := exec.Command("go", "build", "-mod=readonly", "-o", executable, "../../cmd/checkstamp")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("受入試験用ビルドが失敗しました: %v\n%s", err, output)
	}

	t.Run("状態と優先順位と読み取り専用", func(t *testing.T) {
		for _, tc := range []struct {
			name, status, currentChange string
			exit                        int
			started, noAfter            bool
			changed, interrupted, lock  bool
			issues                      []string
		}{
			{name: "有効", status: "valid"},
			{name: "内容変更", status: "stale", currentChange: "modified", issues: []string{"current_input_changed"}},
			{name: "追加", status: "stale", currentChange: "added", issues: []string{"current_input_changed"}},
			{name: "削除", status: "stale", currentChange: "removed", issues: []string{"current_input_changed"}},
			{name: "範囲全体の削除", status: "stale", currentChange: "root_removed", issues: []string{"current_input_changed"}},
			{name: "種類変更", status: "stale", currentChange: "type_changed", issues: []string{"current_input_changed"}},
			{name: "失敗と現在の変更", status: "failed", exit: 64, currentChange: "modified", issues: []string{"child_failed", "current_input_changed"}},
			{name: "実行中の変更", status: "unstable", changed: true, issues: []string{"input_changed"}},
			{name: "実行前に戻しても不安定", status: "unstable", changed: true, currentChange: "reverted", issues: []string{"input_changed", "current_input_changed"}},
			{name: "中断と失敗と変更", status: "interrupted", interrupted: true, exit: 125, changed: true, currentChange: "added", issues: []string{"interrupted", "child_failed", "input_changed", "current_input_changed"}},
			{name: "終了時の読取失敗は回復しても確認不能", status: "unknown", noAfter: true, interrupted: true, exit: 2, currentChange: "modified", issues: []string{"input_read_failed", "interrupted", "child_failed", "current_input_changed"}},
			{name: "未完了と現在の変更", status: "unknown", started: true, currentChange: "modified", issues: []string{"incomplete", "current_input_changed"}},
			{name: "現在の読取不能", status: "unknown", lock: true, issues: []string{"current_input_read_failed"}},
			{name: "読取不能と中断と失敗と変更", status: "unknown", lock: true, interrupted: true, exit: 1, changed: true, issues: []string{"interrupted", "child_failed", "input_changed", "current_input_read_failed"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				dir, run, before := statusFixture(t)
				if tc.changed {
					writeStatusInput(t, dir, "入力", "実行中の変更")
				}
				if !tc.started {
					after, err := input.Recollect(dir, before.Inputs)
					if err != nil {
						t.Fatal(err)
					}
					snapshot := &after
					if tc.noAfter {
						snapshot = nil
					}
					if _, err := run.Complete(tc.exit, snapshot, tc.interrupted); err != nil {
						t.Fatal(err)
					}
				}
				switch tc.currentChange {
				case "modified":
					writeStatusInput(t, dir, "入力", "現在の変更")
				case "reverted":
					writeStatusInput(t, dir, "入力", privateStatusValue)
				case "added":
					writeStatusInput(t, dir, "追加", "現在の追加")
				case "removed", "root_removed", "type_changed":
					if err := os.Remove(filepath.Join(dir, "src/入力")); err != nil {
						t.Fatal(err)
					}
					if tc.currentChange == "root_removed" {
						if err := os.Remove(filepath.Join(dir, "src")); err != nil {
							t.Fatal(err)
						}
					} else if tc.currentChange == "type_changed" {
						if err := os.Mkdir(filepath.Join(dir, "src/入力"), 0700); err != nil {
							t.Fatal(err)
						}
					}
				}
				original := statusTree(t, dir)
				var lock syscall.Handle
				if tc.lock {
					name, err := syscall.UTF16PtrFromString(filepath.Join(dir, "src/入力"))
					if err != nil {
						t.Fatal(err)
					}
					lock, err = syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, 0, 0)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if lock != 0 {
							syscall.CloseHandle(lock)
						}
					})
				}
				wantCode := 1
				if tc.status == "valid" {
					wantCode = 0
				} else if tc.status == "unknown" {
					wantCode = 2
				}
				code, stdout, stderr := invokeStatus(t, executable, dir, "status", run.ID(), "--json")
				result := decodeStatus(t, stdout)
				if code != wantCode || result.Status != tc.status || result.RunID != run.ID() {
					t.Fatalf("終了コード=%d、判定=%s、期待値=%d/%s", code, result.Status, wantCode, tc.status)
				}
				if result.RecordStatus == nil || result.RecordTool == nil || result.RecordTool.Name != "checkstamp" || result.Label == nil || result.Conditions == nil || result.Before == nil || result.StartedAt == nil || result.StartedAt.Location() != time.UTC {
					t.Fatal("読取済みの実行情報がありません")
				}
				if !tc.started && (result.ChildExitCode == nil || *result.ChildExitCode != tc.exit || result.FinishedAt == nil) {
					t.Fatal("子プロセスの終了情報が保持されていません")
				}
				var issues []string
				for _, issue := range result.Issues {
					issues = append(issues, issue.Code)
				}
				if !slices.Equal(issues, tc.issues) || (tc.status == "unknown") != (stderr != "") {
					t.Fatalf("確認事項=%v、標準エラー=%q", issues, stderr)
				}
				if tc.currentChange != "" && len(result.CurrentChanges) == 0 || tc.changed && !tc.noAfter && len(result.Changes) == 0 {
					t.Fatal("実行前後または現在の差分が失われています")
				}
				if tc.lock && result.Current != nil {
					t.Fatal("読取不能時に部分的な一覧を返しています")
				}
				summaryCode, summaryJSON, _ := invokeStatus(t, executable, dir, "status", run.ID(), "--json-summary")
				summary := decodeSummary(t, summaryJSON)
				if summaryCode != code || summary.Status != result.Status || summary.RunID != result.RunID || !reflect.DeepEqual(summary.ChildExitCode, result.ChildExitCode) || !reflect.DeepEqual(summary.Issues, result.Issues) {
					t.Fatal("要約から照合の判定・終了コード・問題が失われています")
				}
				if result.After != nil {
					if summary.ChangeCount == nil || *summary.ChangeCount != len(result.Changes) {
						t.Fatal("要約の実行前後の変更件数が一致しません")
					}
				} else if summary.ChangeCount != nil {
					t.Fatal("未確認の実行前後の変更件数はnullが必要です")
				}
				if result.Current != nil {
					if summary.CurrentChangeCount == nil || *summary.CurrentChangeCount != len(result.CurrentChanges) {
						t.Fatal("要約の現在の変更件数が一致しません")
					}
				} else if summary.CurrentChangeCount != nil {
					t.Fatal("読取不能な現在の変更件数はnullが必要です")
				}
				changesCode, changesJSON, _ := invokeStatus(t, executable, dir, "status", run.ID(), "--json-changes")
				changes := decodeChanges(t, changesJSON)
				if changesCode != code || changes.Status != result.Status || !reflect.DeepEqual(changes.ChildExitCode, result.ChildExitCode) || !reflect.DeepEqual(changes.Issues, result.Issues) || (changes.Changes == nil) != (result.After == nil) || (changes.CurrentChanges == nil) != (result.Current == nil) {
					t.Fatal("変更明細の判定・終了コード・問題・未確認の値が一致しません")
				}
				if result.After != nil && !reflect.DeepEqual(changes.Changes, result.Changes) || result.Current != nil && !reflect.DeepEqual(changes.CurrentChanges, result.CurrentChanges) {
					t.Fatal("実行前後と現在の変更明細が一致しません")
				}
				code, text, _ := invokeStatus(t, executable, dir, "status", run.ID())
				if code != wantCode || !strings.Contains(text, "判定: "+tc.status) || !strings.Contains(text, "未確認の条件:") {
					t.Fatal("日本語表示の判定または終了コードがJSONと一致しません")
				}
				if tc.lock {
					syscall.CloseHandle(lock)
					lock = 0
				}
				if !reflect.DeepEqual(original, statusTree(t, dir)) {
					t.Fatal("statusが入力・記録を書き換えたか、子プロセスを起動しました")
				}
			})
		}
	})

	t.Run("不成立の記録", func(t *testing.T) {
		for _, kind := range []string{"未完了", "破損", "未知形式", "欠落", "キー重複", "保存先なし", "別用途の保存先", "別の作業ディレクトリ"} {
			t.Run(kind, func(t *testing.T) {
				dir, run, before := statusFixture(t)
				if kind != "未完了" {
					if _, err := run.Complete(0, &before, false); err != nil {
						t.Fatal(err)
					}
				}
				name := filepath.Join(dir, ".checkstamp/runs", run.ID()+".json")
				wantIssue := "incomplete"
				switch kind {
				case "破損", "未知形式", "キー重複":
					data := []byte(privateStatusValue)
					wantIssue = "record_corrupt"
					if kind == "未知形式" {
						data = []byte(`{"schema_version":999,"非公開":"` + privateStatusValue + `"}`)
						wantIssue = "unsupported_schema"
					} else if kind == "キー重複" {
						data = []byte(`{"schema_version":1,"schema_version":1}`)
					}
					if err := os.WriteFile(name, data, 0600); err != nil {
						t.Fatal(err)
					}
				case "欠落":
					if err := os.Remove(name); err != nil {
						t.Fatal(err)
					}
					wantIssue = "record_read_failed"
				case "保存先なし", "別の作業ディレクトリ", "別用途の保存先":
					dir = t.TempDir()
					wantIssue = "storage_unavailable"
					if kind == "別用途の保存先" {
						if err := os.Mkdir(filepath.Join(dir, ".checkstamp"), 0700); err != nil {
							t.Fatal(err)
						}
					}
				}
				original := statusTree(t, dir)
				code, stdout, stderr := invokeStatus(t, executable, dir, "status", run.ID(), "--json")
				result := decodeStatus(t, stdout)
				if code != 2 || result.Status != "unknown" || stderr == "" || len(result.Issues) != 1 || result.Issues[0].Code != wantIssue {
					t.Fatalf("不成立の記録を区別できません: %+v、終了コード=%d", result, code)
				}
				if kind != "未完了" && (result.Label != nil || result.Conditions != nil || result.ChildExitCode != nil || result.StartedAt != nil || result.Before != nil || result.RecordStatus != nil) {
					t.Fatal("成立しなかった記録から実行情報を推定しています")
				}
				code, summaryJSON, _ := invokeStatus(t, executable, dir, "status", run.ID(), "--json-summary")
				summary := decodeSummary(t, summaryJSON)
				if code != 2 || summary.Status != "unknown" || summary.ChildExitCode != nil || summary.ChangeCount != nil || (kind != "未完了" && summary.CurrentChangeCount != nil) || !reflect.DeepEqual(summary.Issues, result.Issues) {
					t.Fatal("不成立の記録の要約で未確認の値または問題が失われています")
				}
				code, changesJSON, _ := invokeStatus(t, executable, dir, "status", run.ID(), "--json-changes")
				changes := decodeChanges(t, changesJSON)
				if code != 2 || changes.Status != "unknown" || changes.Changes != nil || (kind != "未完了" && changes.CurrentChanges != nil) || !reflect.DeepEqual(changes.Issues, result.Issues) {
					t.Fatal("不成立の記録の変更明細で未確認の値または問題が失われています")
				}
				if !reflect.DeepEqual(original, statusTree(t, dir)) {
					t.Fatal("失敗時にファイルを作成・変更しています")
				}
			})
		}
	})

	t.Run("引数の検査", func(t *testing.T) {
		id := strings.Repeat("a", 32)
		for _, args := range [][]string{
			{}, {"--json"}, {privateStatusValue}, {id, privateStatusValue}, {strings.Repeat("A", 32)},
			{"../" + privateStatusValue}, {"--json", id}, {id, "--json", "--json"}, {privateStatusValue, "--json"},
		} {
			dir := t.TempDir()
			original := statusTree(t, dir)
			code, stdout, stderr := invokeStatus(t, executable, dir, append([]string{"status"}, args...)...)
			if code != 64 || stderr == "" {
				t.Fatalf("引数エラーの終了コード=%d", code)
			}
			if slices.Contains(args, "--json") {
				result := decodeStatus(t, stdout)
				if result.Status != "invalid_arguments" || result.RunID != "" {
					t.Fatal("不正な引数の生値をJSONへ転記しています")
				}
			} else if stdout != "" {
				t.Fatal("引数エラーの診断は標準エラーへ出す必要があります")
			}
			if !reflect.DeepEqual(original, statusTree(t, dir)) {
				t.Fatal("引数エラーでファイルを変更しています")
			}
		}
	})
}

func TestStatusOutputFailure(t *testing.T) {
	dir, run, before := statusFixture(t)
	if _, err := run.Complete(0, &before, false); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	for _, args := range [][]string{{"status", run.ID()}, {"status", run.ID(), "--json"}, {"status", run.ID(), "--json-summary"}, {"status", run.ID(), "--json-changes"}} {
		var stderr bytes.Buffer
		if code := cli.Run(args, failingWriter{}, &stderr); code != 2 {
			t.Fatalf("statusの出力失敗時の終了コード=%d、期待値=2", code)
		}
		if !strings.Contains(stderr.String(), "書き込めません") || strings.Contains(stderr.String(), "非公開エラー") {
			t.Fatal("出力失敗の診断に問題があります")
		}
	}
}
