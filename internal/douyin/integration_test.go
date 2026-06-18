//go:build integration

package douyin

import (
	"os"
	"strings"
	"testing"
)

func TestFetchRealLink(t *testing.T) {
	dir := t.TempDir()
	path, meta, err := Fetch(
		"https://v.douyin.com/EklG9cO2IMQ/", dir,
	)
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	if !strings.HasSuffix(path, ".mp4") {
		t.Errorf("expected an .mp4 video path, got %s", path)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("expected non-empty video at %s, err=%v", path, err)
	}
	if fi.Size() > maxInlineBytes {
		t.Errorf("transcoded video exceeds inline limit: %d bytes", fi.Size())
	}
	if meta.Title == "" || meta.SourceURL == "" {
		t.Errorf("expected non-empty meta, got %+v", meta)
	}
}
