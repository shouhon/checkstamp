package input_test

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"unicode/utf16"

	"github.com/shouhon/checkstamp/internal/input"
)

func write(t *testing.T, directory, name, content string) {
	t.Helper()
	file := filepath.Join(directory, name)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCollectIncludesHiddenFilesAndEmptyDirectories(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "日本語 空白/a.txt", "検証用の本文")
	write(t, dir, ".隠しファイル", "隠しファイル")
	hidden, err := syscall.UTF16PtrFromString(filepath.Join(dir, ".隠しファイル"))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.SetFileAttributes(hidden, syscall.FILE_ATTRIBUTE_HIDDEN); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "空"), 0700); err != nil {
		t.Fatal(err)
	}
	write(t, dir, ".GIT/秘密", "除外")
	write(t, dir, "日本語 空白/.checkstamp/秘密", "除外")
	snapshot, err := input.Collect(dir, []string{".", "日本語 空白/./a.txt", "."})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, entry := range snapshot.Entries {
		paths = append(paths, entry.Path)
	}
	want := []string{".", ".隠しファイル", "日本語 空白", "日本語 空白/a.txt", "空"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("入力一覧 = %v、期待値 = %v", paths, want)
	}
	if !reflect.DeepEqual(snapshot.Excluded, []string{".GIT", "日本語 空白/.checkstamp"}) {
		t.Fatalf("除外範囲 = %v", snapshot.Excluded)
	}
	sum := sha256.Sum256([]byte("検証用の本文"))
	if snapshot.Entries[3].SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("内容のSHA-256が一致しません")
	}
	if snapshot.Entries[4].Kind != "directory" || snapshot.Entries[4].SHA256 != "" {
		t.Fatal("空ディレクトリの種類が保持されていません")
	}
}

func TestRejectInputsBeforeCollection(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name   string
		inputs []string
		want   error
	}{
		{"指定なし", nil, input.ErrArguments},
		{"空文字", []string{""}, input.ErrArguments},
		{"範囲外", []string{"../秘密"}, input.ErrArguments},
		{"絶対パス", []string{dir}, input.ErrArguments},
		{"ドライブ相対", []string{"C:秘密"}, input.ErrArguments},
		{"代替データストリーム", []string{"入力:秘密"}, input.ErrArguments},
		{"予約名", []string{"NUL"}, input.ErrArguments},
		{"曖昧な末尾", []string{"src. /file"}, input.ErrArguments},
		{"除外領域", []string{"src/.GiT/config"}, input.ErrExcluded},
		{"存在しない入力", []string{"存在しない検証用の非公開入力"}, input.ErrMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := input.Collect(dir, tc.inputs)
			if !errors.Is(err, tc.want) {
				t.Fatalf("失敗の種類 = %v、期待値 = %v", err, tc.want)
			}
			if got.Entries != nil {
				t.Fatal("失敗時に部分的な入力一覧が返されています")
			}
			if strings.Contains(err.Error(), "非公開入力") {
				t.Fatal("入力の生値が診断へ混入しています")
			}
		})
	}
}

func TestDifferencesIncludeDeletionAndTypeReplacement(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "src/変更", "前")
	write(t, dir, "src/削除", "削除")
	write(t, dir, "src/入替", "通常ファイル")
	if err := os.Mkdir(filepath.Join(dir, "src/空の削除"), 0700); err != nil {
		t.Fatal(err)
	}
	before, err := input.Collect(dir, []string{"src"})
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "src/変更", "後")
	if err := os.Remove(filepath.Join(dir, "src/削除")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "src/入替")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "src/空の削除")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "src/入替"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "src/空の追加"), 0700); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "src/追加", "追加")
	after, err := input.Recollect(dir, before.Inputs)
	if err != nil {
		t.Fatal(err)
	}
	got := input.Diff(before, after)
	var summary []string
	for _, change := range got {
		summary = append(summary, change.Path+":"+change.Kind)
	}
	want := []string{"src/入替:modified", "src/削除:removed", "src/変更:modified", "src/空の削除:removed", "src/空の追加:added", "src/追加:added"}
	if !reflect.DeepEqual(summary, want) {
		t.Fatalf("差分 = %v、期待値 = %v", summary, want)
	}
	if got[0].Before.Kind != "file" || got[0].After.Kind != "directory" {
		t.Fatal("種類の入替が保持されていません")
	}
}

func TestMissingRootAndReplacedAncestorAreChanges(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "parent/file", "内容")
	before, err := input.Collect(dir, []string{"parent/file"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "parent/file")); err != nil {
		t.Fatal(err)
	}
	after, err := input.Recollect(dir, before.Inputs)
	if err != nil || len(input.Diff(before, after)) != 1 {
		t.Fatal("明示した入力の削除を検出できません")
	}
	if err := os.Remove(filepath.Join(dir, "parent")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "parent", "祖先を通常ファイルに入替")
	if _, err := input.Recollect(dir, before.Inputs); err != nil {
		t.Fatal("祖先の種類変更を読取失敗として扱っています")
	}
}

func TestInputReadFailureIsNotDeletion(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "秘密の入力", "内容")
	before, err := input.Collect(dir, []string{"秘密の入力"})
	if err != nil {
		t.Fatal(err)
	}
	name, err := syscall.UTF16PtrFromString(filepath.Join(dir, "秘密の入力"))
	if err != nil {
		t.Fatal(err)
	}
	lock, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(lock)
	if _, err := input.Recollect(dir, before.Inputs); !errors.Is(err, input.ErrRead) {
		t.Fatalf("読取失敗 = %v", err)
	}
}

func TestSymbolicLinksAreNotFollowed(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	write(t, outside, "秘密", "外部の内容")
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Skipf("シンボリックリンクを作成できません: %v", err)
	}
	for _, name := range []string{".", "link", "link/秘密"} {
		if _, err := input.Collect(dir, []string{name}); !errors.Is(err, input.ErrUnsupported) {
			t.Fatalf("リンク入力を拒否していません: %v", err)
		}
	}
}

func TestJunctionIsNotFollowed(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	write(t, outside, "秘密", "外部の内容")
	link := filepath.Join(dir, "junction")
	if err := os.Mkdir(link, 0700); err != nil {
		t.Fatal(err)
	}
	linkName, err := syscall.UTF16PtrFromString(link)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(linkName, syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	// REPARSE_DATA_BUFFERのマウントポイント形式。検証用ディレクトリにだけ設定する。
	substitute := utf16.Encode([]rune(`\??\` + outside))
	printName := utf16.Encode([]rune(outside))
	buffer := make([]byte, 16+2*(len(substitute)+1+len(printName)+1))
	binary.LittleEndian.PutUint32(buffer[0:], 0xa0000003)
	binary.LittleEndian.PutUint16(buffer[4:], uint16(len(buffer)-8))
	binary.LittleEndian.PutUint16(buffer[10:], uint16(2*len(substitute)))
	binary.LittleEndian.PutUint16(buffer[12:], uint16(2*(len(substitute)+1)))
	binary.LittleEndian.PutUint16(buffer[14:], uint16(2*len(printName)))
	for i, c := range substitute {
		binary.LittleEndian.PutUint16(buffer[16+2*i:], c)
	}
	for i, c := range printName {
		binary.LittleEndian.PutUint16(buffer[16+2*(len(substitute)+1+i):], c)
	}
	var returned uint32
	err = syscall.DeviceIoControl(handle, 0x000900a4, &buffer[0], uint32(len(buffer)), nil, 0, &returned, nil)
	syscall.CloseHandle(handle)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(link) })
	for _, name := range []string{".", "junction", "junction/秘密"} {
		if _, err := input.Collect(dir, []string{name}); !errors.Is(err, input.ErrUnsupported) {
			t.Fatalf("ジャンクションを拒否していません: %v", err)
		}
	}
}

func TestCaseOnlyRenameIsDetected(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "before.txt", "同じ内容")
	before, err := input.Collect(dir, []string{"."})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "before.txt"), filepath.Join(dir, "BEFORE.txt")); err != nil {
		t.Fatal(err)
	}
	after, err := input.Recollect(dir, before.Inputs)
	if err != nil {
		t.Fatal(err)
	}
	if changes := input.Diff(before, after); len(changes) != 1 || changes[0].Kind != "modified" {
		t.Fatalf("名前の変更を検出できません: %v", changes)
	}
}
