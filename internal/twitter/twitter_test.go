package twitter

import (
	"strings"
	"testing"
)

func TestTweetID(t *testing.T) {
	cases := map[string]string{
		"https://x.com/jack/status/1234567890123456789":  "1234567890123456789",
		"https://twitter.com/foo/status/42?s=20":         "42",
		"https://mobile.twitter.com/a/status/99/photo/1": "99",
	}
	for in, want := range cases {
		got, err := tweetID(in)
		if err != nil || got != want {
			t.Errorf("tweetID(%q) = %q,%v want %q", in, got, err, want)
		}
	}
	if _, err := tweetID("https://x.com/jack"); err == nil {
		t.Error("want error when no /status/ segment")
	}
}

func TestParseSyndication(t *testing.T) {
	js := []byte(`{"text":"hello world","user":{"screen_name":"jack"},
		"mediaDetails":[{"type":"photo","media_url_https":"https://pbs.twimg.com/a.jpg"},
		{"type":"video","media_url_https":"https://pbs.twimg.com/poster.jpg"}]}`)
	text, imgs, author, isArticle, err := parseSyndication(js)
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello world" || author != "jack" || isArticle {
		t.Errorf("text/author/isArticle = %q/%q/%v", text, author, isArticle)
	}
	if len(imgs) != 1 || imgs[0] != "https://pbs.twimg.com/a.jpg" {
		t.Errorf("images = %v (want only the photo)", imgs)
	}
}

// An X Article exposes only title + preview here; isArticle must be true and the
// cover image collected, so the caller can refuse to fabricate a full article.
func TestParseSyndicationArticle(t *testing.T) {
	js := []byte(`{"text":"https://t.co/abc","user":{"screen_name":"thedankoe"},
		"article":{"title":"How to fix your life","preview_text":"If you're like me...",
		"cover_media":{"media_info":{"original_img_url":"https://pbs.twimg.com/cover.jpg"}}}}`)
	text, imgs, author, isArticle, err := parseSyndication(js)
	if err != nil {
		t.Fatal(err)
	}
	if !isArticle {
		t.Error("expected isArticle=true")
	}
	if author != "thedankoe" {
		t.Errorf("author = %q", author)
	}
	if !strings.Contains(text, "How to fix your life") || !strings.Contains(text, "If you're like me") {
		t.Errorf("article text = %q", text)
	}
	if len(imgs) != 1 || imgs[0] != "https://pbs.twimg.com/cover.jpg" {
		t.Errorf("cover image not collected: %v", imgs)
	}
}

// A tweet whose text is only a t.co link must reduce to empty (caught downstream).
func TestParseSyndicationStripsTco(t *testing.T) {
	js := []byte(`{"text":"https://t.co/xyz","user":{"screen_name":"a"}}`)
	text, _, _, _, err := parseSyndication(js)
	if err != nil {
		t.Fatal(err)
	}
	if text != "" {
		t.Errorf("link-only text should strip to empty, got %q", text)
	}
}

func TestSynTokenStable(t *testing.T) {
	if synToken("1234567890123456789") == "" {
		t.Error("token should be non-empty")
	}
}

func TestFindString(t *testing.T) {
	j := map[string]any{
		"data": map[string]any{
			"result": []any{
				map[string]any{"article": map[string]any{"plain_text": "full body here"}},
			},
		},
	}
	if got := findString(j, "plain_text"); got != "full body here" {
		t.Errorf("findString = %q, want %q", got, "full body here")
	}
	if got := findString(j, "missing"); got != "" {
		t.Errorf("missing key should yield empty, got %q", got)
	}
}

func TestParseBookmarks(t *testing.T) {
	body := []byte(`{"data":{"bookmark_timeline_v2":{"timeline":{"instructions":[{"type":"TimelineAddEntries","entries":[
		{"entryId":"tweet-111","content":{"itemContent":{"tweet_results":{"result":{"rest_id":"111"}}}}},
		{"entryId":"tweet-222","content":{"itemContent":{"tweet_results":{"result":{"rest_id":"222"}}}}},
		{"entryId":"cursor-bottom-0","content":{"__typename":"TimelineTimelineCursor","cursorType":"Bottom","value":"NEXTCURSOR"}}
	]}]}}}}`)
	ids, next := parseBookmarks(body)
	if len(ids) != 2 || ids[0] != "111" || ids[1] != "222" {
		t.Errorf("ids = %v, want [111 222]", ids)
	}
	if next != "NEXTCURSOR" {
		t.Errorf("cursor = %q, want NEXTCURSOR", next)
	}
}
