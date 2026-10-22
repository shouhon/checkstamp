package record

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/shouhon/checkstamp/internal/input"
	"github.com/shouhon/checkstamp/internal/version"
	"github.com/shouhon/checkstamp/internal/workspace"
)

const ownerName = "owner"
const ownerContent = "checkstamp storage\nschema_version: 1\n"
const maxRecordBytes = 64 << 20

// Store は所有確認済みの保存先。Openを除いてディレクトリの作成はしない。
type Store struct {
	root      *os.Root
	directory string
	writable  bool
}

// Run は新規実行の保存権限を持つ。1件につき完了保存は一度だけ行う。
type Run struct {
	store     *Store
	record    Record
	mu        sync.Mutex
	completed bool
}

// Open は専用の保存先を用意する。既存の未所有ディレクトリは空でも利用しない。
func Open(directory string) (*Store, error) {
	return open(directory, true)
}

// OpenReadOnly は既存の専用保存先を開く。ファイルやディレクトリを作らない。
func OpenReadOnly(directory string) (*Store, error) {
	return open(directory, false)
}

func open(directory string, create bool) (*Store, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, ErrRead
	}
	working, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, ErrRead
	}
	defer working.Close()
	created := false
	if create {
		err := working.Mkdir(".checkstamp", 0700)
		if err == nil {
			created = true
		} else if !errors.Is(err, os.ErrExist) {
			return nil, ErrWrite
		}
	}
	storage, err := workspace.OpenDir(working, ".checkstamp")
	if err != nil {
		return nil, ErrOwnership
	}
	defer storage.Close()
	if created {
		f, err := workspace.OpenFile(storage, ownerName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, ErrWrite
		}
		_, writeErr := io.WriteString(f, ownerContent)
		syncErr := f.Sync()
		closeErr := f.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			return nil, ErrWrite
		}
	}
	owner, err := workspace.OpenFile(storage, ownerName, os.O_RDONLY, 0)
	if err != nil {
		return nil, ErrOwnership
	}
	data, readErr := io.ReadAll(io.LimitReader(owner, int64(len(ownerContent)+1)))
	closeErr := owner.Close()
	if readErr != nil || closeErr != nil || string(data) != ownerContent {
		return nil, ErrOwnership
	}
	if create {
		if err := storage.Mkdir("runs", 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, ErrWrite
		}
	}
	runs, err := workspace.OpenDir(storage, "runs")
	if err != nil {
		return nil, ErrOwnership
	}
	return &Store{root: runs, directory: absolute, writable: create}, nil
}

// Close は保存先のハンドルを閉じる。以降の読取・保存は失敗する。
func (s *Store) Close() error {
	return s.root.Close()
}

func randomID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", ErrWrite
	}
	return hex.EncodeToString(data[:]), nil
}

// Start は衝突しない実行IDで開始記録を保存する。成功してから子プロセスを起動する。
func (s *Store) Start(label, executable string, args []string, before input.Snapshot) (*Run, error) {
	if !s.writable || !s.atOriginalLocation() {
		return nil, ErrWrite
	}
	id, err := randomID()
	if err != nil {
		return nil, err
	}
	// 呼出し元のスライス変更で、保存済みの開始条件が変わらないよう複製する。
	before.Inputs = slices.Clone(before.Inputs)
	before.Entries = slices.Clone(before.Entries)
	before.Excluded = slices.Clone(before.Excluded)
	r := Record{
		SchemaVersion: SchemaVersion, Tool: Tool{"checkstamp", version.String()}, Command: "run",
		RunID: id, Label: label, Phase: "started", StartedAt: time.Now().UTC(),
		Conditions: Conditions{s.directory, executable, HashArguments(args)}, Coverage: CoverageLimits(),
		Before: before, Changes: []input.Change{},
	}
	r.Status, r.Issues = outcome(r)
	if err := validate(r); err != nil {
		return nil, err
	}
	// O_EXCLでこの実行だけが使う名前を確保する。強制終了時の空ファイルも成功にはならない。
	f, err := workspace.OpenFile(s.root, id+".json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, ErrWrite
	}
	if err := f.Close(); err != nil {
		return nil, ErrWrite
	}
	if err := s.write(r); err != nil {
		return nil, err
	}
	return &Run{store: s, record: r}, nil
}

// ID は保存済みの実行IDを返す。
func (r *Run) ID() string {
	return r.record.RunID
}

// Complete は終了コードと実行後の一覧を保存する。afterがnilなら読取不成立として記録する。
func (r *Run) Complete(exitCode int, after *input.Snapshot, interrupted bool) (Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.completed {
		return Record{}, ErrWrite
	}
	next := r.record
	next.Phase = "completed"
	now := time.Now().UTC()
	next.FinishedAt, next.ChildExitCode = &now, &exitCode
	next.After, next.Interrupted = after, interrupted
	if after != nil {
		next.Changes = input.Diff(next.Before, *after)
	}
	next.Status, next.Issues = outcome(next)
	if err := validate(next); err != nil {
		return Record{}, err
	}
	if err := r.store.write(next); err != nil {
		next.Status = "unknown"
		next.Issues = append(next.Issues, Issue{"record_write_failed", "完了記録を保存できませんでした。"})
		return next, err
	}
	r.completed = true
	return next, nil
}

func (s *Store) write(r Record) error {
	if !s.atOriginalLocation() {
		return ErrWrite
	}
	data, err := json.Marshal(r)
	if err != nil || len(data) >= maxRecordBytes {
		return ErrWrite
	}
	suffix, err := randomID()
	if err != nil {
		return err
	}
	name := "." + r.RunID + "-" + suffix + ".tmp"
	f, err := workspace.OpenFile(s.root, name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrWrite
	}
	defer s.root.Remove(name)
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return ErrWrite
	}
	// 同じディレクトリ内で名前を置換し、読取側へ書きかけのJSONを公開しない。
	if err := s.root.Rename(name, r.RunID+".json"); err != nil {
		return ErrWrite
	}
	if !s.atOriginalLocation() {
		return ErrWrite
	}
	return nil
}

func (s *Store) atOriginalLocation() bool {
	current, err := OpenReadOnly(s.directory)
	if err != nil {
		return false
	}
	defer current.Close()
	held, err := s.root.Stat(".")
	if err != nil {
		return false
	}
	named, err := current.root.Stat(".")
	return err == nil && os.SameFile(held, named)
}

// Load は形式・必須情報・判定の整合性を検査する。未完了時だけ記録とErrIncompleteを返す。
func (s *Store) Load(id string) (Record, error) {
	if !validID(id) {
		return Record{}, ErrID
	}
	if !s.atOriginalLocation() {
		return Record{}, ErrRead
	}
	f, err := workspace.OpenFile(s.root, id+".json", os.O_RDONLY, 0)
	if err != nil {
		return Record{}, ErrRead
	}
	data, readErr := io.ReadAll(io.LimitReader(f, maxRecordBytes))
	closeErr := f.Close()
	if readErr != nil || closeErr != nil {
		return Record{}, ErrRead
	}
	if len(data) >= maxRecordBytes {
		return Record{}, ErrCorrupt
	}
	if !json.Valid(data) || !uniqueJSON(data) {
		return Record{}, ErrCorrupt
	}
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return Record{}, ErrCorrupt
	}
	if header.SchemaVersion > 0 && header.SchemaVersion != SchemaVersion {
		return Record{}, ErrSchema
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Record{}, ErrCorrupt
	}
	shape := reflect.TypeOf(Record{})
	if len(fields) != shape.NumField() {
		return Record{}, ErrCorrupt
	}
	for i := 0; i < shape.NumField(); i++ {
		key := shape.Field(i).Tag.Get("json")
		value, ok := fields[key]
		if !ok || string(value) == "null" && key != "after" && key != "finished_at" && key != "child_exit_code" {
			return Record{}, ErrCorrupt
		}
	}
	var r Record
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&r); err != nil {
		return Record{}, ErrCorrupt
	}
	if err := validate(r); err != nil {
		return Record{}, err
	}
	if r.RunID != id || !stringsEqualDirectory(r.Conditions.WorkingDirectory, s.directory) {
		return Record{}, ErrCorrupt
	}
	if !s.atOriginalLocation() {
		return Record{}, ErrRead
	}
	if r.Phase != "completed" {
		return r, ErrIncomplete
	}
	return r, nil
}

func stringsEqualDirectory(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// 同じキーを複数持つJSONは、読み手によって意味が変わるため受け付けない。
func uniqueJSON(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var value func() bool
	value = func() bool {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			keys := make(map[string]bool)
			for decoder.More() {
				token, err := decoder.Token()
				key, ok := token.(string)
				if err != nil || !ok || keys[key] {
					return false
				}
				keys[key] = true
				if !value() {
					return false
				}
			}
		case '[':
			for decoder.More() {
				if !value() {
					return false
				}
			}
		default:
			return false
		}
		_, err = decoder.Token()
		return err == nil
	}
	if !value() {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}
