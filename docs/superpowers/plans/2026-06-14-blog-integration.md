# 博客集成 Implementation Plan（阶段一）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 video-to-notes bot 能把抖音视频笔记输出为 AstroPaper 博客文章并 push 到 dormon.net 仓库，通过博客自带 RSS 用 Folo 阅读。

**Architecture:** 纯 bot 侧改动（博客 RSS 已输出 blog 全集合，无需改博客代码）。新增可配置 `NOTE_FORMAT=obsidian|blog`；blog 模式渲染符合 AstroPaper schema 的 frontmatter，写入 dormon.net 的 `src/content/blog/`，复用已有 `internal/gitsync` push。

**Tech Stack:** Go；输出目标 Astro/AstroPaper 博客（content collection schema：title/pubDatetime/description/tags/draft）。

---

## 文件结构

```
internal/douyin/douyin.go    # Meta 增加 ID
internal/prompt/prompt.go    # prompt 增加生成 title
internal/llm/llm.go          # schema 增加 title；Data.Title 赋值
internal/note/note.go        # 新增 blog 渲染器 + Options + Write 分派
internal/config/config.go    # NOTE_FORMAT / BLOG_DRAFT / BLOG_TAG
main.go                      # 传 Options、Meta.ID、按格式定 Date
.env.example                 # 新增 blog 相关变量
```

数据契约（贯穿全程，名称固定）：
- `douyin.Meta{ Title, Author, SourceURL, ID string }`
- `note.Data{ Title, Summary string; Tags, KeyPoints []string; Transcript string }`
- `note.Input{ Title, Author, SourceURL, VideoID, Date string; Data Data }`
- `note.Options{ Format string; Draft bool; Tag string }`

---

## Task 1: douyin.Meta 增加 ID

**Files:** Modify `internal/douyin/douyin.go`

- [ ] **Step 1: 修改 Meta 与 Fetch 返回 ID**

把 `Meta` 结构与 Fetch 末尾构造改为：
```go
type Meta struct {
	Title     string
	Author    string
	SourceURL string
	ID        string // 抖音视频 id，用于博客文件名/slug
}
```
在 `Fetch` 的 `return audioPath, Meta{...}, nil` 处加上 `ID: info.ID`：
```go
	return audioPath, Meta{Title: info.Title, Author: author, SourceURL: info.WebpageURL, ID: info.ID}, nil
```

- [ ] **Step 2: 编译**

Run: `go build ./internal/douyin/`
Expected: 无输出（成功）。

- [ ] **Step 3: Commit**

```bash
git add internal/douyin/
git commit -m "feat: expose video id in douyin.Meta"
```

---

## Task 2: 让 Gemini 生成简短标题

**Files:** Modify `internal/prompt/prompt.go`, `internal/llm/llm.go`, `internal/note/note.go`

- [ ] **Step 1: note.Data 增加 Title 字段**

在 `internal/note/note.go` 的 `Data` 结构最前面加字段：
```go
type Data struct {
	Title      string
	Summary    string
	Tags       []string
	KeyPoints  []string
	Transcript string
}
```

- [ ] **Step 2: prompt 增加 title 要求**

把 `internal/prompt/prompt.go` 的 `VideoNote` 常量正文改为（在 summary 前加 title 项）：
```go
const VideoNote = `你是一个视频笔记助手。这是一段视频的音频，请听完后用中文输出：
1. title：一个不超过 20 字的简短、能概括内容的标题（用作博客标题，不要含特殊符号）。
2. summary：一句话概括主旨。
3. tags：3-6 个主题标签（不带 # 号，简短名词）。
4. key_points：核心要点/重点，每条一句，按讲述顺序。
5. transcript：尽量完整的口语转写文字稿（去掉语气词、修正明显口误，保留原意）。
严格按要求的 JSON schema 输出，不要输出多余文字。`
```

- [ ] **Step 3: llm schema 增加 title，赋值 Data.Title**

在 `internal/llm/llm.go` 的 `noteSchema()` 的 `Properties` 里加 `"title": str,` 并把 `"title"` 加入 `Required` 与（如有）`PropertyOrdering` 之前位置：
```go
		"properties": map[string]any{
			"title":      str,
			"summary":    str,
			"tags":       map[string]any{"type": "array", "items": str},
			"key_points": map[string]any{"type": "array", "items": str},
			"transcript": str,
		},
		"required":             []string{"title", "summary", "tags", "key_points", "transcript"},
```
在 `Analyze` 解析结构体与返回里加 Title：
```go
	var d struct {
		Title      string   `json:"title"`
		Summary    string   `json:"summary"`
		Tags       []string `json:"tags"`
		KeyPoints  []string `json:"key_points"`
		Transcript string   `json:"transcript"`
	}
	...
	return note.Data{Title: d.Title, Summary: d.Summary, Tags: d.Tags, KeyPoints: d.KeyPoints, Transcript: d.Transcript}, nil
```

- [ ] **Step 4: 编译 + 单测**

Run: `go build ./... && go test ./internal/note/ -v`
Expected: 编译通过；note 现有单测仍 PASS（Data 加字段不影响 render 既有断言）。

- [ ] **Step 5: Commit**

```bash
git add internal/prompt/ internal/llm/ internal/note/
git commit -m "feat: generate a short blog title in the model output"
```

---

## Task 3: config 增加博客相关配置

**Files:** Modify `internal/config/config.go`, `internal/config/config_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/config/config_test.go` 的 `TestLoadAppliesDefaults` 末尾（最后一个 `}` 之前）加断言：
```go
	if c.NoteFormat != "obsidian" {
		t.Errorf("NoteFormat default = %q, want obsidian", c.NoteFormat)
	}
	if c.BlogTag != "video-note" {
		t.Errorf("BlogTag default = %q, want video-note", c.BlogTag)
	}
	if c.BlogDraft != false {
		t.Errorf("BlogDraft default = %v, want false", c.BlogDraft)
	}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/config/ -run TestLoadAppliesDefaults -v`
Expected: FAIL（c.NoteFormat/BlogTag/BlogDraft 未定义）。

- [ ] **Step 3: 实现**

在 `Config` 结构加字段：
```go
	NoteFormat string // "obsidian" 或 "blog"
	BlogDraft  bool   // blog 模式：是否以草稿发布
	BlogTag    string // blog 模式：标记 tag
```
在 `Load` 的 env key 列表加 `"NOTE_FORMAT", "BLOG_DRAFT", "BLOG_TAG"`。
在 `loadFrom` 的构造里加：
```go
		NoteFormat: env["NOTE_FORMAT"],
		BlogDraft:  env["BLOG_DRAFT"] == "true" || env["BLOG_DRAFT"] == "1",
		BlogTag:    env["BLOG_TAG"],
```
在默认值区加：
```go
	if c.NoteFormat == "" {
		c.NoteFormat = "obsidian"
	}
	if c.BlogTag == "" {
		c.BlogTag = "video-note"
	}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/config/ -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat: config for blog output format/draft/tag"
```

---

## Task 4: note blog 渲染器 + Write 分派

**Files:** Modify `internal/note/note.go`, `internal/note/note_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/note/note_test.go` 末尾追加：
```go
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
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/note/ -run 'Blog' -v`
Expected: FAIL（renderBlog/Options undefined；Write 签名不符）。

- [ ] **Step 3: 实现**

在 `internal/note/note.go` 增加 Options 与辅助函数，新增 renderBlog，并把 `render` 改名为 `renderObsidian`，重写 `Write` 分派。完整代码：

```go
// Options 控制输出格式。
type Options struct {
	Format string // "obsidian" 或 "blog"
	Draft  bool   // blog：草稿
	Tag    string // blog：标记 tag
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
	b.WriteString(in.Data.Summary + "\n\n")
	b.WriteString("## 核心要点\n")
	for _, p := range in.Data.KeyPoints {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	b.WriteString("\n## 完整转写\n")
	b.WriteString(in.Data.Transcript + "\n")
	return b.String()
}
```

把原 `func render(in Input) string {` 改名为 `func renderObsidian(in Input) string {`。

重写 `Write`：
```go
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
```

在 `Input` 结构加 `VideoID string` 字段：
```go
type Input struct {
	Title     string
	Author    string
	SourceURL string
	VideoID   string
	Date      string
	Data      Data
}
```

- [ ] **Step 4: 更新既有 obsidian 测试调用 Write 的签名**

`internal/note/note_test.go` 里 `TestWriteHandlesLongMultibyteTitle` 调用 `Write(Input{...}, dir, "video-notes")` 改为带 Options：
```go
	rel, err := Write(Input{...}, Options{Format: "obsidian"}, dir, "video-notes")
```
（保留其余不变。）

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/note/ -v`
Expected: 全 PASS（含新 blog 测试与既有 obsidian 测试）。

- [ ] **Step 6: Commit**

```bash
git add internal/note/
git commit -m "feat: blog renderer and format-dispatching note.Write"
```

---

## Task 5: main.go 接线

**Files:** Modify `main.go`

- [ ] **Step 1: 按格式构造 Date、Options、VideoID 并调用新 Write**

把 `main.go` 中从 `edit("📝 写入笔记中…")` 到 `note.Write(...)` 调用整段替换为：
```go
	edit("📝 写入笔记中…")
	date := time.Now().Format("2006-01-02")
	if a.cfg.NoteFormat == "blog" {
		date = time.Now().Format(time.RFC3339)
	}
	relPath, err := note.Write(note.Input{
		Title:     meta.Title,
		Author:    meta.Author,
		SourceURL: meta.SourceURL,
		VideoID:   meta.ID,
		Date:      date,
		Data:      data,
	}, note.Options{
		Format: a.cfg.NoteFormat,
		Draft:  a.cfg.BlogDraft,
		Tag:    a.cfg.BlogTag,
	}, a.cfg.VaultPath, a.cfg.NoteSubdir)
	if err != nil {
		edit(fmt.Sprintf("❌ 写入失败：%v", err))
		return
	}
```
（其后的 `if a.cfg.GitSync { ... }` 与最终 `edit("✅ ...")` 保持不变。）

- [ ] **Step 2: 编译 + 全量单测 + 集成编译**

Run: `go build ./... && go vet ./... && go test ./... && go vet -tags integration ./...`
Expected: 全部通过。

- [ ] **Step 3: Commit**

```bash
git add main.go
git commit -m "feat: wire blog output format into the bot handler"
```

---

## Task 6: .env.example + 本机切到博客 + 端到端验证

**Files:** Modify `.env.example`；本机 `.env`（不入库）

- [ ] **Step 1: 更新 .env.example**

在 `.env.example` 的 `NOTE_SUBDIR` 行附近加说明与变量：
```
# 输出格式：obsidian（写 vault）或 blog（写 AstroPaper 博客仓库的 src/content/blog 并 push）
NOTE_FORMAT=obsidian
# blog 模式：草稿发布（true 则文章 draft，不上线）
BLOG_DRAFT=false
# blog 模式：区分用的 tag
BLOG_TAG=video-note
```

- [ ] **Step 2: 本机 .env 切到博客**

把本机 `.env` 改为：
```
NOTE_FORMAT=blog
VAULT_PATH=/Users/dormonzhou/Projects/dormon.net
NOTE_SUBDIR=src/content/blog
GIT_SYNC=true
```
（其余 token/key/proxy 不变。）

- [ ] **Step 3: 重启 bot**

```bash
kill $(pgrep -f /tmp/v2n) 2>/dev/null
go build -o /tmp/v2n . && set -a && . ./.env && set +a && nohup /tmp/v2n > /tmp/v2n.log 2>&1 &
sleep 3 && cat /tmp/v2n.log
```
Expected: `video-to-notes bot started`。

- [ ] **Step 4: 端到端 + 验证 schema 合法**

给 bot 发一条抖音口令。等待笔记生成后：
```bash
ls -t /Users/dormonzhou/Projects/dormon.net/src/content/blog/*.md | head -1
cd /Users/dormonzhou/Projects/dormon.net && pnpm install && pnpm exec astro check
```
Expected: 生成 `YYYY-MM-DD-douyin-<id>.md`，frontmatter 含 title/pubDatetime/description/tags(含 video-note)/draft；`astro check` 不报 content collection schema 错误（证明文章合规、不会让构建挂）。

- [ ] **Step 5: Commit**

```bash
cd /Users/dormonzhou/Projects/video-to-notes
git add .env.example
git commit -m "docs: blog output env vars in .env.example"
```

---

## Self-Review 检查记录

- **Spec 覆盖**：发布位置(blog 集合+video-note tag, Task4/5)/RSS 复用(无需改博客)/自动发布+draft 开关(Task3/4)/Gemini 生成标题(Task2)/可配置输出格式(Task3/4/5)/gitsync push(已存在, Task6 配置启用)/frontmatter 兜底(renderBlog: title/summary 兜底, ensureTag, draft) 全部有任务。✅
- **类型一致**：`note.Data{Title,Summary,Tags,KeyPoints,Transcript}`、`note.Input{...,VideoID,...}`、`note.Options{Format,Draft,Tag}`、`douyin.Meta{...,ID}` 在 llm/note/main 中名称一致；`Write(Input, Options, vaultPath, subdir)` 新签名在 Task4 定义、Task5 调用、Task4 Step4 修旧测试调用，三处一致。✅
- **无占位**：每个代码步骤含完整代码与确切命令/预期。
- **构建安全**：Task6 Step4 用 `astro check` 验证 frontmatter 合规，确保不破坏博客构建。
