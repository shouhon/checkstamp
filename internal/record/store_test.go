package record_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/shouhon/checkstamp/internal/input"
	"github.com/shouhon/checkstamp/internal/record"
)

func setup(t *testing.T) (string, *record.Store, input.Snapshot) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "入力"), []byte("検証用の非公開本文"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := input.Collect(dir, []string{"入力"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := record.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return dir, store, before
}

func start(t *testing.T, dir string, store *record.Store, before input.Snapshot) *record.Run {
	t.Helper()
	run, err := store.Start("同名のラベル", filepath.Join(dir, "検証.exe"), []string{"検証用の非公開引数", "", "引用\"と空白 日本語\\"}, before)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestStartedAndCompletedRecords(t *testing.T) {
	dir, store, before := setup(t)
	run := start(t, dir, store, before)
	started, err := store.Load(run.ID())
	if !errors.Is(err, record.ErrIncomplete) || started.Status != "unknown" || started.ChildExitCode != nil {
		t.Fatalf("開始済みの記録 = %+v、失敗 = %v", started, err)
	}
	finished, err := run.Complete(0, &before, false)
	if err != nil || finished.Status != "valid" {
		t.Fatalf("完了結果 = %+v、失敗 = %v", finished, err)
	}
	loaded, err := store.Load(run.ID())
	if err != nil || !reflect.DeepEqual(loaded, finished) {
		t.Fatalf("保存内容と完了結果が一致しません: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".checkstamp/runs", run.ID()+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"検証用の非公開引数", "検証用の非公開本文", "引用\"と空白"} {
		if bytes.Contains(data, []byte(private)) {
			t.Fatal("本文または引数の生値が記録に保存されています")
		}
	}
	if len(loaded.Coverage.Omitted) == 0 || len(loaded.Coverage.Unverified) == 0 {
		t.Fatal("有効範囲の制限が記録されていません")
	}
	if _, err := run.Complete(1, &before, false); !errors.Is(err, record.ErrWrite) {
		t.Fatal("完了記録を再度上書きできてしまいます")
	}
}

func TestOutcomePriorityRetainsCoexistingFacts(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		exit                          int
		interrupted, missing, changed bool
		status                        string
		issues                        []string
	}{
		{"成功", 0, false, false, false, "valid", nil},
		{"入力変更", 0, false, false, true, "unstable", []string{"input_changed"}},
		{"失敗と変更", 64, false, false, true, "failed", []string{"child_failed", "input_changed"}},
		{"中断と失敗と変更", 125, true, false, true, "interrupted", []string{"interrupted", "child_failed", "input_changed"}},
		{"読取失敗と中断", 2, true, true, false, "unknown", []string{"input_read_failed", "interrupted", "child_failed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, store, before := setup(t)
			run := start(t, dir, store, before)
			after := before
			if tc.changed {
				if err := os.WriteFile(filepath.Join(dir, "入力"), []byte("変更後"), 0600); err != nil {
					t.Fatal(err)
				}
				var err error
				after, err = input.Recollect(dir, before.Inputs)
				if err != nil {
					t.Fatal(err)
				}
			}
			current := &after
			if tc.missing {
				current = nil
			}
			result, err := run.Complete(tc.exit, current, tc.interrupted)
			if err != nil || result.Status != tc.status || *result.ChildExitCode != tc.exit {
				t.Fatalf("結果 = %+v、失敗 = %v", result, err)
			}
			var codes []string
			for _, issue := range result.Issues {
				codes = append(codes, issue.Code)
			}
			if !reflect.DeepEqual(codes, tc.issues) {
				t.Fatalf("併存する事実 = %v、期待値 = %v", codes, tc.issues)
			}
			if _, err := store.Load(run.ID()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOwnershipAndReadOnlyOpening(t *testing.T) {
	for _, kind := range []string{"未所有の空ディレクトリ", "別用途のファイル", "不正な所有情報"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			storage := filepath.Join(dir, ".checkstamp")
			if kind == "別用途のファイル" {
				if err := os.WriteFile(storage, []byte("別用途"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(storage, 0700); err != nil {
					t.Fatal(err)
				}
				if kind == "不正な所有情報" {
					if err := os.WriteFile(filepath.Join(storage, "owner"), []byte("別用途"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if store, err := record.Open(dir); !errors.Is(err, record.ErrOwnership) {
				if store != nil {
					store.Close()
				}
				t.Fatalf("別用途の領域を拒否していません: %v", err)
			}
			if _, err := os.Stat(filepath.Join(storage, "runs")); err == nil {
				t.Fatal("未所有の領域へ書き込んでいます")
			}
		})
	}
	dir := t.TempDir()
	if store, err := record.OpenReadOnly(dir); err == nil {
		store.Close()
		t.Fatal("存在しない保存先を開いています")
	}
	children, err := os.ReadDir(dir)
	if err != nil || len(children) != 0 {
		t.Fatal("読取用の処理が保存先を作成しています")
	}
	dir, store, before := setup(t)
	run := start(t, dir, store, before)
	reader, err := record.OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.Load(run.ID()); !errors.Is(err, record.ErrIncomplete) {
		t.Fatal(err)
	}
	if _, err := reader.Start("書込禁止", filepath.Join(dir, "検証.exe"), nil, before); !errors.Is(err, record.ErrWrite) {
		t.Fatal("読取専用の保存先から書き込めます")
	}
}

func TestCorruptAndUnknownRecordsAreRejected(t *testing.T) {
	dir, store, before := setup(t)
	run := start(t, dir, store, before)
	if _, err := run.Complete(0, &before, false); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, ".checkstamp/runs", run.ID()+".json")
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		transform func([]byte) []byte
		want      error
	}{
		{"切断", func(b []byte) []byte { return b[:len(b)/2] }, record.ErrCorrupt},
		{"空", func([]byte) []byte { return nil }, record.ErrCorrupt},
		{"必要情報なし", func([]byte) []byte { return []byte(`{"schema_version":1}`) }, record.ErrCorrupt},
		{"未知形式", func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"schema_version":1`), []byte(`"schema_version":2`), 1)
		}, record.ErrSchema},
		{"余分な情報", func(b []byte) []byte { return append([]byte(`{"private":"検証用の非公開エラー",`), b[1:]...) }, record.ErrCorrupt},
		{"判定の矛盾", func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"status":"valid"`), []byte(`"status":"failed"`), 1)
		}, record.ErrCorrupt},
		{"重複キー", func(b []byte) []byte { return append([]byte(`{"status":"failed",`), b[1:]...) }, record.ErrCorrupt},
		{"終了コード欠落", func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"child_exit_code":0`), []byte(`"child_exit_code":null`), 1)
		}, record.ErrCorrupt},
		{"必須の真偽値欠落", func(b []byte) []byte { return bytes.Replace(b, []byte(`,"interrupted":false`), nil, 1) }, record.ErrCorrupt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(name, tc.transform(data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Load(run.ID()); !errors.Is(err, tc.want) {
				t.Fatalf("失敗 = %v、期待値 = %v", err, tc.want)
			} else if strings.Contains(err.Error(), "非公開エラー") {
				t.Fatal("エラーの本文が診断に混入しています")
			}
		})
	}
	if _, err := store.Load("../検証用の非公開ID"); !errors.Is(err, record.ErrID) {
		t.Fatal("範囲外の実行IDを拒否していません")
	}
}

func TestSaveFailureKeepsStartedRecord(t *testing.T) {
	dir, store, before := setup(t)
	run := start(t, dir, store, before)
	store.Close()
	result, err := run.Complete(64, &before, false)
	if !errors.Is(err, record.ErrWrite) || result.Status != "unknown" || *result.ChildExitCode != 64 {
		t.Fatalf("保存失敗時の結果 = %+v、失敗 = %v", result, err)
	}
	reader, err := record.OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if saved, err := reader.Load(run.ID()); !errors.Is(err, record.ErrIncomplete) || saved.Status != "unknown" {
		t.Fatal("保存失敗した実行が完了扱いになっています")
	}
	if _, err := store.Start("起動前", filepath.Join(dir, "検証.exe"), nil, before); !errors.Is(err, record.ErrWrite) {
		t.Fatalf("開始前の保存失敗 = %v", err)
	}
}

func TestConcurrentRunsDoNotOverwriteAndReadersSeeWholeRecords(t *testing.T) {
	dir, store, before := setup(t)
	const count = 24
	ids := make(chan string, count)
	failures := make(chan error, count)
	var group sync.WaitGroup
	for range count {
		group.Go(func() {
			ownStore, err := record.Open(dir)
			if err != nil {
				failures <- err
				return
			}
			defer ownStore.Close()
			run, err := ownStore.Start("同名のラベル", filepath.Join(dir, "検証.exe"), nil, before)
			if err != nil {
				failures <- err
				return
			}
			ids <- run.ID()
			var reading sync.WaitGroup
			reading.Go(func() {
				for range 20 {
					if _, err := store.Load(run.ID()); err != nil && !errors.Is(err, record.ErrIncomplete) {
						failures <- err
						return
					}
				}
			})
			if _, err := run.Complete(0, &before, false); err != nil {
				failures <- err
			}
			reading.Wait()
		})
	}
	group.Wait()
	close(ids)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	seen := make(map[string]bool)
	for id := range ids {
		if seen[id] {
			t.Fatal("実行IDが衝突しました")
		}
		seen[id] = true
		if r, err := store.Load(id); err != nil || r.Status != "valid" {
			t.Fatalf("並行保存した記録 = %+v、失敗 = %v", r, err)
		}
	}
	if len(seen) != count {
		t.Fatalf("保存件数 = %d、期待値 = %d", len(seen), count)
	}
	files, err := os.ReadDir(filepath.Join(dir, ".checkstamp/runs"))
	if err != nil || len(files) != count {
		t.Fatal("一時ファイルが残ったか、記録が失われています")
	}
}

func TestArgumentsHashPreservesBoundaries(t *testing.T) {
	groups := [][]string{nil, {""}, {"", ""}, {"a", "b"}, {"a b"}, {"ab"}, {"a", "", "b"}, {"日本語", `引用"と末尾\`}}
	seen := make(map[string]bool)
	for _, args := range groups {
		hash := record.HashArguments(args)
		if seen[hash] || len(hash) != 64 {
			t.Fatal("引数の境界を識別できません")
		}
		seen[hash] = true
	}
	if record.HashArguments(nil) != record.HashArguments([]string{}) {
		t.Fatal("引数なしの表現が一致しません")
	}
}

func TestBeforeSnapshotIsIndependentOfCaller(t *testing.T) {
	dir, store, before := setup(t)
	run := start(t, dir, store, before)
	before.Entries[0].SHA256 = strings.Repeat("0", 64)
	after, err := input.Recollect(dir, before.Inputs)
	if err != nil {
		t.Fatal(err)
	}
	result, err := run.Complete(0, &after, false)
	if err != nil || result.Status != "valid" {
		t.Fatal("呼出し元の変更で保存済みの開始条件が変わりました")
	}
	if _, err := json.Marshal(result); err != nil {
		t.Fatal(err)
	}
}

func TestStorageLinksAreRejected(t *testing.T) {
	for _, component := range []string{".checkstamp", ".checkstamp/owner", ".checkstamp/runs", "record"} {
		t.Run(component, func(t *testing.T) {
			dir, store, before := setup(t)
			run := start(t, dir, store, before)
			store.Close()
			name := filepath.Join(dir, component)
			target := t.TempDir()
			if component == "record" {
				name = filepath.Join(dir, ".checkstamp/runs", run.ID()+".json")
			}
			if component == ".checkstamp/owner" || component == "record" {
				target = filepath.Join(target, "外部ファイル")
				content := []byte("checkstamp storage\nschema_version: 1\n")
				if component == "record" {
					var err error
					content, err = os.ReadFile(name)
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(target, content, 0600); err != nil {
					t.Fatal(err)
				}
			}
			// この試験が作った対象だけを退避し、利用者の領域は操作しない。
			if err := os.Rename(name, name+"-saved"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, name); err != nil {
				t.Skipf("シンボリックリンクを作成できません: %v", err)
			}
			t.Cleanup(func() { os.Remove(name) })
			reader, err := record.OpenReadOnly(dir)
			if component == "record" {
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				if _, err := reader.Load(run.ID()); !errors.Is(err, record.ErrRead) {
					t.Fatalf("記録のリンクを拒否していません: %v", err)
				}
			} else if !errors.Is(err, record.ErrOwnership) {
				if reader != nil {
					reader.Close()
				}
				t.Fatalf("保存先のリンクを拒否していません: %v", err)
			}
		})
	}
}

func TestReplacementFailureDoesNotTruncateStartedRecord(t *testing.T) {
	dir, store, before := setup(t)
	run := start(t, dir, store, before)
	name := filepath.Join(dir, ".checkstamp/runs", run.ID()+".json")
	original, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(name, 0400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(name, 0600) })
	result, err := run.Complete(0, &before, false)
	if !errors.Is(err, record.ErrWrite) || result.Status != "unknown" || *result.ChildExitCode != 0 {
		t.Fatalf("置換失敗時の結果 = %+v、失敗 = %v", result, err)
	}
	retained, err := os.ReadFile(name)
	if err != nil || !bytes.Equal(original, retained) {
		t.Fatal("置換の失敗で開始記録が破損しました")
	}
	if _, err := store.Load(run.ID()); !errors.Is(err, record.ErrIncomplete) {
		t.Fatal("置換に失敗した実行を成功扱いしています")
	}
}

func TestMovedStorageDoesNotReportValid(t *testing.T) {
	dir, store, before := setup(t)
	run := start(t, dir, store, before)
	runs := filepath.Join(dir, ".checkstamp/runs")
	if err := os.Rename(runs, runs+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(runs, 0700); err != nil {
		t.Fatal(err)
	}
	result, err := run.Complete(0, &before, false)
	if !errors.Is(err, record.ErrWrite) || result.Status != "unknown" {
		t.Fatalf("保存先の移動後に成功扱いしています: %s、%v", result.Status, err)
	}
	if _, err := store.Load(run.ID()); !errors.Is(err, record.ErrRead) {
		t.Fatal("移動前の保存先から記録を読み出しています")
	}
	saved := filepath.Join(runs+"-moved", run.ID()+".json")
	data, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"phase":"started"`)) {
		t.Fatal("移動した保存先へ完了記録を書き込んでいます")
	}
}
