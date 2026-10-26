package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/shouhon/checkstamp/internal/cli"
	"github.com/shouhon/checkstamp/internal/input"
	"github.com/shouhon/checkstamp/internal/record"
)

const runMarker = "試験専用の非公開値"

type runOutput struct {
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
	Before, After *input.Snapshot
	Changes       []input.Change `json:"changes"`
	Issues        []record.Issue `json:"issues"`
	Interrupted   bool           `json:"interrupted"`
}

func runDirectory(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "日本語 空白")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"変更", "削除"} {
		if err := os.WriteFile(filepath.Join(dir, "src", name), []byte("実行前"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func invokeRun(t *testing.T, executable, dir string, args ...string) (int, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	command.WaitDelay = 3 * time.Second
	command.Dir = dir
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if ctx.Err() != nil {
		t.Fatal("実行試験が制限時間内に終了しませんでした")
	}
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatal(err)
		}
	}
	return command.ProcessState.ExitCode(), stdout.String(), stderr.String()
}

func decodeRun(t *testing.T, text string) runOutput {
	t.Helper()
	var result runOutput
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatal("標準出力がJSONオブジェクト1件ではありません")
	}
	if result.SchemaVersion != 1 || result.Command != "run" || result.Tool.Name != "checkstamp" || result.Coverage.Omitted == nil || result.Issues == nil || result.Changes == nil {
		t.Fatal("実行結果の共通情報が不足しています")
	}
	if strings.Contains(text, runMarker) {
		t.Fatal("引数や出力の本文が結果JSONに含まれています")
	}
	return result
}

func assertRunRecord(t *testing.T, dir string, result runOutput) record.Record {
	t.Helper()
	store, err := record.OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saved, err := store.Load(result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != result.Status || !reflect.DeepEqual(saved.ChildExitCode, result.ChildExitCode) || !reflect.DeepEqual(saved.Issues, result.Issues) || !reflect.DeepEqual(saved.Changes, result.Changes) || saved.Interrupted != result.Interrupted {
		t.Fatal("保存記録と結果JSONが一致していません")
	}
	data, err := os.ReadFile(filepath.Join(dir, ".checkstamp", "runs", result.RunID+".json"))
	if err != nil || bytes.Contains(data, []byte(runMarker)) {
		t.Fatal("保存記録を確認できないか、非公開値が含まれています")
	}
	return saved
}

func TestRunExecutable(t *testing.T) {
	bin := t.TempDir()
	executable, helper := filepath.Join(bin, "checkstamp.exe"), filepath.Join(bin, "検証 子.exe")
	for _, target := range []struct{ output, source string }{{executable, "../../cmd/checkstamp"}, {helper, "./testdata/command"}} {
		build := exec.Command("go", "build", "-mod=readonly", "-o", target.output, target.source)
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("受入試験用ビルドに失敗しました: %v\n%s", err, output)
		}
	}
	base := func(jsonOutput bool) []string {
		args := []string{"run", "--label", "実行試験", "--input", "src"}
		if jsonOutput {
			args = append(args, "--json")
		}
		return append(args, "--", helper)
	}

	t.Run("引数と環境と作業場所と出力", func(t *testing.T) {
		for _, jsonOutput := range []bool{false, true} {
			dir := runDirectory(t)
			t.Setenv("CHECKSTAMP_TEST_DIRECTORY", dir)
			t.Setenv("CHECKSTAMP_TEST_VALUE", runMarker)
			childArgs := []string{"contract", "", "空白 日本語", `引用"符`, `末尾\`, `空白 末尾\`, "--json", "--json-summary", "--", runMarker}
			code, stdout, stderr := invokeRun(t, executable, dir, append(base(jsonOutput), childArgs...)...)
			if code != 0 || !strings.Contains(stderr, runMarker+"-標準エラー") {
				t.Fatalf("引数または出力の契約が成立しません: 終了コード=%d、標準エラー=%s", code, stderr)
			}
			if jsonOutput {
				result := decodeRun(t, stdout)
				saved := assertRunRecord(t, dir, result)
				if result.Status != "valid" || saved.Conditions.WorkingDirectory != dir || saved.Conditions.Executable != helper || saved.Conditions.ArgumentsSHA256 != record.HashArguments(childArgs) || !strings.Contains(stderr, runMarker+"-標準出力") {
					t.Fatal("実行条件またはJSON時の出力先が一致しません")
				}
				if saved.StartedAt.Location() != time.UTC || saved.FinishedAt.Location() != time.UTC {
					t.Fatal("実行日時がUTCではありません")
				}
				if err := os.WriteFile(filepath.Join(dir, "src", "変更"), []byte("実行後の変更"), 0600); err != nil {
					t.Fatal(err)
				}
				statusCode, output, _ := invokeRun(t, executable, dir, "status", result.RunID, "--json")
				if statusCode != 1 || decodeStatus(t, output).Status != "stale" {
					t.Fatal("実行後の変更がstaleになりません")
				}
			} else if stdout != runMarker+"-標準出力" || !strings.Contains(stderr, "実行ID:") || !strings.Contains(stderr, "判定: valid") {
				t.Fatal("通常実行の出力先が一致しません")
			}
		}
	})
	t.Run("終了コードを保持", func(t *testing.T) {
		for _, want := range []int{0, 1, 2, 64, 125, 0xffffffff} {
			dir := runDirectory(t)
			code, stdout, _ := invokeRun(t, executable, dir, append(base(true), "exit", strconv.Itoa(want))...)
			result := decodeRun(t, stdout)
			if code != want || result.ChildExitCode == nil || *result.ChildExitCode != want {
				t.Fatalf("終了コード=%d、期待値=%d", code, want)
			}
			assertRunRecord(t, dir, result)
		}
	})
	t.Run("実行中の差分と優先順位", func(t *testing.T) {
		for _, exit := range []int{0, 2} {
			dir := runDirectory(t)
			code, stdout, _ := invokeRun(t, executable, dir, append(base(true), "change", strconv.Itoa(exit))...)
			result := decodeRun(t, stdout)
			want := "unstable"
			if exit != 0 {
				want = "failed"
			}
			if code != exit || result.Status != want || len(result.Changes) != 3 {
				t.Fatalf("実行中の差分が反映されません: %+v", result)
			}
			assertRunRecord(t, dir, result)
		}
	})
	t.Run("起動前の拒否", func(t *testing.T) {
		for _, kind := range []string{"入力なし", "範囲外", "別用途", "解決失敗", "起動不能", "暗黙のシェル禁止"} {
			t.Run(kind, func(t *testing.T) {
				dir := runDirectory(t)
				args := append(base(true), "tree")
				switch kind {
				case "入力なし":
					args[4] = "存在しない入力"
				case "範囲外":
					args[4] = "../範囲外"
				case "別用途":
					if err := os.Mkdir(filepath.Join(dir, ".checkstamp"), 0700); err != nil {
						t.Fatal(err)
					}
				case "解決失敗":
					args[len(args)-2] = filepath.Join(dir, runMarker+".exe")
				case "起動不能", "暗黙のシェル禁止":
					ext := ".exe"
					if kind == "暗黙のシェル禁止" {
						ext = ".cmd"
					}
					name := filepath.Join(dir, "起動不可"+ext)
					if err := os.WriteFile(name, []byte(runMarker), 0600); err != nil {
						t.Fatal(err)
					}
					args[len(args)-2] = name
				}
				code, stdout, stderr := invokeRun(t, executable, dir, args...)
				if code != 125 && !(kind == "範囲外" && code == 64) || decodeRun(t, stdout).Status == "valid" || strings.Contains(stderr, runMarker) {
					t.Fatal("起動前の失敗を安全に扱えていません")
				}
				if _, err := os.Stat(filepath.Join(dir, "ready")); !os.IsNotExist(err) {
					t.Fatal("拒否した子が起動しています")
				}
			})
		}
	})
	t.Run("保存失敗でも終了コードを保持", func(t *testing.T) {
		dir := runDirectory(t)
		code, stdout, stderr := invokeRun(t, executable, dir, append(base(true), "save-failure")...)
		result := decodeRun(t, stdout)
		if code != 64 || result.Status != "unknown" || result.ChildExitCode == nil || *result.ChildExitCode != 64 || !strings.Contains(stderr, "保存できません") {
			t.Fatalf("保存失敗時の結果が一致しません: %d %+v %s", code, result, stderr)
		}
		name := filepath.Join(dir, ".checkstamp", "runs", result.RunID+".json")
		t.Cleanup(func() { os.Chmod(name, 0600) })
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var saved record.Record
		if json.Unmarshal(data, &saved) != nil || saved.Phase != "started" || saved.Status != "unknown" {
			t.Fatal("保存失敗で開始記録が成功へ変わっています")
		}
	})
	t.Run("再読取失敗でも終了コードを保持", func(t *testing.T) {
		dir := runDirectory(t)
		probe := filepath.Join(dir, "リンク作成の確認")
		if err := os.Symlink(helper, probe); err != nil {
			t.Skip("シンボリックリンクの作成権限がありません")
		}
		if err := os.Remove(probe); err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr := invokeRun(t, executable, dir, append(base(true), "read-failure")...)
		result := decodeRun(t, stdout)
		if code != 0 || result.Status != "unknown" || result.After != nil || !strings.Contains(stderr, "読み取れません") {
			t.Fatal("入力の再読取失敗を成功扱いしています")
		}
		assertRunRecord(t, dir, result)
	})
	t.Run("結果出力失敗でも終了コードを保持", func(t *testing.T) {
		dir := runDirectory(t)
		t.Chdir(dir)
		for _, flag := range []string{"--json", "--json-summary"} {
			args := base(true)
			args[5] = flag
			var stderr bytes.Buffer
			code := cli.Run(append(args, "exit", "64"), failingWriter{}, &stderr)
			if code != 64 || !strings.Contains(stderr.String(), "書き込めません") || strings.Contains(stderr.String(), "検証用の非公開エラー") {
				t.Fatal("結果出力の失敗で子の終了コードが変わったか、エラー本文が漏れています")
			}
		}
	})
	t.Run("1832ファイルでも短い要約と詳細記録を保持", func(t *testing.T) {
		dir := runDirectory(t)
		for i := 2; i < 1832; i++ {
			writeStatusInput(t, dir, "入力-"+strconv.Itoa(i), runMarker)
		}
		t.Setenv("CHECKSTAMP_TEST_DIRECTORY", dir)
		t.Setenv("CHECKSTAMP_TEST_VALUE", runMarker)
		childArgs := []string{"contract", "", "空白 日本語", `引用"符`, `末尾\`, `空白 末尾\`, "--json", "--json-summary", "--", runMarker}
		args := []string{"run", "--label", "要約の試験", "--input", "src", "--json-summary", "--", helper}
		code, stdout, stderr := invokeRun(t, executable, dir, append(args, childArgs...)...)
		result := decodeSummary(t, stdout)
		if code != 0 || result.Status != "valid" || result.ChildExitCode == nil || *result.ChildExitCode != 0 || result.ChangeCount == nil || *result.ChangeCount != 0 || result.CurrentChangeCount != nil || len(result.Issues) != 0 || len(stdout) > 1024 {
			t.Fatal("大量の入力に対する実行の要約が短くないか、判定と件数が一致しません")
		}
		if !strings.Contains(stderr, runMarker+"-標準出力") || !strings.Contains(stderr, runMarker+"-標準エラー") {
			t.Fatal("要約時の子の両出力が標準エラーに流れていません")
		}
		name := filepath.Join(dir, ".checkstamp", "runs", result.RunID+".json")
		recorded, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var saved record.Record
		if err := json.Unmarshal(recorded, &saved); err != nil || saved.After == nil || len(saved.Before.Entries) != 1833 || len(saved.After.Entries) != 1833 || bytes.Contains(recorded, []byte(runMarker)) {
			t.Fatal("要約時にも保存すべき詳細な入力一覧が失われています")
		}
		fullCode, fullJSON, _ := invokeRun(t, executable, dir, "status", result.RunID, "--json")
		full := decodeStatus(t, fullJSON)
		if fullCode != 0 || full.Current == nil || len(full.Current.Entries) != 1833 || len(fullJSON) < 200*1024 {
			t.Fatal("既存のJSON出力から詳細な入力一覧が失われています")
		}
		code, stdout, _ = invokeRun(t, executable, dir, "status", result.RunID, "--json-summary")
		current := decodeSummary(t, stdout)
		if code != 0 || current.Status != "valid" || current.ChangeCount == nil || *current.ChangeCount != 0 || current.CurrentChangeCount == nil || *current.CurrentChangeCount != 0 || len(stdout) > 1024 {
			t.Fatal("大量の入力に対する照合の要約が短くないか、件数が一致しません")
		}
		t.Logf("1832ファイル: 詳細JSON %dバイト、照合要約 %dバイト", len(fullJSON), len(stdout))
		if err := os.RemoveAll(filepath.Join(dir, "src")); err != nil {
			t.Fatal(err)
		}
		code, stdout, _ = invokeRun(t, executable, dir, "status", result.RunID, "--json-summary")
		current = decodeSummary(t, stdout)
		if code != 1 || current.Status != "stale" || current.ChangeCount == nil || *current.ChangeCount != 0 || current.CurrentChangeCount == nil || *current.CurrentChangeCount != 1833 || len(stdout) > 1024 || len(current.Issues) != 1 || current.Issues[0].Code != "current_input_changed" {
			t.Fatal("大量の差分が明細の出力へ戻っているか、件数と問題が失われています")
		}
		afterStatus, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(recorded, afterStatus) {
			t.Fatal("要約による照合が詳細記録を書き換えています")
		}
	})
	t.Run("要約でも実行中の差分と終了コードを保持", func(t *testing.T) {
		for _, exit := range []int{0, 64} {
			dir := runDirectory(t)
			args := []string{"run", "--label", "要約の試験", "--input", "src", "--json-summary", "--", helper, "change", strconv.Itoa(exit)}
			code, stdout, _ := invokeRun(t, executable, dir, args...)
			result := decodeSummary(t, stdout)
			want := "unstable"
			if exit != 0 {
				want = "failed"
			}
			if code != exit || result.Status != want || result.ChildExitCode == nil || *result.ChildExitCode != exit || result.ChangeCount == nil || *result.ChangeCount != 3 || result.CurrentChangeCount != nil {
				t.Fatal("要約から実行中の差分または子の終了コードが失われています")
			}
			_, fullJSON, _ := invokeRun(t, executable, dir, "status", result.RunID, "--json")
			full := decodeStatus(t, fullJSON)
			if full.Status != result.Status || len(full.Changes) != 3 || !reflect.DeepEqual(full.Issues, result.Issues) {
				t.Fatal("要約と詳細記録の判定・問題が一致しません")
			}
		}
	})
	t.Run("要約の起動前失敗では未確認の件数をnullにする", func(t *testing.T) {
		dir := runDirectory(t)
		code, stdout, _ := invokeRun(t, executable, dir, "run", "--label", "要約の試験", "--input", runMarker, "--json-summary", "--", helper, "exit", "0")
		result := decodeSummary(t, stdout)
		if code != 125 || result.Status != "unknown" || result.ChildExitCode != nil || result.ChangeCount != nil || result.CurrentChangeCount != nil || len(result.Issues) != 1 || result.Issues[0].Code != "input_collection_failed" {
			t.Fatal("起動前失敗の要約から未確認の値または問題が失われています")
		}
	})
	t.Run("大量出力を流す", func(t *testing.T) {
		dir := runDirectory(t)
		command := exec.Command(executable, append(base(true), "volume")...)
		command.Dir = dir
		var stdout bytes.Buffer
		var stderr byteCounter
		command.Stdout, command.Stderr = &stdout, &stderr
		if err := command.Run(); err != nil {
			t.Fatal(err)
		}
		if stderr.count < 16*1024*1024 || decodeRun(t, stdout.String()).Status != "valid" {
			t.Fatal("大量出力が途中で欠けています")
		}
	})
	t.Run("同時実行", func(t *testing.T) {
		dir := runDirectory(t)
		store, err := record.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		store.Close()
		var wg sync.WaitGroup
		ids := make(chan string, 8)
		for range 8 {
			wg.Go(func() {
				code, stdout, _ := invokeRun(t, executable, dir, append(base(true), "exit", "0")...)
				result := decodeRun(t, stdout)
				if code != 0 || result.Status != "valid" {
					t.Error("同時実行が失敗しました")
				}
				assertRunRecord(t, dir, result)
				ids <- result.RunID
			})
		}
		wg.Wait()
		close(ids)
		seen := map[string]bool{}
		for id := range ids {
			if seen[id] {
				t.Fatal("実行IDが衝突しました")
			}
			seen[id] = true
		}
	})
	t.Run("中断と子孫と強制終了", func(t *testing.T) {
		for _, mode := range []string{"notify", "ctrl-c", "ignore", "repeat", "early", "kill", "normal", "normal-many"} {
			t.Run(mode, func(t *testing.T) {
				dir := runDirectory(t)
				childMode := "tree"
				if mode == "ignore" || mode == "repeat" {
					childMode = "tree-ignore"
				}
				if mode == "normal" {
					childMode = "tree-exit"
				}
				if mode == "normal-many" {
					childMode = "tree-exit-many"
					for index := range 32 {
						name := filepath.Join(dir, "src", "保持-"+strconv.Itoa(index))
						if err := os.WriteFile(name, []byte("実行前"), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				args := append(base(true), childMode)
				program := executable
				if mode != "normal" && mode != "normal-many" {
					args = append([]string{"console", "wrapper.pid", mode, executable}, args...)
					program = helper
				}
				code, stdout, stderr := invokeRun(t, program, dir, args...)
				if mode == "kill" {
					if code == 0 || stdout != "" {
						t.Fatal("強制終了が成功扱いになっています")
					}
					files, _ := filepath.Glob(filepath.Join(dir, ".checkstamp", "runs", "*.json"))
					if len(files) != 1 {
						t.Fatal("開始記録がありません")
					}
					id := strings.TrimSuffix(filepath.Base(files[0]), ".json")
					statusCode, output, _ := invokeRun(t, executable, dir, "status", id, "--json")
					if statusCode != 2 || decodeStatus(t, output).Status != "unknown" {
						t.Fatal("強制終了後がunknownではありません")
					}
				} else {
					result := decodeRun(t, stdout)
					if mode == "normal" || mode == "normal-many" {
						if code != 0 || result.Status != "valid" {
							t.Fatalf("通常終了が失敗しました: 終了コード=%d、状態=%s、問題=%v", code, result.Status, result.Issues)
						}
					} else if mode == "early" {
						if result.Status == "valid" || !result.Interrupted {
							t.Fatalf("起動直後の中断を失っています: %d %+v %s", code, result, stderr)
						}
					} else {
						want := 2
						if mode == "ignore" || mode == "repeat" {
							want = 125
						}
						if code != want || result.Status != "interrupted" || !result.Interrupted {
							t.Fatalf("中断結果が一致しません: %d %+v %s", code, result, stderr)
						}
						if _, err := os.Stat(filepath.Join(dir, "notified")); err != nil {
							t.Fatal("子へ中断が通知されていません")
						}
					}
					if result.ChildExitCode != nil {
						assertRunRecord(t, dir, result)
					}
				}
				pidFiles := []string{"child.pid", "grandchild.pid"}
				if mode == "normal-many" {
					for index := 1; index < 32; index++ {
						pidFiles = append(pidFiles, "grandchild-"+strconv.Itoa(index)+".pid")
					}
				}
				for _, name := range pidFiles {
					data, err := os.ReadFile(filepath.Join(dir, name))
					if mode == "early" && os.IsNotExist(err) {
						continue
					}
					if err != nil {
						t.Fatal(err)
					}
					pid, err := strconv.Atoi(string(data))
					if err != nil {
						t.Fatal(err)
					}
					handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
					if err == nil {
						state, waitErr := syscall.WaitForSingleObject(handle, 0)
						syscall.CloseHandle(handle)
						if waitErr != nil || state != syscall.WAIT_OBJECT_0 {
							t.Fatalf("%sのプロセスが残っています", name)
						}
					}
				}
			})
		}
	})
}

type byteCounter struct{ count int }

func (w *byteCounter) Write(data []byte) (int, error) { w.count += len(data); return len(data), nil }

func TestRunInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{}, {"--json"}, {"--label", runMarker, "--input", "src"},
		{"--label", runMarker, "--label", "重複", "--input", "src", "--", "program.exe"},
		{"--label", runMarker, "--input", "src", "--json", "--json", "--", "program.exe"},
		{"--label", runMarker, "--input", "src", "--", ""},
	} {
		var stdout, stderr bytes.Buffer
		code := cli.Run(append([]string{"run"}, args...), &stdout, &stderr)
		if code != 64 || strings.Contains(stdout.String()+stderr.String(), runMarker) {
			t.Fatal("不正な引数の扱いが一致しません")
		}
		if slices.Contains(args, "--json") && decodeRun(t, stdout.String()).Status != "invalid_arguments" {
			t.Fatal("引数エラーのJSONがありません")
		}
	}
}
