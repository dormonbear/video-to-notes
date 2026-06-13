package note

import (
	"strings"
	"testing"
)

func TestSafeFilename(t *testing.T) {
	got := safeFilename("a/b:c*?\"<>|d")
	if strings.ContainsAny(got, `/\:*?"<>|`) {
		t.Errorf("safeFilename left unsafe chars: %q", got)
	}
}

func TestRenderContainsAllSections(t *testing.T) {
	md := render(Input{
		Title: "标题", Author: "作者", SourceURL: "https://v.douyin.com/x/",
		Date: "2026-06-13",
		Data: Data{
			Summary:   "一句话",
			Tags:      []string{"a", "b"},
			KeyPoints: []string{"p1", "p2"},
			Transcript: "全文",
		},
	})
	for _, want := range []string{
		"source: https://v.douyin.com/x/", "author: 作者",
		"tags: [a, b]", "## 一句话摘要", "一句话",
		"## 核心要点", "- p1", "## 完整转写", "全文",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("render output missing %q\n---\n%s", want, md)
		}
	}
}
