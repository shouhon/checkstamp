package input

import (
	"errors"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrExcludePattern = errors.New("除外パターンを作業ディレクトリ基準の / 区切りで指定してください。")
	ErrInputExcluded  = errors.New("明示した入力を除外することはできません。")
)

// Exclusions は作業ディレクトリ基準で、大文字小文字を区別しない除外条件を保持する。
type Exclusions struct {
	patterns []string
	parts    [][]string
}

// NewExclusions は各構成要素の *・?・文字クラスと、階層をまたぐ ** を検査する。
func NewExclusions(patterns []string) (Exclusions, error) {
	result := Exclusions{patterns: []string{}}
	for _, pattern := range patterns {
		if pattern == "" || !utf8.ValidString(pattern) || strings.ContainsAny(pattern, "\\:") || strings.ContainsFunc(pattern, unicode.IsControl) {
			return Exclusions{}, ErrExcludePattern
		}
		for _, part := range strings.Split(pattern, "/") {
			if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.Contains(part, "**") && part != "**" {
				return Exclusions{}, ErrExcludePattern
			}
			if _, err := path.Match(part, ""); err != nil {
				return Exclusions{}, ErrExcludePattern
			}
		}
		result.patterns = append(result.patterns, strings.ToLower(pattern))
	}
	slices.Sort(result.patterns)
	result.patterns = slices.Compact(result.patterns)
	for _, pattern := range result.patterns {
		result.parts = append(result.parts, strings.Split(pattern, "/"))
	}
	return result, nil
}

// Patterns は保存と再照合に使う、並べ替え・重複除去済みの条件を返す。
func (e Exclusions) Patterns() []string {
	return slices.Clone(e.patterns)
}

// Match はパス自身または祖先が除外条件に一致するかを返す。除外した木は探索しない。
func (e Exclusions) Match(name string) bool {
	if len(e.parts) == 0 {
		return false
	}
	var names []string
	if name != "." {
		names = strings.Split(strings.ToLower(name), "/")
	}
	for _, parts := range e.parts {
		// ** を含む場合も、構成要素数の積に比例する範囲で照合する。
		reachable := make([]bool, len(names)+1)
		reachable[0] = true
		for _, part := range parts {
			if part == "**" {
				for i := 1; i < len(reachable); i++ {
					reachable[i] = reachable[i] || reachable[i-1]
				}
			} else {
				for i := len(names); i > 0; i-- {
					matched, _ := path.Match(part, names[i-1])
					reachable[i] = reachable[i-1] && matched
				}
				reachable[0] = false
			}
		}
		if slices.Contains(reachable, true) {
			return true
		}
	}
	return false
}
