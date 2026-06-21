package twitter

import "testing"

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
	text, imgs, author, err := parseSyndication(js)
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello world" || author != "jack" {
		t.Errorf("text/author = %q/%q", text, author)
	}
	if len(imgs) != 1 || imgs[0] != "https://pbs.twimg.com/a.jpg" {
		t.Errorf("images = %v (want only the photo)", imgs)
	}
}

func TestSynTokenStable(t *testing.T) {
	if synToken("1234567890123456789") == "" {
		t.Error("token should be non-empty")
	}
}
