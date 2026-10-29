package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/shouhon/checkstamp/internal/cli"
	"github.com/shouhon/checkstamp/internal/input"
	"github.com/shouhon/checkstamp/internal/record"
)

type changesOutput struct {
	summaryOutput
	Changes        []input.Change `json:"changes"`
	CurrentChanges []input.Change `json:"current_changes"`
}

func TestChangesInvalidArguments(t *testing.T) {
	for _, flags := range [][]string{
		{"--json-changes"},
		{"--json", "--json-changes"}, {"--json-changes", "--json"},
		{"--json-summary", "--json-changes"}, {"--json-changes", "--json-summary"},
		{"--json-changes", "--json-changes"},
	} {
		for _, command := range []string{"run", "status"} {
			args := []string{command}
			if command == "run" {
				args = append(args, "--label", runMarker, "--input", "src")
			} else {
				args = append(args, strings.Repeat("a", 32))
			}
			args = append(args, flags...)
			// フラグ単独の例は必須値の欠落、それ以外は併用・重複を検査する。
			if len(flags) == 1 && command == "status" {
				args[1] = runMarker
			}
			if len(flags) > 1 && command == "run" {
				args = append(args, "--", "program.exe")
			}
			var stdout, stderr bytes.Buffer
			code := cli.Run(args, &stdout, &stderr)
			var result summaryOutput
			if strings.Contains(stdout.String(), `"changes":`) {
				result = decodeChanges(t, stdout.String()).summaryOutput
			} else {
				result = decodeSummary(t, stdout.String())
			}
			if code != 64 || result.Status != "invalid_arguments" || result.ChildExitCode != nil || result.ChangeCount != nil || result.CurrentChangeCount != nil || len(result.Issues) != 1 || strings.Contains(stderr.String(), runMarker) {
				t.Fatal("変更明細モードの不正な引数の処理が一致しません")
			}
		}
	}
}

func decodeChanges(t *testing.T, output string) changesOutput {
	t.Helper()
	var result changesOutput
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatal("変更明細の標準出力がJSONオブジェクト1件ではありません")
	}
	var fields map[string]json.RawMessage
	wantFields := 10
	if result.Exclusions != nil {
		wantFields++
	}
	if err := json.Unmarshal([]byte(output), &fields); err != nil || len(fields) != wantFields || result.SchemaVersion != record.SchemaVersion || result.Issues == nil {
		t.Fatal("変更明細の共通情報が不足しているか、入力一覧が混入しています")
	}
	if (result.Changes == nil) != (result.ChangeCount == nil) || (result.CurrentChanges == nil) != (result.CurrentChangeCount == nil) {
		t.Fatal("変更件数と明細の未確認の値が一致しません")
	}
	if result.ChangeCount != nil && *result.ChangeCount != len(result.Changes) || result.CurrentChangeCount != nil && *result.CurrentChangeCount != len(result.CurrentChanges) {
		t.Fatal("変更件数と明細が一致しません")
	}
	if strings.Contains(output, runMarker) || strings.Contains(output, privateStatusValue) {
		t.Fatal("引数や入力の本文が変更明細に含まれています")
	}
	return result
}

func testExclusionsAndChanges(t *testing.T, executable, helper string) {
	const pattern = "**/__pycache__/**"
	t.Run("既定の収集範囲を維持", func(t *testing.T) {
		dir := runDirectory(t)
		code, output, _ := invokeRun(t, executable, dir, "run", "--label", "既定の範囲", "--input", "src", "--json-changes", "--", helper, "cache")
		result := decodeChanges(t, output)
		if code != 0 || result.Status != "unstable" || len(result.Changes) != 2 || result.CurrentChanges != nil || result.Exclusions != nil {
			t.Fatal("既定の収集範囲または変更明細が変わりました")
		}
	})
	for _, mode := range []string{"", "--json", "--json-summary", "--json-changes"} {
		t.Run("キャッシュを除外/"+mode, func(t *testing.T) {
			dir := runDirectory(t)
			writeStatusInput(t, dir, "変更.py", "実行前")
			writeStatusInput(t, dir, "削除.py", "実行前")
			args := []string{"run", "--label", "明示的な除外", "--input", "src", "--exclude", pattern}
			if mode != "" {
				args = append(args, mode)
			}
			code, output, stderr := invokeRun(t, executable, dir, append(args, "--", helper, "cache")...)
			if code != 0 {
				t.Fatalf("キャッシュの生成で実行が失敗しました: %d、%s", code, stderr)
			}
			files, err := filepath.Glob(filepath.Join(dir, ".checkstamp/runs/*.json"))
			if err != nil || len(files) != 1 {
				t.Fatal("詳細記録が1件保存されていません")
			}
			id := strings.TrimSuffix(filepath.Base(files[0]), ".json")
			store, err := record.OpenReadOnly(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			saved, err := store.Load(id)
			if err != nil || saved.Status != "valid" || saved.ChildExitCode == nil || *saved.ChildExitCode != 0 || saved.After == nil || !reflect.DeepEqual(saved.Before.ExcludePatterns, []string{pattern}) || !reflect.DeepEqual(saved.After.Excluded, []string{"src/__pycache__"}) {
				t.Fatalf("除外条件・適用範囲・判定が保存されていません: %v", err)
			}
			switch mode {
			case "":
				if output != "" || !strings.Contains(stderr, pattern) || !strings.Contains(stderr, "判定: valid") {
					t.Fatal("通常表示で除外条件を確認できません")
				}
			case "--json":
				result := decodeRun(t, output)
				if result.Status != "valid" || !reflect.DeepEqual(result.Before.ExcludePatterns, []string{pattern}) {
					t.Fatal("詳細JSONの除外条件が一致しません")
				}
			default:
				var result summaryOutput
				if mode == "--json-summary" {
					result = decodeSummary(t, output)
				} else {
					changes := decodeChanges(t, output)
					result = changes.summaryOutput
					if changes.Changes == nil || len(changes.Changes) != 0 || changes.CurrentChanges != nil {
						t.Fatal("実行の変更なしと未照合を区別できません")
					}
				}
				if result.Status != "valid" || result.Exclusions == nil || result.Exclusions.Base != "working_directory" || !reflect.DeepEqual(result.Exclusions.Patterns, []string{pattern}) {
					t.Fatal("短いJSONで除外条件と適用基準を確認できません")
				}
			}
			if err := os.WriteFile(filepath.Join(dir, "src/__pycache__/sample.pyc"), []byte("更新"), 0600); err != nil {
				t.Fatal(err)
			}
			code, output, _ = invokeStatus(t, executable, dir, "status", id, "--json-changes")
			result := decodeChanges(t, output)
			if code != 0 || result.Status != "valid" || result.CurrentChanges == nil || len(result.CurrentChanges) != 0 || result.Exclusions == nil {
				t.Fatal("キャッシュの更新で現在の判定が変わっています")
			}
			writeStatusInput(t, dir, "変更.py", "実行後")
			writeStatusInput(t, dir, "追加.py", "追加")
			if err := os.Remove(filepath.Join(dir, "src/削除.py")); err != nil {
				t.Fatal(err)
			}
			original := statusTree(t, dir)
			code, output, _ = invokeStatus(t, executable, dir, "status", id, "--json-changes")
			result = decodeChanges(t, output)
			if code != 1 || result.Status != "stale" || len(result.Changes) != 0 || len(result.CurrentChanges) != 3 {
				t.Fatal("除外後もソースの変更・追加・削除を検出できません")
			}
			for i, want := range []struct{ path, kind string }{{"src/削除.py", "removed"}, {"src/変更.py", "modified"}, {"src/追加.py", "added"}} {
				if result.CurrentChanges[i].Path != want.path || result.CurrentChanges[i].Kind != want.kind {
					t.Fatal("ソースの変更パス・変更種類が一致しません")
				}
			}
			if !reflect.DeepEqual(original, statusTree(t, dir)) {
				t.Fatal("変更明細の照合が入力・記録を書き換えています")
			}
			_, text, _ := invokeStatus(t, executable, dir, "status", id)
			if !strings.Contains(text, pattern) {
				t.Fatal("通常の照合結果で除外条件を確認できません")
			}
		})
	}
	t.Run("多数の入力のうち1件だけを返す", func(t *testing.T) {
		dir := runDirectory(t)
		for i := range 185 {
			writeStatusInput(t, dir, "source-"+strconv.Itoa(i)+".py", "変更しない本文")
		}
		code, output, _ := invokeRun(t, executable, dir, "run", "--label", "変更明細", "--input", "src", "--json-changes", "--", helper, "exit", "0")
		result := decodeChanges(t, output)
		if code != 0 || result.Status != "valid" || result.Changes == nil || len(result.Changes) != 0 {
			t.Fatal("変更なしの実行結果を取得できません")
		}
		writeStatusInput(t, dir, "source-0.py", "変更後")
		code, output, _ = invokeStatus(t, executable, dir, "status", result.RunID, "--json-changes")
		result = decodeChanges(t, output)
		if code != 1 || result.Status != "stale" || len(result.CurrentChanges) != 1 || result.CurrentChanges[0].Path != "src/source-0.py" || result.CurrentChanges[0].Before.SHA256 == result.CurrentChanges[0].After.SHA256 || len(output) > 2048 || strings.Contains(output, "source-1.py") {
			t.Fatal("変更されていない入力を省いた明細になっていません")
		}
		_, full, _ := invokeStatus(t, executable, dir, "status", result.RunID, "--json")
		if len(decodeStatus(t, full).Before.Entries) != 188 {
			t.Fatal("詳細記録の入力一覧が失われています")
		}
	})
	t.Run("形式1の記録も同じ範囲で照合", func(t *testing.T) {
		dir, run, before := statusFixture(t)
		if _, err := run.Complete(0, &before, false); err != nil {
			t.Fatal(err)
		}
		name := filepath.Join(dir, ".checkstamp/runs", run.ID()+".json")
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		legacy := strings.Replace(string(data), `"schema_version":2`, `"schema_version":1`, 1)
		legacy = strings.ReplaceAll(legacy, `,"exclude_patterns":[]`, "")
		if err := os.WriteFile(name, []byte(legacy), 0600); err != nil {
			t.Fatal(err)
		}
		code, output, _ := invokeStatus(t, executable, dir, "status", run.ID(), "--json-changes")
		if code != 0 || decodeChanges(t, output).Status != "valid" {
			t.Fatal("形式1の既存記録を照合できません")
		}
		writeStatusInput(t, dir, "追加.py", "追加")
		original := statusTree(t, dir)
		code, output, _ = invokeStatus(t, executable, dir, "status", run.ID(), "--json-changes")
		result := decodeChanges(t, output)
		if code != 1 || result.Status != "stale" || len(result.CurrentChanges) != 1 || result.CurrentChanges[0].Path != "src/追加.py" || !reflect.DeepEqual(original, statusTree(t, dir)) {
			t.Fatal("形式1の入力範囲または読み取り専用の照合が維持されていません")
		}
	})
	t.Run("変更明細でも子の引数と出力先を保持", func(t *testing.T) {
		dir := runDirectory(t)
		t.Setenv("CHECKSTAMP_TEST_DIRECTORY", dir)
		t.Setenv("CHECKSTAMP_TEST_VALUE", runMarker)
		args := []string{"run", "--label", "子の引数", "--input", "src", "--json-changes", "--", helper,
			"contract", "", "空白 日本語", `引用"符`, `末尾\`, `空白 末尾\`, "--json", "--json-summary", "--json-changes", "--exclude", "**/__pycache__/**", "--", runMarker}
		code, output, stderr := invokeRun(t, executable, dir, args...)
		if code != 0 || decodeChanges(t, output).Status != "valid" || !strings.Contains(stderr, runMarker+"-標準出力") || !strings.Contains(stderr, runMarker+"-標準エラー") {
			t.Fatal("子の引数か標準出力・標準エラーの転送先が変わっています")
		}
	})
	t.Run("失敗と不明な実行", func(t *testing.T) {
		for _, tc := range []struct {
			child, status string
			args          []string
			code, changes int
		}{
			{"exit", "failed", []string{"2"}, 2, 0},
			{"change", "unstable", []string{"0"}, 0, 3},
			{"read-failure", "unknown", nil, 0, -1},
		} {
			dir := runDirectory(t)
			args := []string{"run", "--label", "変更明細の失敗", "--input", "src", "--json-changes", "--", helper, tc.child}
			code, output, _ := invokeRun(t, executable, dir, append(args, tc.args...)...)
			result := decodeChanges(t, output)
			if code != tc.code || result.Status != tc.status || result.CurrentChanges != nil || (result.Changes == nil) != (tc.changes < 0) || tc.changes >= 0 && len(result.Changes) != tc.changes {
				t.Fatal("失敗・入力変更・照合不能の明細が一致しません")
			}
		}
	})
	t.Run("起動前の除外エラー", func(t *testing.T) {
		for _, pattern := range []string{"../非公開値", "**", "src/**", "[", ""} {
			dir := runDirectory(t)
			original := statusTree(t, dir)
			code, output, stderr := invokeRun(t, executable, dir, "run", "--label", "不正な除外", "--input", "src", "--exclude", pattern, "--json-changes", "--", helper, "cache")
			result := decodeChanges(t, output)
			if code != 64 || result.Status != "invalid_arguments" || result.Changes != nil || result.ChildExitCode != nil || result.RunID != "" || strings.Contains(stderr+output, "非公開値") || !reflect.DeepEqual(original, statusTree(t, dir)) {
				t.Fatal("不正な除外条件で起動・保存・情報の漏えいが発生しています")
			}
		}
	})
}
