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
