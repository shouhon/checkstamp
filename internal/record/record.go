// Package record は、実行条件と入力一覧を検査可能な形式で保存する。
package record

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/shouhon/checkstamp/internal/input"
	"github.com/shouhon/checkstamp/internal/workspace"
)

// SchemaVersion は保存形式の版。製品の版番号とは独立して管理する。
const SchemaVersion = 1

var (
	ErrCorrupt    = errors.New("記録が破損しているか、必要な情報が不足しています。")
	ErrSchema     = errors.New("この保存形式の版には対応していません。")
	ErrIncomplete = errors.New("実行の完了が記録されていません。")
	ErrRead       = errors.New("記録を読み取れませんでした。")
	ErrWrite      = errors.New("記録を保存できませんでした。")
	ErrOwnership  = errors.New(".checkstamp をcheckstamp専用の保存先として確認できませんでした。")
	ErrID         = errors.New("実行IDの形式が正しくありません。")
)

// Tool は記録を作成した製品名と版番号を表す。
type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Conditions は再実行の復元を目的としない実行条件。引数の生値を持たない。
type Conditions struct {
	WorkingDirectory string `json:"working_directory"`
	Executable       string `json:"executable"`
	ArgumentsSHA256  string `json:"arguments_sha256"`
}

// Coverage は保存を省略した情報と未確認の条件を明示する。
type Coverage struct {
	Omitted    []string `json:"omitted"`
	Unverified []string `json:"unverified"`
}

// Issue は機械判定用の識別子と、機密値を含まない固定の説明を持つ。
type Issue struct {
	Code        string `json:"code"`
	Description string `json:"description"`
}

// Record は開始済みまたは完了済みの実行1件を表す。
type Record struct {
	SchemaVersion int             `json:"schema_version"`
	Tool          Tool            `json:"tool"`
	Command       string          `json:"command"`
	RunID         string          `json:"run_id"`
	Label         string          `json:"label"`
	Phase         string          `json:"phase"`
	Status        string          `json:"status"`
	ChildExitCode *int            `json:"child_exit_code"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    *time.Time      `json:"finished_at"`
	Conditions    Conditions      `json:"conditions"`
	Coverage      Coverage        `json:"coverage"`
	Before        input.Snapshot  `json:"before"`
	After         *input.Snapshot `json:"after"`
	Changes       []input.Change  `json:"changes"`
	Issues        []Issue         `json:"issues"`
	Interrupted   bool            `json:"interrupted"`
}

// HashArguments はJSON配列の表現をハッシュ化し、空文字や引数の境界を保持する。
func HashArguments(args []string) string {
	if args == nil {
		args = []string{}
	}
	data, _ := json.Marshal(args)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func coverage() Coverage {
	return Coverage{
		Omitted:    []string{"引数の生値", "環境変数の値", "標準出力と標準エラーの本文", "入力ファイルの本文"},
		Unverified: []string{"未指定のファイル", "環境変数と外部サービスの変化", "実行中に変更されて元に戻った内容"},
	}
}

func outcome(r Record) (string, []Issue) {
	issues := []Issue{}
	unknown := r.Phase != "completed" || r.After == nil
	if r.Phase != "completed" {
		issues = append(issues, Issue{"incomplete", "実行の完了が記録されていません。"})
	} else if r.After == nil {
		issues = append(issues, Issue{"input_read_failed", "実行後の入力を十分に読み取れませんでした。"})
	}
	if r.Interrupted {
		issues = append(issues, Issue{"interrupted", "中断を受けた事実を記録しました。"})
	}
	if r.ChildExitCode != nil && *r.ChildExitCode != 0 {
		issues = append(issues, Issue{"child_failed", "子プロセスが0以外の終了コードで終了しました。"})
	}
	if len(r.Changes) != 0 {
		issues = append(issues, Issue{"input_changed", "実行前後で入力が変わっています。"})
	}
	switch {
	case unknown:
		return "unknown", issues
	case r.Interrupted:
		return "interrupted", issues
	case r.ChildExitCode != nil && *r.ChildExitCode != 0:
		return "failed", issues
	case len(r.Changes) != 0:
		return "unstable", issues
	default:
		return "valid", issues
	}
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil && hash == strings.ToLower(hash)
}

func validateSnapshot(snapshot input.Snapshot, allowMissing bool) bool {
	if len(snapshot.Inputs) == 0 || snapshot.Entries == nil || snapshot.Excluded == nil || !slices.IsSorted(snapshot.Inputs) || !slices.IsSorted(snapshot.Excluded) {
		return false
	}
	roots := make(map[string]bool)
	for _, name := range snapshot.Inputs {
		normalized, err := workspace.Normalize(name)
		key := strings.ToLower(name)
		if err != nil || normalized != name || workspace.Excluded(name) != "" || roots[key] {
			return false
		}
		roots[key] = true
	}
	within := func(name string) bool {
		for root := range roots {
			if root == "." || name == root || strings.HasPrefix(name, root+"/") {
				return true
			}
		}
		return false
	}
	entries := make(map[string]input.Entry)
	previous := ""
	for _, entry := range snapshot.Entries {
		normalized, err := workspace.Normalize(entry.Path)
		key := strings.ToLower(entry.Path)
		if err != nil || normalized != entry.Path || workspace.Excluded(entry.Path) != "" || !within(key) || entry.Path <= previous {
			return false
		}
		if _, ok := entries[key]; ok {
			return false
		}
		if entry.Kind != "file" && entry.Kind != "directory" || entry.Kind == "file" && !validHash(entry.SHA256) || entry.Kind == "directory" && entry.SHA256 != "" {
			return false
		}
		previous = entry.Path
		entries[key] = entry
	}
	if !allowMissing {
		for root := range roots {
			if _, ok := entries[root]; !ok {
				return false
			}
		}
	}
	for name := range entries {
		if roots[name] {
			continue
		}
		parent := strings.ToLower(filepath.ToSlash(filepath.Dir(name)))
		if entry, ok := entries[parent]; !ok || entry.Kind != "directory" {
			return false
		}
	}
	seenExcluded := make(map[string]bool)
	for _, name := range snapshot.Excluded {
		key := strings.ToLower(name)
		normalized, err := workspace.Normalize(name)
		if err != nil || normalized != name || workspace.Excluded(name) != name || !within(key) || seenExcluded[key] {
			return false
		}
		seenExcluded[key] = true
	}
	return true
}

func validate(r Record) error {
	if r.SchemaVersion != SchemaVersion {
		if r.SchemaVersion > 0 {
			return ErrSchema
		}
		return ErrCorrupt
	}
	if !validID(r.RunID) || r.Tool.Name != "checkstamp" || r.Tool.Version == "" || r.Command != "run" || strings.TrimSpace(r.Label) == "" || r.StartedAt.IsZero() || r.StartedAt.Location() != time.UTC || !filepath.IsAbs(r.Conditions.WorkingDirectory) || !filepath.IsAbs(r.Conditions.Executable) || !validHash(r.Conditions.ArgumentsSHA256) || !reflect.DeepEqual(r.Coverage, coverage()) || !validateSnapshot(r.Before, false) || r.Changes == nil || r.Issues == nil {
		return ErrCorrupt
	}
	switch r.Phase {
	case "started":
		if r.FinishedAt != nil || r.ChildExitCode != nil || r.After != nil || r.Interrupted || len(r.Changes) != 0 {
			return ErrCorrupt
		}
	case "completed":
		if r.FinishedAt == nil || r.FinishedAt.IsZero() || r.FinishedAt.Location() != time.UTC || r.FinishedAt.Before(r.StartedAt) || r.ChildExitCode == nil || *r.ChildExitCode < 0 || uint64(*r.ChildExitCode) > 0xffffffff {
			return ErrCorrupt
		}
		if r.After != nil {
			if !validateSnapshot(*r.After, true) || !slices.Equal(r.Before.Inputs, r.After.Inputs) || !reflect.DeepEqual(r.Changes, input.Diff(r.Before, *r.After)) {
				return ErrCorrupt
			}
		} else if len(r.Changes) != 0 {
			return ErrCorrupt
		}
	default:
		return ErrCorrupt
	}
	status, issues := outcome(r)
	if r.Status != status || !reflect.DeepEqual(r.Issues, issues) {
		return ErrCorrupt
	}
	return nil
}
