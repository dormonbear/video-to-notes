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
	// 已发布的视频文章（注意日期与查询无关，按 source+id glob）
	if err := os.WriteFile(filepath.Join(dir, "2026-06-14-douyin-12345.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !Exists(vault, sub, "douyin", "12345") {
		t.Error("existing video id should match regardless of date")
	}
	if Exists(vault, sub, "douyin", "99999") {
		t.Error("unknown video id should not match")
	}
	if Exists(vault, sub, "douyin", "") {
		t.Error("empty video id must be false")
	}
	if Exists(vault, sub, "twitter", "12345") {
		t.Error("same id under a different source must not match")
	}
}

func TestExistsBySource(t *testing.T) {
	vault := t.TempDir()
	sub := "posts"
	if err := os.MkdirAll(filepath.Join(vault, sub), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(vault, sub, "2026-06-20-web-abc123.md"), []byte("x"), 0o644)
	if !Exists(vault, sub, "web", "abc123") {
		t.Error("web/abc123 should exist")
	}
	if Exists(vault, sub, "twitter", "abc123") {
		t.Error("twitter/abc123 should NOT match a web file")
	}
}
