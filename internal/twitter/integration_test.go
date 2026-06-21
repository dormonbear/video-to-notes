//go:build integration

package twitter

import (
	"os"
	"testing"
)

// Run: go test -tags integration ./internal/twitter/ -run TestFetchLive -v
// Set TWITTER_PROXY to the over-the-wall channel (e.g. http://127.0.0.1:7897).
func TestFetchLive(t *testing.T) {
	proxy := os.Getenv("TWITTER_PROXY")
	if proxy == "" {
		t.Skip("set TWITTER_PROXY to run the live twitter fetch test")
	}
	item, err := Fetch("https://x.com/Interior/status/463440424141459456", proxy, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if item.Meta.ID == "" || (item.Text == "" && len(item.MediaPaths) == 0) {
		t.Fatalf("empty item: %+v", item)
	}
	t.Logf("kind=%s author=%s mediaKind=%s media=%d text=%.80q",
		item.Kind, item.Meta.Author, item.MediaKind, len(item.MediaPaths), item.Text)
}
