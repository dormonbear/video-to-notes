package douyin

import "testing"

func TestExtractURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2.84 复制打开抖音…… https://v.douyin.com/EklG9cO2IMQ/ 看看", "https://v.douyin.com/EklG9cO2IMQ/"},
		{"https://www.douyin.com/video/7649793480441713339?x=1", "https://www.douyin.com/video/7649793480441713339?x=1"},
		{"no link here", ""},
	}
	for _, c := range cases {
		if got := extractURL(c.in); got != c.want {
			t.Errorf("extractURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExtractURLs(t *testing.T) {
	in := "批量：https://v.douyin.com/AAA/ 然后 https://www.douyin.com/video/123?x=1 再来 https://v.douyin.com/AAA/ 重复"
	got := ExtractURLs(in)
	want := []string{"https://v.douyin.com/AAA/", "https://www.douyin.com/video/123?x=1"}
	if len(got) != len(want) {
		t.Fatalf("ExtractURLs returned %d urls, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("url[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if n := len(ExtractURLs("no link")); n != 0 {
		t.Errorf("expected 0 urls for no-link text, got %d", n)
	}
}
