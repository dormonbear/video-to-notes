//go:build integration

package llm

import (
	"context"
	"os"
	"testing"
)

func TestAnalyzeRealVideo(t *testing.T) {
	key := os.Getenv("OPENROUTER_API_KEY")
	video := os.Getenv("TEST_VIDEO")
	if key == "" || video == "" {
		t.Skip("set OPENROUTER_API_KEY and TEST_VIDEO (path to an .mp4) to run")
	}
	model := os.Getenv("MODEL")
	if model == "" {
		model = "google/gemini-2.5-flash"
	}
	proxy := os.Getenv("OPENROUTER_PROXY") // "" → use HTTP(S)_PROXY env

	c, err := New(key, model, proxy)
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Analyze(context.Background(), video)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if d.Summary == "" || d.Article == "" {
		t.Errorf("expected populated note data, got %+v", d)
	}
	t.Logf("title=%s summary=%s tags=%v article_len=%d",
		d.Title, d.Summary, d.Tags, len(d.Article))
}
