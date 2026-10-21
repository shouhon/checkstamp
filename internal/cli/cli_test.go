package cli_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/shouhon/checkstamp/internal/cli"
	"github.com/shouhon/checkstamp/internal/version"
)

func TestGuidance(t *testing.T) {
	for _, tc := range []struct {
		name string
		arg  string
		want string
	}{
		{name: "日本語ヘルプ", arg: "--help", want: "判定の有効範囲:"},
		{name: "埋め込んだ版番号", arg: "--version", want: "checkstamp " + version.String() + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := cli.Run([]string{tc.arg}, &stdout, &stderr); code != 0 {
				t.Fatalf("終了コード = %d、期待値 = 0", code)
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("標準出力に必要な案内がありません: %q", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("標準エラーが空ではありません: %q", stderr.String())
			}
		})
	}
}

func TestInvalidArgumentsDoNotDiscloseValues(t *testing.T) {
	const privateValue = "検証用の非公開引数"
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "引数なし"},
		{name: "未知の引数", args: []string{privateValue}},
		{name: "ヘルプの余分な引数", args: []string{"--help", privateValue}},
		{name: "版表示の余分な引数", args: []string{"--version", privateValue}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := cli.Run(tc.args, &stdout, &stderr); code != 64 {
				t.Fatalf("終了コード = %d、期待値 = 64", code)
			}
			if stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatal("引数エラーの診断は標準エラーだけに出す必要があります")
			}
			if strings.Contains(stderr.String(), privateValue) {
				t.Fatal("引数の生値が診断に含まれています")
			}
		})
	}
}

func TestUnavailableCommandsNeverReportSuccess(t *testing.T) {
	const privateValue = "検証用の非公開引数"
	for _, tc := range []struct {
		name string
		code int
	}{
		{name: "run", code: 125},
		{name: "status", code: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := cli.Run([]string{tc.name, privateValue}, &stdout, &stderr); code != tc.code {
				t.Fatalf("終了コード = %d、期待値 = %d", code, tc.code)
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), "未実装") {
				t.Fatal("未実装であることを標準エラーに示す必要があります")
			}
			if strings.Contains(stderr.String(), privateValue) {
				t.Fatal("引数の生値が診断に含まれています")
			}
		})
	}
}

func TestOutputFailure(t *testing.T) {
	var stderr bytes.Buffer
	if code := cli.Run([]string{"--version"}, failingWriter{}, &stderr); code != 125 {
		t.Fatalf("終了コード = %d、期待値 = 125", code)
	}
	if !strings.Contains(stderr.String(), "書き込めません") {
		t.Fatal("出力の失敗を診断していません")
	}
	if strings.Contains(stderr.String(), "検証用の非公開エラー") {
		t.Fatal("エラーの生値が診断に含まれています")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("検証用の非公開エラー")
}
