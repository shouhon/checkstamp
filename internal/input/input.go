// Package input は、明示された入力の一覧とSHA-256、一覧間の差分を作る。
package input

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/shouhon/checkstamp/internal/workspace"
)

var (
	ErrArguments   = errors.New("入力を1つ以上、作業ディレクトリ内の相対パスで指定してください。")
	ErrExcluded    = errors.New("管理領域を入力として直接指定することはできません。")
	ErrMissing     = errors.New("指定した入力が存在しません。")
	ErrRead        = errors.New("入力の種類または内容を読み取れませんでした。")
	ErrUnsupported = workspace.ErrUnsupported
)

// Entry は通常ファイルまたはディレクトリ1件の状態。ディレクトリのハッシュは空とする。
type Entry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256,omitempty"`
}

// Snapshot は指定範囲、収集した入力、実際に除外した管理領域を保持する。
type Snapshot struct {
	Inputs   []string `json:"inputs"`
	Entries  []Entry  `json:"entries"`
	Excluded []string `json:"excluded"`
}

// Change は追加・削除・内容または種類の変更を表す。
type Change struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Before *Entry `json:"before,omitempty"`
	After  *Entry `json:"after,omitempty"`
}

// Collect は実行前の入力を収集する。指定した入力が欠けていれば一覧を返さず失敗する。
func Collect(directory string, inputs []string) (Snapshot, error) {
	return collect(directory, inputs, false)
}

// Recollect は同じ範囲を再収集する。削除された入力は一覧から除き、読取失敗とは区別する。
func Recollect(directory string, inputs []string) (Snapshot, error) {
	return collect(directory, inputs, true)
}

func collect(directory string, inputs []string, allowMissing bool) (Snapshot, error) {
	names, err := normalizeInputs(inputs)
	if err != nil {
		return Snapshot{}, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return Snapshot{}, ErrRead
	}
	defer root.Close()
	result := Snapshot{Inputs: names, Entries: []Entry{}, Excluded: []string{}}
	entries := make(map[string]Entry)
	excluded := make(map[string]string)
	for _, name := range names {
		if err := collectPath(root, name, "", entries, excluded); err != nil {
			if allowMissing && errors.Is(err, os.ErrNotExist) {
				continue
			}
			if errors.Is(err, os.ErrNotExist) {
				return Snapshot{}, ErrMissing
			}
			if errors.Is(err, ErrUnsupported) {
				return Snapshot{}, ErrUnsupported
			}
			return Snapshot{}, ErrRead
		}
	}
	for _, entry := range entries {
		result.Entries = append(result.Entries, entry)
	}
	for _, name := range excluded {
		result.Excluded = append(result.Excluded, name)
	}
	slices.SortFunc(result.Entries, func(a, b Entry) int { return strings.Compare(a.Path, b.Path) })
	slices.Sort(result.Excluded)
	return result, nil
}

func normalizeInputs(inputs []string) ([]string, error) {
	if len(inputs) == 0 {
		return nil, ErrArguments
	}
	seen := make(map[string]string)
	names := make([]string, 0, len(inputs))
	for _, name := range inputs {
		name, err := workspace.Normalize(name)
		if err != nil {
			return nil, ErrArguments
		}
		if workspace.Excluded(name) != "" {
			return nil, ErrExcluded
		}
		key := strings.ToLower(name)
		if previous, exists := seen[key]; exists && previous != name {
			return nil, ErrUnsupported
		} else if !exists {
			seen[key] = name
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names, nil
}

func collectPath(root *os.Root, name, prefix string, entries map[string]Entry, excluded map[string]string) error {
	// 中間の構成要素も1件ずつ検査し、リンク配下を直接指定しても拒否する。
	first, rest, nested := strings.Cut(name, "/")
	full := path.Join(prefix, first)
	if skip := workspace.Excluded(full); skip != "" {
		excluded[strings.ToLower(skip)] = skip
		return nil
	}
	info, err := root.Lstat(first)
	if err != nil {
		return err
	}
	if err := workspace.Check(info); err != nil {
		return err
	}
	if nested {
		if !info.IsDir() {
			// 祖先が通常ファイルへ置き換わった場合、その配下の入力は削除として扱う。
			return os.ErrNotExist
		}
		dir, err := workspace.OpenDir(root, first)
		if err != nil {
			return err
		}
		defer dir.Close()
		return collectPath(dir, rest, full, entries, excluded)
	}
	key := strings.ToLower(full)
	if _, ok := entries[key]; ok {
		return nil
	}
	if info.IsDir() {
		dir, err := workspace.OpenDir(root, first)
		if err != nil {
			return err
		}
		defer dir.Close()
		f, err := workspace.OpenFile(dir, ".", os.O_RDONLY, 0)
		if err != nil {
			return err
		}
		children, readErr := f.ReadDir(-1)
		closeErr := f.Close()
		if readErr != nil || closeErr != nil {
			return ErrRead
		}
		entries[key] = Entry{Path: full, Kind: "directory"}
		childNames := make(map[string]bool, len(children))
		for _, child := range children {
			childKey := strings.ToLower(child.Name())
			if childNames[childKey] {
				// 大文字小文字だけが異なる複数の名前を持つディレクトリは初版では扱わない。
				return ErrUnsupported
			}
			childNames[childKey] = true
			if err := collectPath(dir, child.Name(), full, entries, excluded); err != nil {
				// 列挙済みの項目が収集中に消えた場合も、部分的な一覧は返さない。
				if errors.Is(err, os.ErrNotExist) {
					return ErrRead
				}
				return err
			}
		}
		return nil
	}
	f, err := workspace.OpenFile(root, first, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	opened, statErr := f.Stat()
	if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		f.Close()
		return ErrRead
	}
	hash := sha256.New()
	_, readErr := io.Copy(hash, f)
	after, statErr := f.Stat()
	closeErr := f.Close()
	if readErr != nil || statErr != nil || closeErr != nil || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return ErrRead
	}
	entries[key] = Entry{Path: full, Kind: "file", SHA256: hex.EncodeToString(hash.Sum(nil))}
	return nil
}

// Diff は通常ファイルの内容、種類、空ディレクトリを含む追加・削除をパス順で返す。
func Diff(before, after Snapshot) []Change {
	old := make(map[string]Entry, len(before.Entries))
	for _, entry := range before.Entries {
		old[strings.ToLower(entry.Path)] = entry
	}
	changes := []Change{}
	for _, entry := range after.Entries {
		key := strings.ToLower(entry.Path)
		previous, ok := old[key]
		if !ok {
			changes = append(changes, Change{Path: entry.Path, Kind: "added", After: &entry})
		} else if previous.Path != entry.Path || previous.Kind != entry.Kind || previous.SHA256 != entry.SHA256 {
			changes = append(changes, Change{Path: entry.Path, Kind: "modified", Before: &previous, After: &entry})
		}
		delete(old, key)
	}
	for _, entry := range old {
		changes = append(changes, Change{Path: entry.Path, Kind: "removed", Before: &entry})
	}
	slices.SortFunc(changes, func(a, b Change) int { return strings.Compare(a.Path, b.Path) })
	return changes
}
