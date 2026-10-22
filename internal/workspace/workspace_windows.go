// Package workspace は、作業ディレクトリ内の通常ファイルとディレクトリを扱う。
package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

var (
	ErrPath        = errors.New("作業ディレクトリ内の相対パスを指定してください。")
	ErrUnsupported = errors.New("リンク、再解析ポイント、特殊ファイルは扱えません。")
	ErrRead        = errors.New("対象の種類または内容を読み取れませんでした。")
)

// Normalize はWindowsの曖昧な別名や代替データストリームを拒否し、区切りを / にそろえる。
func Normalize(name string) (string, error) {
	if !filepath.IsLocal(name) || strings.ContainsAny(name, ":\x00*?\"<>|") {
		return "", ErrPath
	}
	for _, part := range strings.Split(strings.ReplaceAll(name, `\`, "/"), "/") {
		if part != "." && part != ".." && (strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ")) {
			return "", ErrPath
		}
	}
	return filepath.ToSlash(filepath.Clean(name)), nil
}

// Excluded は入力から除外する管理領域の先頭パスを返す。
func Excluded(name string) string {
	parts := strings.Split(name, "/")
	for i, part := range parts {
		if strings.EqualFold(part, ".git") || strings.EqualFold(part, ".checkstamp") {
			return strings.Join(parts[:i+1], "/")
		}
	}
	return ""
}

// Check はリンクとすべてのWindows再解析ポイント、特殊ファイルを拒否する。
func Check(info os.FileInfo) error {
	attrs, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return ErrRead
	}
	if attrs.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
		return ErrUnsupported
	}
	return nil
}

// OpenFile は単一の名前を参照先へ移動せず開き、通常ファイルまたはディレクトリであることを確認する。
func OpenFile(root *os.Root, name string, flags int, perm os.FileMode) (*os.File, error) {
	f, err := root.OpenFile(name, flags|syscall.FILE_FLAG_OPEN_REPARSE_POINT, perm)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil {
		err = Check(info)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// OpenDir は単一のディレクトリ名を検査して開く。途中で対象が入れ替わった場合も拒否する。
func OpenDir(parent *os.Root, name string) (*os.Root, error) {
	f, err := OpenFile(parent, name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.IsDir() {
		return nil, ErrRead
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	after, err := root.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		root.Close()
		return nil, ErrRead
	}
	return root, nil
}
