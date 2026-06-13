//go:build integration

package douyin

import (
	"os"
	"testing"
)

func TestDownloadRealLink(t *testing.T) {
	dir := t.TempDir()
	path, meta, err := Download(
		"https://v.douyin.com/EklG9cO2IMQ/", dir,
	)
	if err != nil {
		t.Fatalf("Download error: %v", err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		t.Fatalf("expected non-empty file at %s, err=%v", path, err)
	}
	if meta.Title == "" || meta.SourceURL == "" {
		t.Errorf("expected non-empty meta, got %+v", meta)
	}
}
