package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/shouhon/checkstamp/internal/cli"
	"github.com/shouhon/checkstamp/internal/record"
)

type summaryOutput struct {
	SchemaVersion      int            `json:"schema_version"`
	Command            string         `json:"command"`
	RunID              string         `json:"run_id"`
	Status             string         `json:"status"`
	ChildExitCode      *int           `json:"child_exit_code"`
	ChangeCount        *int           `json:"change_count"`
	CurrentChangeCount *int           `json:"current_change_count"`
	Issues             []record.Issue `json:"issues"`
	Exclusions         *struct {
		Base     string   `json:"base"`
		Patterns []string `json:"patterns"`
	} `json:"exclusions,omitempty"`
}

func decodeSummary(t *testing.T, output string) summaryOutput {
	t.Helper()
	var result summaryOutput
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatal("要約の標準出力がJSONオブジェクト1件ではありません")
	}
	var fields map[string]json.RawMessage
	wantFields := 8
	if result.Exclusions != nil {
		wantFields++
	}
	if err := json.Unmarshal([]byte(output), &fields); err != nil || len(fields) != wantFields {
		t.Fatal("要約の項目が不足しているか、詳細が混入しています")
	}
	if result.SchemaVersion != record.SchemaVersion || (result.Command != "run" && result.Command != "status") || result.Issues == nil {
		t.Fatal("要約の共通情報が不足しています")
	}
	if strings.Contains(output, runMarker) || strings.Contains(output, privateStatusValue) {
		t.Fatal("引数や入力の本文が要約に含まれています")
	}
	return result
}

func TestSummaryInvalidArguments(t *testing.T) {
	id := strings.Repeat("a", 32)
	for _, args := range [][]string{
		{"run", "--json-summary"},
		{"run", "--json-summary", "--label", runMarker, "--input", "src"},
		{"run", "--json-summary", "--json-summary", "--label", runMarker, "--input", "src", "--", "program.exe"},
		{"run", "--json", "--json-summary", "--label", runMarker, "--input", "src", "--", "program.exe"},
		{"run", "--json-summary", "--json", "--label", runMarker, "--input", "src", "--", "program.exe"},
		{"status", "--json-summary"},
		{"status", runMarker, "--json-summary"},
		{"status", "--json-summary", id},
		{"status", id, "--json-summary", "--json-summary"},
		{"status", id, "--json", "--json-summary"},
		{"status", id, "--json-summary", "--json"},
	} {
		var stdout, stderr bytes.Buffer
		code := cli.Run(args, &stdout, &stderr)
		result := decodeSummary(t, stdout.String())
		if code != 64 || result.Status != "invalid_arguments" || result.RunID != "" || result.ChildExitCode != nil || result.ChangeCount != nil || result.CurrentChangeCount != nil || len(result.Issues) != 1 || result.Issues[0].Code != "invalid_arguments" {
			t.Fatal("要約の引数エラーで終了コード・未確認の値・問題が失われています")
		}
		if strings.Contains(stderr.String(), runMarker) {
			t.Fatal("不正な引数の生値が診断に含まれています")
		}
	}
}
