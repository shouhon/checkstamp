package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/shouhon/checkstamp/internal/cli"
	"github.com/shouhon/checkstamp/internal/record"
)

type listOutput struct {
	SchemaVersion    int              `json:"schema_version"`
	Command          string           `json:"command"`
	Status           string           `json:"status"`
	Runs             []record.Summary `json:"runs"`
	HasMore          *bool            `json:"has_more"`
	UnavailableCount *int             `json:"unavailable_count"`
	Issues           []record.Issue   `json:"issues"`
	Notice           string           `json:"notice"`
}

func decodeList(t *testing.T, output string) listOutput {
	t.Helper()
	var result listOutput
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || result.SchemaVersion != record.SchemaVersion || result.Command != "list" || result.Issues == nil || !strings.Contains(result.Notice, "status <実行ID>") {
		t.Fatal("一覧のJSON1件と案内の取り決めが成立していません")
	}
	for _, forbidden := range []string{"arguments_sha256", "working_directory", "before", "after", "entries", "current_changes", privateStatusValue} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("一覧に不要な情報が含まれています: %s", forbidden)
		}
	}
	return result
}

func testListExecutable(t *testing.T, executable string) {
	t.Run("記録なしと別用途と欠損", func(t *testing.T) {
		for _, kind := range []string{"未作成", "専用だが空", "別用途", "runs欠損"} {
			t.Run(kind, func(t *testing.T) {
				dir := t.TempDir()
				if kind == "専用だが空" || kind == "runs欠損" {
					store, err := record.Open(dir)
					if err != nil {
						t.Fatal(err)
					}
					store.Close()
					if kind == "runs欠損" {
						if err := os.Remove(filepath.Join(dir, ".checkstamp/runs")); err != nil {
							t.Fatal(err)
						}
					}
				} else if kind == "別用途" {
					if err := os.WriteFile(filepath.Join(dir, ".checkstamp"), []byte(privateStatusValue), 0600); err != nil {
						t.Fatal(err)
					}
				}
				original := statusTree(t, dir)
				for _, jsonOutput := range []bool{false, true} {
					args := []string{"list"}
					if jsonOutput {
						args = append(args, "--json-summary")
					}
					code, stdout, stderr := invokeStatus(t, executable, dir, args...)
					wantCode := 0
					if kind == "別用途" || kind == "runs欠損" {
						wantCode = 2
					}
					if code != wantCode || (stderr == "") != (wantCode == 0) {
						t.Fatalf("終了コードまたは診断が不正です: %d %s", code, stderr)
					}
					if jsonOutput {
						result := decodeList(t, stdout)
						if wantCode == 0 {
							if result.Status != "complete" || result.Runs == nil || len(result.Runs) != 0 || result.HasMore == nil || *result.HasMore || result.UnavailableCount == nil || *result.UnavailableCount != 0 {
								t.Fatal("記録なしを確認できていません")
							}
						} else if result.Status != "unknown" || result.Runs != nil || result.HasMore != nil || result.UnavailableCount != nil || len(result.Issues) == 0 {
							t.Fatal("読取不能を空の一覧と区別できていません")
						}
					} else if !strings.Contains(stdout, "status <実行ID>") {
						t.Fatal("現在との照合が必要だと案内していません")
					}
				}
				if !reflect.DeepEqual(original, statusTree(t, dir)) {
					t.Fatal("一覧が保存先を作成・変更しました")
				}
			})
		}
	})
	t.Run("順序と完全一致と件数と保存時の判定", func(t *testing.T) {
		dir, first, before := statusFixture(t)
		if _, err := first.Complete(0, &before, false); err != nil {
			t.Fatal(err)
		}
		store, err := record.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		var tied []string
		latest := ""
		for i := range 22 {
			label := "別のラベル"
			if i < 2 {
				label = "unit"
			} else if i == 2 {
				label = "Unit"
			}
			run, err := store.Start(label, filepath.Join(dir, "実行しない.exe"), []string{privateStatusValue}, before)
			if err != nil {
				t.Fatal(err)
			}
			saved, err := run.Complete(2, &before, false)
			if err != nil {
				t.Fatal(err)
			}
			// ファイルの更新日時と異なる順序、同じ開始日時でも順序が決まることを試す。
			saved.StartedAt = time.Date(2025, 1, 1, 0, 0, max(i-1, 0), 0, time.UTC)
			finished := saved.StartedAt.Add(time.Second)
			saved.FinishedAt = &finished
			data, err := json.Marshal(saved)
			if err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				data = bytes.Replace(data, []byte(`"schema_version":2`), []byte(`"schema_version":1`), 1)
				data = bytes.ReplaceAll(data, []byte(`"exclude_patterns":[],`), nil)
			}
			if err := os.WriteFile(filepath.Join(dir, ".checkstamp/runs", run.ID()+".json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if i < 2 {
				tied = append(tied, run.ID())
			}
			latest = run.ID()
		}
		slices.Sort(tied)
		// 入力が消えても一覧は保存時の判定を保ち、現在をvalidと判定しない。
		if err := os.RemoveAll(filepath.Join(dir, "src")); err != nil {
			t.Fatal(err)
		}
		original := statusTree(t, dir)
		for _, tc := range []struct {
			args  []string
			count int
			more  bool
			first string
		}{
			{nil, 20, true, first.ID()},
			{[]string{"--label", "unit"}, 2, false, tied[0]},
			{[]string{"--label", "unit", "--limit", "1"}, 1, true, tied[0]},
			{[]string{"--label", "Unit"}, 1, false, ""},
			{[]string{"--label", "見つからない"}, 0, false, ""},
			{[]string{"--label", "別のラベル", "--limit", "1000"}, 19, false, latest},
		} {
			args := append([]string{"list", "--json-summary"}, tc.args...)
			code, stdout, stderr := invokeStatus(t, executable, dir, args...)
			result := decodeList(t, stdout)
			if code != 0 || stderr != "" || result.Status != "complete" || len(result.Runs) != tc.count || result.HasMore == nil || *result.HasMore != tc.more || result.UnavailableCount == nil || *result.UnavailableCount != 0 {
				t.Fatalf("一覧の抽出結果が不正です: %v %d %s", tc.args, code, stdout)
			}
			if tc.first != "" && result.Runs[0].RunID != tc.first {
				t.Fatal("開始日時または実行IDの順序が違います")
			}
			if tc.first == first.ID() && (result.Runs[0].RecordedStatus == nil || *result.Runs[0].RecordedStatus != "valid") {
				t.Fatal("一覧が保存時の判定を変更しました")
			}
			if tc.count == 2 && (result.Runs[1].RunID != tied[1] || *result.Runs[0].ChildExitCode != 2 || *result.Runs[0].RecordedStatus != "failed") {
				t.Fatal("同時刻の順序、保存した判定、終了コードが違います")
			}
		}
		code, stdout, stderr := invokeStatus(t, executable, dir, "list", "--label", "unit", "--limit", "1")
		if code != 0 || stderr != "" || !strings.Contains(stdout, tied[0]) || strings.Contains(stdout, tied[1]) || !strings.Contains(stdout, "保存時の判定: failed") || !strings.Contains(stdout, "終了日時（UTC）: 2025-01-01T00:00:01Z") || !strings.Contains(stdout, "表示件数を超える") {
			t.Fatal("通常表示に選択情報や件数制限の案内がありません")
		}
		if !reflect.DeepEqual(original, statusTree(t, dir)) {
			t.Fatal("一覧が記録・入力を書き換えたか、子コマンドを再実行しました")
		}
	})
	t.Run("不成立記録を成功や記録なしとしない", func(t *testing.T) {
		for _, kind := range []string{"未完了", "破損", "未知形式", "読取不能", "不正な名前", "リンク"} {
			t.Run(kind, func(t *testing.T) {
				dir, run, before := statusFixture(t)
				name := filepath.Join(dir, ".checkstamp/runs", run.ID()+".json")
				if kind != "未完了" {
					if _, err := run.Complete(0, &before, false); err != nil {
						t.Fatal(err)
					}
				}
				data, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				wantIssue := "incomplete"
				switch kind {
				case "破損":
					data, wantIssue = []byte(privateStatusValue), "record_corrupt"
				case "未知形式":
					data, wantIssue = bytes.Replace(data, []byte(`"schema_version":2`), []byte(`"schema_version":999`), 1), "unsupported_schema"
				case "読取不能", "リンク":
					wantIssue = "record_read_failed"
				}
				if err := os.WriteFile(name, data, 0600); err != nil {
					t.Fatal(err)
				}
				if kind == "不正な名前" {
					if err := os.Rename(name, filepath.Join(dir, ".checkstamp/runs", privateStatusValue+".json")); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "リンク" {
					if err := os.Remove(name); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(filepath.Join(dir, "src/入力"), name); err != nil {
						t.Fatal(err)
					}
				}
				var lock syscall.Handle
				if kind == "読取不能" {
					namePtr, err := syscall.UTF16PtrFromString(name)
					if err != nil {
						t.Fatal(err)
					}
					lock, err = syscall.CreateFile(namePtr, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, 0, 0)
					if err != nil {
						t.Fatal(err)
					}
					defer syscall.CloseHandle(lock)
				}
				if kind == "未完了" {
					code, stdout, _ := invokeStatus(t, executable, dir, "list")
					if code != 2 || !strings.Contains(stdout, "終了日時（UTC）: 未取得") {
						t.Fatal("未完了の通常表示が終了日時を推定しています")
					}
				}
				for _, filter := range [][]string{nil, {"--label", "別ラベル", "--limit", "1"}} {
					args := append([]string{"list", "--json-summary"}, filter...)
					code, stdout, stderr := invokeStatus(t, executable, dir, args...)
					result := decodeList(t, stdout)
					if code != 2 || stderr == "" || result.Status != "partial" || result.Runs == nil || result.UnavailableCount == nil || *result.UnavailableCount != 1 || len(result.Issues) == 0 {
						t.Fatalf("不成立記録を区別していません: %d %s", code, stdout)
					}
					if kind == "不正な名前" || kind == "未完了" && len(filter) != 0 {
						if len(result.Runs) != 0 {
							t.Fatal("不正な名前または他ラベルを一覧に含めています")
						}
						continue
					}
					if len(result.Runs) != 1 || result.Runs[0].RunID != run.ID() || result.Runs[0].Issues[0].Code != wantIssue {
						t.Fatal("読取不成立の種類または実行IDを取得できません")
					}
					item := result.Runs[0]
					if kind == "未完了" {
						if item.RecordedStatus == nil || *item.RecordedStatus != "unknown" || item.ChildExitCode != nil || item.FinishedAt != nil {
							t.Fatal("未完了を終了済みと推定しています")
						}
					} else if item.Label != nil || item.StartedAt != nil || item.RecordedStatus != nil || item.ChildExitCode != nil {
						t.Fatal("破損・未対応・読取不能から情報を推定しています")
					}
				}
			})
		}
	})
}

func TestListArgumentsAndOutputFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"--label"}, {"--label", " "}, {"--label", "a", "--label", "b"},
		{"--label", "a\x00b"}, {"--limit"}, {"--limit", "0"}, {"--limit", "-1"},
		{"--limit", "+1"}, {"--limit", "1.5"}, {"--limit", "1001"}, {"--limit", "999999999999999999999"},
		{"--limit", "2", "--limit", "3"}, {"--json-summary", "--json-summary"},
		{"--json"}, {"--json-changes"}, {privateStatusValue}, {"--help", privateStatusValue},
	} {
		var stdout, stderr bytes.Buffer
		code := cli.Run(append([]string{"list", "--json-summary"}, args...), &stdout, &stderr)
		result := decodeList(t, stdout.String())
		if code != 64 || stderr.Len() == 0 || strings.Contains(stderr.String(), privateStatusValue) || result.Status != "invalid_arguments" || result.Runs != nil || result.UnavailableCount != nil {
			t.Fatalf("引数エラーを安全に返していません: %v", args)
		}
	}
	for _, args := range [][]string{{"list"}, {"list", "--json-summary"}} {
		var stderr bytes.Buffer
		if code := cli.Run(args, failingWriter{}, &stderr); code != 2 || !strings.Contains(stderr.String(), "書き込めません") || strings.Contains(stderr.String(), "検証用の非公開エラー") {
			t.Fatal("一覧の出力失敗が安全な終了コード2になりません")
		}
	}
}
