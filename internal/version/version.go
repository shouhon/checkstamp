// Package version は、実行ファイルに埋め込む版番号を提供する。
package version

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var value string

// String は、VERSION を正本とするCLIの版番号を返す。
func String() string {
	return strings.TrimSpace(value)
}
