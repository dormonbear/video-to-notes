package note

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Data 是 Gemini 返回的结构化笔记内容。
type Data struct {
	Summary    string
	Tags       []string
	KeyPoints  []string
	Transcript string
}

// Input 是渲染一篇笔记所需的全部信息。
type Input struct {
	Title     string
	Author    string
	SourceURL string
	Date      string // YYYY-MM-DD
	Data      Data
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

func render(in Input) string {
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

	b.WriteString("## 核心要点\n")
	for _, p := range in.Data.KeyPoints {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	b.WriteString("\n")

	b.WriteString("## 完整转写\n")
	b.WriteString(in.Data.Transcript + "\n")
	return b.String()
}

// Write 渲染笔记并写入 vaultPath/subdir/<date>-<title>.md，返回相对 vault 的路径。
func Write(in Input, vaultPath, subdir string) (string, error) {
	dir := filepath.Join(vaultPath, subdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir vault subdir: %w", err)
	}
	name := fmt.Sprintf("%s-%s.md", in.Date, safeFilename(in.Title))
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte(render(in)), 0o644); err != nil {
		return "", fmt.Errorf("write note: %w", err)
	}
	return filepath.Join(subdir, name), nil
}
