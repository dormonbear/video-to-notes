package note

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExists(t *testing.T) {
	vault := t.TempDir()
	sub := "src/content/posts"
	dir := filepath.Join(vault, sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 已发布的视频文章（注意日期与查询无关，按 id glob）
	if err := os.WriteFile(filepath.Join(dir, "2026-06-14-douyin-12345.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !Exists(vault, sub, "12345") {
		t.Error("existing video id should match regardless of date")
	}
	if Exists(vault, sub, "99999") {
		t.Error("unknown video id should not match")
	}
	if Exists(vault, sub, "") {
		t.Error("empty video id must be false")
	}
}
