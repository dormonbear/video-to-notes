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
		t.Skip("set OPENROUTER_API_KEY and TEST_VIDEO to run")
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
	if d.Summary == "" || d.Transcript == "" || len(d.KeyPoints) == 0 {
		t.Errorf("expected populated note data, got %+v", d)
	}
	t.Logf("summary=%s tags=%v key_points=%d transcript_len=%d",
		d.Summary, d.Tags, len(d.KeyPoints), len(d.Transcript))
}
