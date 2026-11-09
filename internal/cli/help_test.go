package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/shouhon/checkstamp/internal/cli"
)

func testSubcommandHelp(t *testing.T, executable string) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".checkstamp"), []byte(privateStatusValue), 0600); err != nil {
		t.Fatal(err)
	}
	original := statusTree(t, dir)
	for _, command := range []string{"run", "status", "list"} {
		for _, flag := range []string{"--help", "-h"} {
			code, stdout, stderr := invokeStatus(t, executable, dir, command, flag)
			if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "checkstamp "+command+" - ") || !strings.Contains(stdout, "使い方:") || !strings.Contains(stdout, "使用例:") || !strings.Contains(stdout, "オプション:") {
				t.Fatalf("サブコマンドの使い方を確認できません: %s %s %d %s", command, flag, code, stderr)
			}
		}
		for _, args := range [][]string{{command, "--help", privateStatusValue}, {command, privateStatusValue, "-h"}} {
			code, stdout, stderr := invokeStatus(t, executable, dir, args...)
			if code != 64 || stdout != "" || stderr == "" {
				t.Fatal("ヘルプへの余分な引数が引数エラーになりません")
			}
		}
	}
	if !reflect.DeepEqual(original, statusTree(t, dir)) {
		t.Fatal("ヘルプが作業ディレクトリを書き換えました")
	}
}

func TestSubcommandHelpOutputFailure(t *testing.T) {
	for _, command := range []string{"run", "status", "list"} {
		var stderr bytes.Buffer
		if code := cli.Run([]string{command, "--help"}, failingWriter{}, &stderr); code != 125 || !strings.Contains(stderr.String(), "書き込めません") || strings.Contains(stderr.String(), "検証用の非公開エラー") {
			t.Fatal("ヘルプの出力失敗を安全に通知していません")
		}
	}
}
