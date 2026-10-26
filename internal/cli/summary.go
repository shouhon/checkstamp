package cli

import (
	"github.com/shouhon/checkstamp/internal/input"
	"github.com/shouhon/checkstamp/internal/record"
)

// 一覧や差分の明細を持たず、入力件数に比例して出力が増えない要約。
type summaryResult struct {
	SchemaVersion      int            `json:"schema_version"`
	Command            string         `json:"command"`
	RunID              string         `json:"run_id"`
	Status             string         `json:"status"`
	ChildExitCode      *int           `json:"child_exit_code"`
	ChangeCount        *int           `json:"change_count"`
	CurrentChangeCount *int           `json:"current_change_count"`
	Issues             []record.Issue `json:"issues"`
}

func changeCount(changes []input.Change, available bool) *int {
	if !available {
		return nil
	}
	count := len(changes)
	return &count
}
