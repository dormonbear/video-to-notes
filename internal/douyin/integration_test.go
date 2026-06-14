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
	if !strings.HasSuffix(path, ".mp3") {
		t.Errorf("expected an .mp3 audio path, got %s", path)
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		t.Fatalf("expected non-empty audio at %s, err=%v", path, err)
	}
	if meta.Title == "" || meta.SourceURL == "" {
		t.Errorf("expected non-empty meta, got %+v", meta)
	}
}
