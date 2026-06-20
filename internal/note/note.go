package note

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Data 是模型返回的结构化笔记内容。
type Data struct {
	Title   string // 模型生成的简短标题（用作博客 title）
	Summary string
	Tags    []string
	Article string // AI 二次创作的成稿正文（markdown），取代逐字稿
}

// Input 是渲染一篇笔记所需的全部信息。
type Input struct {
	Title     string // 抖音原始标题
	Author    string
	SourceURL string
	VideoID   string // 抖音视频 id（blog 模式做文件名/slug）
	Date      string // obsidian: YYYY-MM-DD；blog: ISO 8601
	Data      Data
}

// Options 控制输出格式。
type Options struct {
	Format string // "obsidian" 或 "blog"
	Draft  bool   // blog：草稿
	Tag    string // blog：标记 tag
}

var unsafe = strings.NewReplacer(
	"/", "_", "\\", "_", ":", "_", "*", "_",
	"?", "_", `"`, "_", "<", "_", ">", "_", "|", "_",
)

func yamlStr(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strconv.Quote(s)
}

func firstRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return strings.TrimSpace(string(r[:n]))
	}
	return s
}

func ensureTag(tags []string, tag string) []string {
	for _, t := range tags {
		if t == tag {
			return tags
		}
	}
	return append(append([]string{}, tags...), tag)
}

func safeFilename(s string) string {
	s = unsafe.Replace(s)
	// 把换行/制表/控制字符压成空格，避免文件名里出现非法字符。
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || r < 0x20 {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ") // 折叠多余空白
	// 按 rune 截断（不能按字节，否则会切断多字节 UTF-8 字符 → 非法字节序列，写文件失败）。
	if r := []rune(s); len(r) > 60 {
		s = strings.TrimSpace(string(r[:60]))
	}
	if s == "" {
		s = "untitled"
	}
	return s
}

func renderObsidian(in Input) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "source: %s\n", yamlStr(in.SourceURL))
	fmt.Fprintf(&b, "author: %s\n", yamlStr(in.Author))
	fmt.Fprintf(&b, "title: %s\n", yamlStr(in.Title))
	fmt.Fprintf(&b, "date: %s\n", in.Date)
	quoted := make([]string, len(in.Data.Tags))
	for i, t := range in.Data.Tags {
		quoted[i] = yamlStr(t)
	}
	fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(quoted, ", "))
	b.WriteString("---\n\n")

	b.WriteString("## 一句话摘要\n")
	b.WriteString(in.Data.Summary + "\n\n")

	b.WriteString(strings.TrimSpace(in.Data.Article) + "\n")
	return b.String()
}

// renderBlog 输出符合 AstroPaper content collection schema 的文章。
func renderBlog(in Input, opts Options) string {
	title := strings.TrimSpace(in.Data.Title)
	if title == "" {
		title = firstRunes(in.Data.Summary, 20)
	}
	if title == "" {
		title = "视频笔记"
	}
	tags := ensureTag(in.Data.Tags, opts.Tag)
	quoted := make([]string, len(tags))
	for i, t := range tags {
		quoted[i] = yamlStr(t)
	}

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", yamlStr(title))
	fmt.Fprintf(&b, "pubDatetime: %s\n", in.Date) // 调用方传 ISO 8601
	fmt.Fprintf(&b, "description: %s\n", yamlStr(in.Data.Summary))
	fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(quoted, ", "))
	fmt.Fprintf(&b, "draft: %t\n", opts.Draft)
	b.WriteString("---\n\n")

	fmt.Fprintf(&b, "> 来源：[抖音 @%s](%s)\n\n", in.Author, in.SourceURL)
	b.WriteString(strings.TrimSpace(in.Data.Article) + "\n")
	return b.String()
}

// Write 按 opts.Format 渲染并写入 vaultPath/subdir，返回相对 vaultPath 的路径。
func Write(in Input, opts Options, vaultPath, subdir string) (string, error) {
	dir := filepath.Join(vaultPath, subdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir target dir: %w", err)
	}
	var name, content string
	switch opts.Format {
	case "blog":
		date := in.Date
		if len(date) >= 10 {
			date = date[:10]
		}
		name = fmt.Sprintf("%s-douyin-%s.md", date, in.VideoID)
		content = renderBlog(in, opts)
	default: // obsidian
		name = fmt.Sprintf("%s-%s.md", in.Date, safeFilename(in.Title))
		content = renderObsidian(in)
	}
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write note: %w", err)
	}
	return filepath.Join(subdir, name), nil
}

// Exists 报告 blog 模式下某 video id 是否已有文章（任意日期）。文件名规则是
// {date}-douyin-{id}.md，按 *-douyin-{id}.md glob 匹配，用于跨请求/跨天去重。
// videoID 为空、或 obsidian 模式（文件名不含 id）时返回 false。
func Exists(vaultPath, subdir, videoID string) bool {
	if videoID == "" {
		return false
	}
	matches, _ := filepath.Glob(filepath.Join(vaultPath, subdir, "*-douyin-"+videoID+".md"))
	return len(matches) > 0
}

// PostURL 把 blog 模式写出的相对路径映射为 AstroPaper 文章在线地址。
// base 为站点域名（如 https://dormon.net）；base 为空返回 ""。
// AstroPaper 路由为 /posts/<slug>，slug 默认取文件名去掉 .md。
func PostURL(base, relPath string) string {
	if base == "" {
		return ""
	}
	slug := strings.TrimSuffix(filepath.Base(relPath), ".md")
	return strings.TrimRight(base, "/") + "/posts/" + slug
}
