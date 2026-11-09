package record_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shouhon/checkstamp/internal/record"
)

func TestListTemporaryFilesAndStoreIdentity(t *testing.T) {
	dir, store, before := setup(t)
	run := start(t, dir, store, before)
	if _, err := run.Complete(0, &before, false); err != nil {
		t.Fatal(err)
	}
	// 正規の保存用一時ファイルだけを無視し、名前の異常を空の履歴にしない。
	temporary := "." + run.ID() + "-" + strings.Repeat("a", 32) + ".tmp"
	if err := os.WriteFile(filepath.Join(dir, ".checkstamp/runs", temporary), []byte("書込途中"), 0600); err != nil {
		t.Fatal(err)
	}
	listing, err := store.List("", 1)
	if err != nil || len(listing.Runs) != 1 || listing.Runs[0].RunID != run.ID() || listing.UnavailableCount != 0 || listing.HasMore {
		t.Fatalf("保存用一時ファイルを履歴に含めています: %+v %v", listing, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".checkstamp/runs", "不明な名前.tmp"), []byte("検証用"), 0600); err != nil {
		t.Fatal(err)
	}
	listing, err = store.List("", 1)
	if err != nil || listing.UnavailableCount != 1 || len(listing.Runs) != 1 {
		t.Fatal("名前の異常を読取不成立として数えていません")
	}
	if _, err := store.List("", 0); !errors.Is(err, record.ErrRead) {
		t.Fatal("無効な制限を成功にしています")
	}
	runs := filepath.Join(dir, ".checkstamp/runs")
	if err := os.Rename(runs, runs+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(runs, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.List("", 1); !errors.Is(err, record.ErrRead) {
		t.Fatal("移動した保存先を元の保存先として読み取っています")
	}
	if _, err := record.OpenReadOnly(t.TempDir()); !errors.Is(err, record.ErrNoStorage) {
		t.Fatal("保存先未作成を別用途の保存先と区別していません")
	}
}
