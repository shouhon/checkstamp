package record

import (
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/shouhon/checkstamp/internal/workspace"
)

// Summary は保存記録を選ぶための情報。現在の入力の有効性は表さない。
type Summary struct {
	RunID          string     `json:"run_id"`
	Label          *string    `json:"label"`
	StartedAt      *time.Time `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at"`
	RecordedStatus *string    `json:"recorded_status"`
	ChildExitCode  *int       `json:"child_exit_code"`
	Issues         []Issue    `json:"issues"`
}

// Listing は件数制限した履歴と、絞り込み・制限の外も含む読取不成立件数を表す。
type Listing struct {
	Runs             []Summary
	HasMore          bool
	UnavailableCount int
}

// List は入力を再収集せず履歴を読む。空のlabelは全件、limitは1以上とする。
// 日時不明の記録を先に、残りは開始日時の降順、同時刻は実行IDの昇順に返す。
func (s *Store) List(label string, limit int) (Listing, error) {
	result := Listing{Runs: []Summary{}}
	if limit < 1 || !s.atOriginalLocation() {
		return result, ErrRead
	}
	dir, err := workspace.OpenFile(s.root, ".", os.O_RDONLY, 0)
	if err != nil {
		return result, ErrRead
	}
	defer dir.Close()
	for {
		entries, readErr := dir.ReadDir(128)
		for _, entry := range entries {
			name := entry.Name()
			// 保存中・強制終了後の一時ファイルは、公開済みの記録ではない。
			if len(name) == 70 && name[0] == '.' && name[33] == '-' && strings.HasSuffix(name, ".tmp") && validID(name[1:33]) && validID(name[34:66]) {
				continue
			}
			id := strings.TrimSuffix(name, ".json")
			if !strings.HasSuffix(name, ".json") || !validID(id) {
				result.UnavailableCount++
				continue
			}
			saved, loadErr := s.Load(id)
			item := Summary{RunID: id, Issues: []Issue{}}
			if loadErr == nil || errors.Is(loadErr, ErrIncomplete) {
				label, started, status := saved.Label, saved.StartedAt, saved.Status
				item.Label, item.StartedAt, item.FinishedAt = &label, &started, saved.FinishedAt
				item.RecordedStatus, item.ChildExitCode, item.Issues = &status, saved.ChildExitCode, saved.Issues
			} else {
				issue := Issue{"record_read_failed", "記録を読み取れませんでした。"}
				switch {
				case errors.Is(loadErr, ErrCorrupt):
					issue = Issue{"record_corrupt", "記録が破損しているか、必須情報が一致しません。"}
				case errors.Is(loadErr, ErrSchema):
					issue = Issue{"unsupported_schema", "記録の形式の版に対応していません。"}
				}
				item.Issues = append(item.Issues, issue)
			}
			if loadErr != nil {
				result.UnavailableCount++
			}
			// ラベルを読めなかった記録は、指定ラベルと無関係だと推定しない。
			if label != "" && item.Label != nil && *item.Label != label {
				continue
			}
			index, _ := slices.BinarySearchFunc(result.Runs, item, compareSummary)
			result.Runs = slices.Insert(result.Runs, index, item)
			if len(result.Runs) > limit {
				result.Runs = result.Runs[:limit]
				result.HasMore = true
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return Listing{}, ErrRead
		}
	}
	if err := dir.Close(); err != nil || !s.atOriginalLocation() {
		return Listing{}, ErrRead
	}
	return result, nil
}

func compareSummary(a, b Summary) int {
	if a.StartedAt == nil && b.StartedAt != nil {
		return -1
	}
	if a.StartedAt != nil && b.StartedAt == nil {
		return 1
	}
	if a.StartedAt != nil && b.StartedAt != nil {
		if order := b.StartedAt.Compare(*a.StartedAt); order != 0 {
			return order
		}
	}
	return strings.Compare(a.RunID, b.RunID)
}
