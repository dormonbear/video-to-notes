package source

import "testing"

func TestClassify(t *testing.T) {
	in := `看这个 https://v.douyin.com/abc/ 和 https://x.com/foo/status/123 ` +
		`还有 https://example.com/post?a=1。 重复 https://v.douyin.com/abc/ ` +
		`别误判 https://netflix.com/title`
	got := Classify(in)
	want := []Ref{
		{Kind: "douyin", URL: "https://v.douyin.com/abc/"},
		{Kind: "twitter", URL: "https://x.com/foo/status/123"},
		{Kind: "web", URL: "https://example.com/post?a=1"}, // trailing 。 trimmed
		{Kind: "web", URL: "https://netflix.com/title"},    // NOT twitter (x.com substring)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d refs %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ref %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestHostOf(t *testing.T) {
	if h := HostOf("https://Sub.X.com/a"); h != "sub.x.com" {
		t.Errorf("HostOf = %q, want sub.x.com", h)
	}
}
