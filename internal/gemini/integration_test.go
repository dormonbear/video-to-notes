//go:build integration

package gemini

import (
	"context"
	"os"
	"testing"
)

func TestAnalyzeRealVideo(t *testing.T) {
	key := os.Getenv("GEMINI_API_KEY")
	video := os.Getenv("TEST_VIDEO")
	if key == "" || video == "" {
		t.Skip("set GEMINI_API_KEY and TEST_VIDEO to run")
	}
	c, err := New(context.Background(), key, "gemini-2.5-flash")
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
}
