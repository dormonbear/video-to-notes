package llm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildContentParts(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "a.png")
	// PNG magic bytes are enough for DetectContentType to report image/png.
	os.WriteFile(img, []byte("\x89PNG\r\n\x1a\n................"), 0o644)

	parts, err := buildContentParts(Content{
		Prompt:     "写文章",
		Text:       "正文素材",
		MediaKind:  "image",
		MediaPaths: []string{img},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 {
		t.Fatalf("want 2 parts (text+image), got %d", len(parts))
	}
	first := parts[0].(map[string]any)
	if first["type"] != "text" || !strings.Contains(first["text"].(string), "正文素材") {
		t.Errorf("text part wrong: %+v", first)
	}
	second := parts[1].(map[string]any)
	if second["type"] != "image_url" {
		t.Errorf("media part type = %v, want image_url", second["type"])
	}
}

func TestBuildContentPartsTextOnly(t *testing.T) {
	parts, err := buildContentParts(Content{Prompt: "p", Text: "t", MediaKind: ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 {
		t.Fatalf("text-only should yield 1 part, got %d", len(parts))
	}
}
