package input_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/shouhon/checkstamp/internal/input"
)

func TestExclusionPatterns(t *testing.T) {
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"**/__pycache__/**", "src/__pycache__", true},
		{"**/__pycache__/**", "__pycache__/sample.pyc", true},
		{"**/__pycache__/**", "src/deep/__PYCACHE__/sample.pyc", true},
		{"**/__pycache__/**", "src/sample.py", false},
		{"*.pyc", "sample.pyc", true},
		{"*.pyc", "src/sample.pyc", false},
		{"src/*.pyc", "src/deep/sample.pyc", false},
		{"src/**/cache", "src/cache/child", true},
		{"src/**/cache", "src/a/b/cache/child", true},
		{"src/**/cache", "other/cache", false},
		{"src/[ab]?.tmp", "src/a1.tmp", true},
		{"src/[ab]?.tmp", "src/c1.tmp", false},
		{"src/日本語 *", "src/日本語 一時/file", true},
		{"**", ".", true},
		{"*", ".", false},
	} {
		t.Run(tc.pattern+"/"+tc.name, func(t *testing.T) {
			matcher, err := input.NewExclusions([]string{tc.pattern})
			if err != nil || matcher.Match(tc.name) != tc.want {
				t.Fatalf("除外の照合が一致しません: %v", err)
			}
		})
	}
	matcher, err := input.NewExclusions([]string{"SRC/*.PYC", "**/cache/**", "src/*.pyc"})
	if err != nil || !reflect.DeepEqual(matcher.Patterns(), []string{"**/cache/**", "src/*.pyc"}) {
		t.Fatal("除外条件が正規化されていません")
	}
	for _, pattern := range []string{"", "/cache", "../cache", "src/../cache", "./cache", "src//cache", "cache/", `src\cache`, "C:cache", "src/**cache", "src/[", "src/cache.", "src/cache ", "src/\n非公開値"} {
		if _, err := input.NewExclusions([]string{pattern}); !errors.Is(err, input.ErrExcludePattern) || strings.Contains(err.Error(), "非公開値") {
			t.Fatalf("不正な除外条件の診断が安全ではありません: %v", err)
		}
	}
}

func TestExcludedCachesDoNotHideSourceChanges(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "src/変更.py", "前")
	write(t, dir, "src/削除.py", "前")
	before, err := input.Collect(dir, []string{"src"}, "**/__pycache__/**")
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "src/__pycache__/sample.pyc", "キャッシュ")
	write(t, dir, "src/nested/__pycache__/sample.pyc", "キャッシュ")
	// 除外していない空ディレクトリは従来どおり検出する。
	after, err := input.Recollect(dir, before.Inputs, before.ExcludePatterns...)
	if err != nil || len(input.Diff(before, after)) != 1 || input.Diff(before, after)[0].Path != "src/nested" {
		t.Fatalf("キャッシュ以外の追加が失われています: %v", err)
	}
	before = after
	write(t, dir, "src/__pycache__/sample.pyc", "更新")
	write(t, dir, "src/__pycache__/追加.pyc", "追加")
	after, err = input.Recollect(dir, before.Inputs, before.ExcludePatterns...)
	if err != nil || len(input.Diff(before, after)) != 0 || !reflect.DeepEqual(after.Excluded, []string{"src/__pycache__", "src/nested/__pycache__"}) {
		t.Fatalf("キャッシュの変更が入力の差分になっています: %v", err)
	}
	write(t, dir, "src/変更.py", "後")
	write(t, dir, "src/追加.py", "追加")
	if err := os.Remove(filepath.Join(dir, "src/削除.py")); err != nil {
		t.Fatal(err)
	}
	after, err = input.Recollect(dir, before.Inputs, before.ExcludePatterns...)
	changes := input.Diff(before, after)
	if err != nil || len(changes) != 3 {
		t.Fatalf("ソースの変更・追加・削除を検出できません: %v", err)
	}
	for i, want := range []struct{ name, kind string }{{"src/削除.py", "removed"}, {"src/変更.py", "modified"}, {"src/追加.py", "added"}} {
		if changes[i].Path != want.name || changes[i].Kind != want.kind {
			t.Fatalf("ソースの差分が一致しません: %v", changes)
		}
	}
	if _, err := input.Collect(dir, []string{"src", "src/__pycache__/sample.pyc"}, "**/__pycache__/**"); !errors.Is(err, input.ErrInputExcluded) {
		t.Fatal("明示した入力を除外できてしまいます")
	}
	if _, err := input.Collect(dir, []string{"../外部"}, "**"); !errors.Is(err, input.ErrArguments) {
		t.Fatal("除外によって範囲外の入力を許可しています")
	}
}

func TestExcludedLinkIsStillRejected(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "src/sample.py", "本文")
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "src/__pycache__")); err != nil {
		t.Skipf("シンボリックリンクを作成できません: %v", err)
	}
	if _, err := input.Collect(dir, []string{"src"}, "**/__pycache__/**"); !errors.Is(err, input.ErrUnsupported) {
		t.Fatal("除外条件に一致するリンクを拒否していません")
	}
}
