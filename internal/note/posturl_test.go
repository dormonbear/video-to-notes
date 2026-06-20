package note

import "testing"

func TestPostURL(t *testing.T) {
	cases := []struct {
		name, base, relPath, want string
	}{
		{"blog post", "https://blog.dormon.net", "src/content/posts/2026-06-18-douyin-7651611087061683508.md", "https://blog.dormon.net/posts/2026-06-18-douyin-7651611087061683508"},
		{"trailing slash on base", "https://blog.dormon.net/", "src/content/posts/x.md", "https://blog.dormon.net/posts/x"},
		{"empty base disables link", "", "src/content/posts/x.md", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PostURL(c.base, c.relPath); got != c.want {
				t.Errorf("PostURL(%q, %q) = %q, want %q", c.base, c.relPath, got, c.want)
			}
		})
	}
}
