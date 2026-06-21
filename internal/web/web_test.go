package web

import (
	"strings"
	"testing"
)

func TestExtract(t *testing.T) {
	html := []byte(`<html><head><title>我的文章</title></head>
	<body><script>var x=1;</script><style>.a{}</style>
	<h1>标题</h1><p>第一段正文足够长足够长足够长。</p>
	<p>第二段也有内容内容内容内容内容。</p><noscript>ns</noscript></body></html>`)
	title, text := extract(html)
	if title != "我的文章" {
		t.Errorf("title = %q", title)
	}
	for _, want := range []string{"标题", "第一段正文", "第二段"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q; got %q", want, text)
		}
	}
	for _, bad := range []string{"var x=1", ".a{}", "ns"} {
		if strings.Contains(text, bad) {
			t.Errorf("text should not contain %q; got %q", bad, text)
		}
	}
}

func TestURLID(t *testing.T) {
	a, b := urlID("https://e.com/p"), urlID("https://e.com/p")
	if a != b || len(a) != 12 {
		t.Errorf("urlID unstable/wrong len: %q %q", a, b)
	}
	if urlID("https://e.com/p") == urlID("https://e.com/q") {
		t.Error("different URLs must hash differently")
	}
}
