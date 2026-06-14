package note

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSafeFilename(t *testing.T) {
	got := safeFilename("a/b:c*?\"<>|d")
	if strings.ContainsAny(got, `/\:*?"<>|`) {
		t.Errorf("safeFilename left unsafe chars: %q", got)
	}
}

// 真机 bug 复现：长中文标题 + 换行。按字节截断会切断多字节字符产生非法 UTF-8，
// 换行会进文件名。两者都必须被清理，否则 os.WriteFile 报 "illegal byte sequence"。
func TestSafeFilenameMultibyteAndNewline(t *testing.T) {
	long := strings.Repeat("夜航船", 40) // 120 个中文字符
	got := safeFilename("第一行\n第二行 " + long)
	if !utf8.ValidString(got) {
		t.Errorf("safeFilename produced invalid UTF-8: %q", got)
	}
	if strings.ContainsAny(got, "\n\r\t") {
		t.Errorf("safeFilename left a newline/tab: %q", got)
	}
	if n := utf8.RuneCountInString(got); n > 60 {
		t.Errorf("safeFilename too long: %d runes", n)
	}
}

func TestWriteHandlesLongMultibyteTitle(t *testing.T) {
	dir := t.TempDir()
	rel, err := Write(Input{
		Title:     "夜航船 v0.01『夜航船』是一个专注于硬核技术的播客\n复制打开抖音的口令尾巴 " + strings.Repeat("长", 50),
		Author:    "夜航船",
		SourceURL: "https://v.douyin.com/x/",
		Date:      "2026-06-14",
		Data:      Data{Summary: "s", Tags: []string{"a"}, KeyPoints: []string{"p"}, Transcript: "t"},
	}, Options{Format: "obsidian"}, dir, "video-notes")
	if err != nil {
		t.Fatalf("Write failed on long multibyte title: %v", err)
	}
	if rel == "" {
		t.Error("expected a relative path")
	}
}

func TestRenderBlogFrontmatterValid(t *testing.T) {
	md := renderBlog(Input{
		Title:     "原始很长的抖音标题",
		Author:    "夜航船",
		SourceURL: "https://v.douyin.com/x/",
		Date:      "2026-06-14T06:06:00Z",
		Data: Data{
			Title: "Agent 落地踩坑", Summary: "讲 Agent 落地经验。",
			Tags: []string{"Agent", "AI"}, KeyPoints: []string{"p1"}, Transcript: "全文",
		},
	}, Options{Format: "blog", Draft: false, Tag: "video-note"})
	for _, want := range []string{
		`title: "Agent 落地踩坑"`,
		"pubDatetime: 2026-06-14T06:06:00Z",
		`description: "讲 Agent 落地经验。"`,
		`"video-note"`, "draft: false",
		"## 核心要点", "- p1", "## 完整转写", "全文",
		"来源：[抖音 @夜航船](https://v.douyin.com/x/)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("renderBlog missing %q\n---\n%s", want, md)
		}
	}
}

func TestWriteBlogFilenameIsAsciiSlug(t *testing.T) {
	dir := t.TempDir()
	rel, err := Write(Input{
		Title: "无所谓", Author: "夜航船", SourceURL: "https://v.douyin.com/x/",
		VideoID: "7650479446944032101", Date: "2026-06-14T06:06:00Z",
		Data: Data{Title: "标题", Summary: "s", Tags: []string{"a"}, KeyPoints: []string{"p"}, Transcript: "t"},
	}, Options{Format: "blog", Tag: "video-note"}, dir, "src/content/blog")
	if err != nil {
		t.Fatalf("Write blog failed: %v", err)
	}
	if rel != "src/content/blog/2026-06-14-douyin-7650479446944032101.md" {
		t.Errorf("unexpected blog path: %s", rel)
	}
}

func TestRenderContainsAllSections(t *testing.T) {
	md := renderObsidian(Input{
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
		`source: "https://v.douyin.com/x/"`, `author: "作者"`,
		`tags: ["a", "b"]`, "## 一句话摘要", "一句话",
		"## 核心要点", "- p1", "## 完整转写", "全文",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("render output missing %q\n---\n%s", want, md)
		}
	}
}

func TestRenderEscapesUnsafeFrontmatter(t *testing.T) {
	md := renderObsidian(Input{
		Title:     "踩过的坑 #agent: 第一行\n第二行",
		Author:    "作者",
		SourceURL: "https://v.douyin.com/x/",
		Date:      "2026-06-13",
		Data:      Data{Summary: "s", Tags: []string{"a"}, KeyPoints: []string{"p"}, Transcript: "t"},
	})
	// The title line must be a single quoted scalar with no raw newline and no comment truncation.
	if !strings.Contains(md, `title: "踩过的坑 #agent: 第一行 第二行"`) {
		t.Errorf("title not safely quoted/escaped:\n%s", md)
	}
}
