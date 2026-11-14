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
	"testing"

	"github.com/shouhon/checkstamp/internal/cli"
	"github.com/shouhon/checkstamp/internal/input"
	"github.com/shouhon/checkstamp/internal/record"
)

type multipleStatusOutput struct {
	SchemaVersion int               `json:"schema_version"`
	Command       string            `json:"command"`
	Status        string            `json:"status"`
	Runs          []json.RawMessage `json:"runs"`
}

func decodeMultipleStatus(t *testing.T, output string) multipleStatusOutput {
	t.Helper()
	var result multipleStatusOutput
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || result.SchemaVersion != record.SchemaVersion || result.Command != "status" || len(result.Runs) < 2 {
		t.Fatal("複数照合のJSON1件と共通項目が成立していません")
	}
	return result
}

func testMultipleStatusExecutable(t *testing.T, executable string) {
	t.Run("全件有効と指定順と単一結果との一致", func(t *testing.T) {
		dir, first, before := statusFixture(t)
		if _, err := first.Complete(0, &before, false); err != nil {
			t.Fatal(err)
		}
		store, err := record.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		second, err := store.Start("別の検証", executable, []string{privateStatusValue}, before)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := second.Complete(0, &before, false); err != nil {
			t.Fatal(err)
		}
		ids := []string{second.ID(), first.ID(), second.ID()}
		original := statusTree(t, dir)
		for _, flag := range []string{"--json", "--json-summary", "--json-changes"} {
			args := append([]string{"status"}, ids...)
			code, stdout, stderr := invokeStatus(t, executable, dir, append(args, flag)...)
			result := decodeMultipleStatus(t, stdout)
			if code != 0 || stderr != "" || result.Status != "valid" || len(result.Runs) != len(ids) {
				t.Fatal("全件有効の判定または件数が一致しません")
			}
			for i, raw := range result.Runs {
				_, single, _ := invokeStatus(t, executable, dir, "status", ids[i], flag)
				var got, want any
				if json.Unmarshal(raw, &got) != nil || json.Unmarshal([]byte(single), &want) != nil || !reflect.DeepEqual(got, want) {
					t.Fatal("指定順または単一IDのJSONとの一致が失われています")
				}
				if flag == "--json-summary" {
					decodeSummary(t, string(raw))
				}
			}
		}
		code, stdout, stderr := invokeStatus(t, executable, dir, append([]string{"status"}, ids...)...)
		if code != 0 || stderr != "" || !strings.Contains(stdout, "全体の判定: valid（3件）") || strings.Count(stdout, "実行ID:") != 3 {
			t.Fatal("通常表示に全件の結果と全体の判定がありません")
		}
		if !reflect.DeepEqual(original, statusTree(t, dir)) {
			t.Fatal("複数照合が記録・入力を変更または子を再実行しました")
		}
	})
	t.Run("優先順位と部分的な読取不成立", func(t *testing.T) {
		dir, stale, before := statusFixture(t)
		if _, err := stale.Complete(0, &before, false); err != nil {
			t.Fatal(err)
		}
		writeStatusInput(t, dir, "入力", "現在の変更")
		current, err := input.Collect(dir, []string{"src"})
		if err != nil {
			t.Fatal(err)
		}
		store, err := record.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		ids := map[string]string{"stale": stale.ID(), "missing": strings.Repeat("f", 32)}
		for _, status := range []string{"valid", "unstable", "failed", "interrupted", "incomplete", "corrupt"} {
			snapshot := current
			if status == "unstable" {
				snapshot = before
			}
			run, err := store.Start(status, executable, []string{privateStatusValue}, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			ids[status] = run.ID()
			if status == "incomplete" {
				continue
			}
			exit := 0
			if status == "failed" || status == "interrupted" {
				exit = 64
			}
			if _, err := run.Complete(exit, &current, status == "interrupted"); err != nil {
				t.Fatal(err)
			}
			if status == "corrupt" {
				if err := os.WriteFile(filepath.Join(dir, ".checkstamp", "runs", run.ID()+".json"), []byte(privateStatusValue), 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
		original := statusTree(t, dir)
		for _, tc := range []struct {
			statuses []string
			want     string
			code     int
		}{
			{[]string{"stale", "valid"}, "stale", 1},
			{[]string{"stale", "unstable", "valid"}, "unstable", 1},
			{[]string{"failed", "unstable", "stale", "valid"}, "failed", 1},
			{[]string{"failed", "interrupted", "valid"}, "interrupted", 1},
			{[]string{"interrupted", "missing", "valid"}, "unknown", 2},
			{[]string{"valid", "corrupt", "valid"}, "unknown", 2},
			{[]string{"incomplete", "valid"}, "unknown", 2},
		} {
			for _, reverse := range []bool{false, true} {
				statuses := slices.Clone(tc.statuses)
				if reverse {
					slices.Reverse(statuses)
				}
				args := []string{"status"}
				for _, status := range statuses {
					args = append(args, ids[status])
				}
				code, stdout, stderr := invokeStatus(t, executable, dir, append(args, "--json-summary")...)
				result := decodeMultipleStatus(t, stdout)
				if code != tc.code || result.Status != tc.want || len(result.Runs) != len(statuses) || (stderr != "") != (tc.want == "unknown") {
					t.Fatalf("全体の判定が一致しません: %v %d %s", statuses, code, result.Status)
				}
				for i, raw := range result.Runs {
					item := decodeSummary(t, string(raw))
					wantStatus := statuses[i]
					if wantStatus == "missing" || wantStatus == "corrupt" || wantStatus == "incomplete" {
						wantStatus = "unknown"
					}
					if item.RunID != ids[statuses[i]] || item.Status != wantStatus {
						t.Fatal("読取不成立の前後の結果を失っています")
					}
					if item.Status == "unknown" && (item.ChildExitCode != nil || item.ChangeCount != nil || len(item.Issues) == 0) {
						t.Fatal("未確認値を成功時の値で補っています")
					}
				}
			}
		}
		if !reflect.DeepEqual(original, statusTree(t, dir)) {
			t.Fatal("部分的に読めない複数照合が記録・入力を変更しました")
		}
	})
	t.Run("IDごとの入力と除外条件", func(t *testing.T) {
		dir := t.TempDir()
		store, err := record.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		args := []string{"status"}
		for _, root := range []string{"a", "b"} {
			if err := os.MkdirAll(filepath.Join(dir, root, "cache"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, root, "source"), []byte(privateStatusValue), 0600); err != nil {
				t.Fatal(err)
			}
			var patterns []string
			if root == "a" {
				patterns = []string{"a/cache/**"}
			}
			snapshot, err := input.Collect(dir, []string{root}, patterns...)
			if err != nil {
				t.Fatal(err)
			}
			run, err := store.Start(root, executable, nil, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := run.Complete(0, &snapshot, false); err != nil {
				t.Fatal(err)
			}
			args = append(args, run.ID())
			if err := os.WriteFile(filepath.Join(dir, root, "cache", "generated"), []byte(privateStatusValue), 0600); err != nil {
				t.Fatal(err)
			}
		}
		original := statusTree(t, dir)
		code, stdout, _ := invokeStatus(t, executable, dir, append(args, "--json-summary")...)
		result := decodeMultipleStatus(t, stdout)
		first, second := decodeSummary(t, string(result.Runs[0])), decodeSummary(t, string(result.Runs[1]))
		if code != 1 || result.Status != "stale" || first.Status != "valid" || first.Exclusions == nil || first.Exclusions.Base != "working_directory" || !slices.Equal(first.Exclusions.Patterns, []string{"a/cache/**"}) || second.Status != "stale" || second.Exclusions != nil || second.CurrentChangeCount == nil || *second.CurrentChangeCount != 1 {
			t.Fatal("IDごとの入力・除外条件で照合していません")
		}
		if !reflect.DeepEqual(original, statusTree(t, dir)) {
			t.Fatal("保存した除外条件を書き換えました")
		}
	})
}

func TestMultipleStatusInvalidArguments(t *testing.T) {
	t.Chdir(t.TempDir())
	id := strings.Repeat("a", 32)
	for _, args := range [][]string{
		{id, privateStatusValue, "--json-summary"}, {id, id, "--json-summary", privateStatusValue},
		{id, "--json-summary", id}, {id, id, "--json-summary", "--json-summary"},
		{id, id, "--json", "--json-summary"}, {id, id, "--exclude", privateStatusValue, "--json-summary"},
	} {
		var stdout, stderr bytes.Buffer
		code := cli.Run(append([]string{"status"}, args...), &stdout, &stderr)
		result := decodeSummary(t, stdout.String())
		if code != 64 || result.Status != "invalid_arguments" || result.RunID != "" || len(result.Issues) != 1 || strings.Contains(stderr.String(), privateStatusValue) || strings.Contains(stderr.String(), "有効性") {
			t.Fatal("全引数の検査前に照合したか、不正な値を転記しました")
		}
	}
}

type limitedStatusWriter struct{ remaining int }

func (w *limitedStatusWriter) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		written := w.remaining
		w.remaining = 0
		return written, io.ErrClosedPipe
	}
	w.remaining -= len(p)
	return len(p), nil
}

func TestMultipleStatusOutputFailure(t *testing.T) {
	dir, run, before := statusFixture(t)
	if _, err := run.Complete(0, &before, false); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	for _, flag := range []string{"", "--json", "--json-summary", "--json-changes"} {
		args := []string{"status", run.ID(), run.ID()}
		if flag != "" {
			args = append(args, flag)
		}
		var complete, diagnostics bytes.Buffer
		if code := cli.Run(args, &complete, &diagnostics); code != 0 {
			t.Fatal("出力失敗の比較用実行に失敗しました")
		}
		for _, remaining := range []int{0, complete.Len() / 2, complete.Len() - 1} {
			var stderr bytes.Buffer
			if code := cli.Run(args, &limitedStatusWriter{remaining: remaining}, &stderr); code != 2 || !strings.Contains(stderr.String(), "書き込めません") {
				t.Fatal("出力の開始・途中・終了の失敗を確認不能としていません")
			}
		}
	}
}
