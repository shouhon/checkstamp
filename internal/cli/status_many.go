package cli

import (
	"fmt"
	"io"

	"github.com/shouhon/checkstamp/internal/record"
)

func runMultipleStatus(ids []string, stdout, stderr io.Writer, jsonOutput, summary, changes bool) int {
	// 詳細な入力一覧を全件分保持せず、指定順に照合して1つのJSONへ書き込む。
	write := func(format string, args ...any) bool {
		if _, err := fmt.Fprintf(stdout, format, args...); err != nil {
			fmt.Fprintln(stderr, "checkstamp: 標準出力へ書き込めませんでした。")
			return false
		}
		return true
	}
	if jsonOutput && !write("{\"schema_version\":%d,\"command\":\"status\",\"runs\":[", record.SchemaVersion) {
		return 2
	}
	overall := "valid"
	for i, id := range ids {
		result := newStatusResult(id)
		checkStatus(&result)
		if result.Status == "unknown" {
			fmt.Fprintf(stderr, "checkstamp: 実行ID %s\n", id)
			reportUnknownStatus(stderr, result)
		}
		overall = aggregateStatus(overall, result.Status)
		if jsonOutput {
			if i > 0 && !write(",") {
				return 2
			}
			if !writeStatusJSON(stdout, stderr, result, summary, changes) {
				return 2
			}
		} else if !writeStatusText(stdout, stderr, result) {
			return 2
		}
	}
	if jsonOutput {
		if !write("],\"status\":%q}\n", overall) {
			return 2
		}
	} else if !write("全体の判定: %s（%d件）\n", overall, len(ids)) {
		return 2
	}
	return statusExitCode(overall)
}

func aggregateStatus(first, second string) string {
	for _, status := range []string{"unknown", "interrupted", "failed", "unstable", "stale", "valid"} {
		if first == status || second == status {
			return status
		}
	}
	return "unknown"
}
