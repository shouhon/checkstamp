package cli

import (
	"github.com/shouhon/checkstamp/internal/input"
	"github.com/shouhon/checkstamp/internal/record"
)

// 一覧や差分の明細を持たず、入力件数に比例して出力が増えない要約。
type summaryResult struct {
	SchemaVersion      int             `json:"schema_version"`
	Command            string          `json:"command"`
	RunID              string          `json:"run_id"`
	Status             string          `json:"status"`
	ChildExitCode      *int            `json:"child_exit_code"`
	ChangeCount        *int            `json:"change_count"`
	CurrentChangeCount *int            `json:"current_change_count"`
	Issues             []record.Issue  `json:"issues"`
	Exclusions         *exclusionScope `json:"exclusions,omitempty"`
}

// 除外条件は短い出力でも確認できるよう、入力一覧とは分けて返す。
type exclusionScope struct {
	Base     string   `json:"base"`
	Patterns []string `json:"patterns"`
}

type changesResult struct {
	summaryResult
	Changes        []input.Change `json:"changes"`
	CurrentChanges []input.Change `json:"current_changes"`
}

func exclusions(snapshot *input.Snapshot) *exclusionScope {
	if snapshot == nil || len(snapshot.ExcludePatterns) == 0 {
		return nil
	}
	return &exclusionScope{Base: "working_directory", Patterns: snapshot.ExcludePatterns}
}

func availableChanges(changes []input.Change, available bool) []input.Change {
	if !available {
		return nil
	}
	return changes
}

func changeCount(changes []input.Change, available bool) *int {
	if !available {
		return nil
	}
	count := len(changes)
	return &count
}
