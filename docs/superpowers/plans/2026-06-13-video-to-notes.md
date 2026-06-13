# video-to-notes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把抖音分享链接发给自建 Telegram bot，自动下载无水印视频、用 Gemini 2.5 Flash 解析、生成结构化 markdown 笔记写入本地 Obsidian 库。

**Architecture:** 单 Go 进程跑在 Mac 上，long-polling 监听 Telegram。收到消息→正则提抖音链接→调 `yt-dlp` 二进制下载无水印 mp4→官方 genai SDK 上传视频并结构化输出→渲染 markdown 写入 vault→TG 回复进度。

**Tech Stack:** Go 1.22+；`google.golang.org/genai`（官方 Gemini SDK）；`github.com/go-telegram/bot`（零依赖 TG 框架）；`yt-dlp` 二进制（系统已装 2026.06.09）。

---

## 文件结构

```
video-to-notes/
├── go.mod
├── .env.example
├── .gitignore
├── main.go                      # 启动、加载 config、注册 handler、long-polling
├── internal/config/config.go    # 从环境变量加载并校验配置
├── internal/config/config_test.go
├── internal/douyin/douyin.go    # 提链接 + 调 yt-dlp 下载 → (videoPath, Meta)
├── internal/douyin/douyin_test.go
├── internal/prompt/prompt.go    # Gemini prompt 模板（常量）
├── internal/gemini/gemini.go    # 上传视频 + 结构化输出 → Note
├── internal/gemini/gemini_test.go
├── internal/note/note.go        # 渲染 markdown + 写入 vault
└── internal/note/note_test.go
```

依赖边界：`main` 编排，调用 `config`/`douyin`/`gemini`/`note`；`gemini` 调用 `prompt`；其余互不依赖。纯逻辑（config 校验、URL 提取、文件名安全化、markdown 渲染）走单测；触网部分（yt-dlp 下载、Gemini 调用）走 `//go:build integration` 集成测试，默认 `go test` 不跑。

---

## Task 0: 项目脚手架

**Files:**
- Create: `go.mod`, `.env.example`, `.gitignore`

- [ ] **Step 1: 初始化 module 并加依赖**

Run:
```bash
cd /Users/dormonzhou/Projects/video-to-notes
go mod init video-to-notes
go get google.golang.org/genai
go get github.com/go-telegram/bot
```
Expected: `go.mod` 出现，列出两个 require。

- [ ] **Step 2: 写 `.env.example`**

```bash
cat > .env.example <<'EOF'
# Telegram bot token，@BotFather 新建 bot 后获得
TELEGRAM_BOT_TOKEN=

# Google AI Studio API key: https://aistudio.google.com/apikey
GEMINI_API_KEY=

# Gemini 模型
GEMINI_MODEL=gemini-2.5-flash

# Obsidian vault 根目录的绝对路径
VAULT_PATH=/Users/dormonzhou/path/to/vault

# vault 内目标子文件夹（相对 VAULT_PATH），笔记写到这里
NOTE_SUBDIR=video-notes

# 视频临时下载目录
TMP_DIR=/tmp/video-to-notes
EOF
```

- [ ] **Step 3: 写 `.gitignore`**

```bash
cat > .gitignore <<'EOF'
.env
/video-to-notes
/tmp/
*.mp4
EOF
```

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum .env.example .gitignore
git commit -m "chore: scaffold go module and config template"
```

---

## Task 1: config 包（加载 + 校验）

**Files:**
- Create: `internal/config/config.go`, `internal/config/config_test.go`

- [ ] **Step 1: 写失败测试**

```go
// internal/config/config_test.go
package config

import "testing"

func TestLoadValidatesRequiredFields(t *testing.T) {
	env := map[string]string{
		"GEMINI_API_KEY": "key",
		"VAULT_PATH":     "/v",
	} // 缺 TELEGRAM_BOT_TOKEN
	_, err := loadFrom(env)
	if err == nil {
		t.Fatal("expected error for missing TELEGRAM_BOT_TOKEN, got nil")
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	env := map[string]string{
		"TELEGRAM_BOT_TOKEN": "tok",
		"GEMINI_API_KEY":     "key",
		"VAULT_PATH":         "/v",
	}
	c, err := loadFrom(env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Model != "gemini-2.5-flash" {
		t.Errorf("Model default = %q, want gemini-2.5-flash", c.Model)
	}
	if c.NoteSubdir != "video-notes" {
		t.Errorf("NoteSubdir default = %q, want video-notes", c.NoteSubdir)
	}
	if c.TmpDir != "/tmp/video-to-notes" {
		t.Errorf("TmpDir default = %q", c.TmpDir)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/config/ -run TestLoad -v`
Expected: FAIL（`loadFrom` undefined）。

- [ ] **Step 3: 写实现**

```go
// internal/config/config.go
package config

import (
	"fmt"
	"os"
)

type Config struct {
	TelegramToken string
	GeminiAPIKey  string
	Model         string
	VaultPath     string
	NoteSubdir    string
	TmpDir        string
}

// Load 从进程环境变量读取配置。
func Load() (Config, error) {
	env := map[string]string{}
	for _, k := range []string{
		"TELEGRAM_BOT_TOKEN", "GEMINI_API_KEY", "GEMINI_MODEL",
		"VAULT_PATH", "NOTE_SUBDIR", "TMP_DIR",
	} {
		env[k] = os.Getenv(k)
	}
	return loadFrom(env)
}

func loadFrom(env map[string]string) (Config, error) {
	c := Config{
		TelegramToken: env["TELEGRAM_BOT_TOKEN"],
		GeminiAPIKey:  env["GEMINI_API_KEY"],
		Model:         env["GEMINI_MODEL"],
		VaultPath:     env["VAULT_PATH"],
		NoteSubdir:    env["NOTE_SUBDIR"],
		TmpDir:        env["TMP_DIR"],
	}
	if c.TelegramToken == "" {
		return Config{}, fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}
	if c.GeminiAPIKey == "" {
		return Config{}, fmt.Errorf("GEMINI_API_KEY is required")
	}
	if c.VaultPath == "" {
		return Config{}, fmt.Errorf("VAULT_PATH is required")
	}
	if c.Model == "" {
		c.Model = "gemini-2.5-flash"
	}
	if c.NoteSubdir == "" {
		c.NoteSubdir = "video-notes"
	}
	if c.TmpDir == "" {
		c.TmpDir = "/tmp/video-to-notes"
	}
	return c, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/config/ -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat: config loading with validation and defaults"
```

---

## Task 2: douyin 包（提链接 + 下载）

**Files:**
- Create: `internal/douyin/douyin.go`, `internal/douyin/douyin_test.go`

- [ ] **Step 1: 写 URL 提取的失败测试（纯逻辑，单测）**

```go
// internal/douyin/douyin_test.go
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
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/douyin/ -run TestExtractURL -v`
Expected: FAIL（`extractURL` undefined）。

- [ ] **Step 3: 写实现（提链接 + Download）**

```go
// internal/douyin/douyin.go
package douyin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

// ErrNoURL 表示消息文本里没有抖音链接。
var ErrNoURL = errors.New("no douyin url found in message")

// Meta 是从视频解析出的元数据。
type Meta struct {
	Title     string
	Author    string
	SourceURL string
}

var urlRe = regexp.MustCompile(`https?://[^\s]*douyin\.com/[^\s]+`)

func extractURL(s string) string {
	return urlRe.FindString(s)
}

// Download 提取分享文本里的抖音链接，用 yt-dlp 下载无水印视频到 destDir。
// 返回下载好的视频文件路径与元数据。yt-dlp 默认 format 已是无水印 playback 流。
func Download(shareText, destDir string) (string, Meta, error) {
	url := extractURL(shareText)
	if url == "" {
		return "", Meta{}, ErrNoURL
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", Meta{}, fmt.Errorf("mkdir tmp: %w", err)
	}
	outTmpl := filepath.Join(destDir, "%(id)s.%(ext)s")

	var stdout bytes.Buffer
	cmd := exec.Command("yt-dlp", "--no-progress", "--print-json", "-o", outTmpl, url)
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", Meta{}, fmt.Errorf("yt-dlp failed: %w", err)
	}

	var info struct {
		ID         string `json:"id"`
		Ext        string `json:"ext"`
		Title      string `json:"title"`
		Channel    string `json:"channel"`
		Uploader   string `json:"uploader"`
		WebpageURL string `json:"webpage_url"`
	}
	// --print-json 输出单行 JSON 到 stdout。
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &info); err != nil {
		return "", Meta{}, fmt.Errorf("parse yt-dlp json: %w", err)
	}

	author := info.Channel
	if author == "" {
		author = info.Uploader
	}
	path := filepath.Join(destDir, info.ID+"."+info.Ext)
	if _, err := os.Stat(path); err != nil {
		return "", Meta{}, fmt.Errorf("downloaded file missing at %s: %w", path, err)
	}
	return path, Meta{Title: info.Title, Author: author, SourceURL: info.WebpageURL}, nil
}
```

- [ ] **Step 4: 跑单测确认通过**

Run: `go test ./internal/douyin/ -run TestExtractURL -v`
Expected: PASS。

- [ ] **Step 5: 写集成测试（真链接，gated）**

```go
// internal/douyin/integration_test.go
//go:build integration

package douyin

import (
	"os"
	"testing"
)

func TestDownloadRealLink(t *testing.T) {
	dir := t.TempDir()
	path, meta, err := Download(
		"https://v.douyin.com/EklG9cO2IMQ/", dir,
	)
	if err != nil {
		t.Fatalf("Download error: %v", err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		t.Fatalf("expected non-empty file at %s, err=%v", path, err)
	}
	if meta.Title == "" || meta.SourceURL == "" {
		t.Errorf("expected non-empty meta, got %+v", meta)
	}
}
```

- [ ] **Step 6: 跑集成测试确认真能下载**

Run: `go test ./internal/douyin/ -tags integration -run TestDownloadRealLink -v`
Expected: PASS（下载真实 mp4，meta 非空）。若失败说明抖音解析变动，仅需改本包。

- [ ] **Step 7: Commit**

```bash
git add internal/douyin/
git commit -m "feat: douyin url extraction and yt-dlp download"
```

---

## Task 3: note 包（渲染 markdown + 写入 vault）

**Files:**
- Create: `internal/note/note.go`, `internal/note/note_test.go`

- [ ] **Step 1: 写失败测试（纯逻辑：文件名安全化 + 渲染）**

```go
// internal/note/note_test.go
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
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/note/ -v`
Expected: FAIL（`render`/`safeFilename`/类型 undefined）。

- [ ] **Step 3: 写实现**

```go
// internal/note/note.go
package note

import (
	"fmt"
	"os"
	"path/filepath"
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

func safeFilename(s string) string {
	s = unsafe.Replace(s)
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		s = s[:80]
	}
	if s == "" {
		s = "untitled"
	}
	return s
}

func render(in Input) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "source: %s\n", in.SourceURL)
	fmt.Fprintf(&b, "author: %s\n", in.Author)
	fmt.Fprintf(&b, "title: %s\n", in.Title)
	fmt.Fprintf(&b, "date: %s\n", in.Date)
	fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(in.Data.Tags, ", "))
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
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/note/ -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/note/
git commit -m "feat: markdown note rendering and vault write"
```

---

## Task 4: prompt 包

**Files:**
- Create: `internal/prompt/prompt.go`

- [ ] **Step 1: 写常量（无测试，纯字符串）**

```go
// internal/prompt/prompt.go
package prompt

// VideoNote 指示 Gemini 看完视频后输出结构化笔记。
// 实际结构由 GenerateContentConfig.ResponseSchema 强制，prompt 只描述任务与语言。
const VideoNote = `你是一个视频笔记助手。请观看这段视频（重点听语音内容，画面作为辅助理解），用中文输出：
1. summary：一句话概括视频主旨。
2. tags：3-6 个主题标签（不带 # 号，简短名词）。
3. key_points：视频的核心要点/重点，每条一句，按视频讲述顺序。
4. transcript：尽量完整的口语转写文字稿（去掉语气词、修正明显口误，保留原意）。
严格按要求的 JSON schema 输出，不要输出多余文字。`
```

- [ ] **Step 2: 编译确认**

Run: `go build ./internal/prompt/`
Expected: 无输出（成功）。

- [ ] **Step 3: Commit**

```bash
git add internal/prompt/
git commit -m "feat: gemini prompt template"
```

---

## Task 5: gemini 包（上传 + 结构化输出）

**Files:**
- Create: `internal/gemini/gemini.go`, `internal/gemini/gemini_test.go`

- [ ] **Step 1: 写实现**

```go
// internal/gemini/gemini.go
package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"google.golang.org/genai"

	"video-to-notes/internal/note"
	"video-to-notes/internal/prompt"
)

// Client 包一个 genai 客户端与模型名。
type Client struct {
	gc    *genai.Client
	model string
}

func New(ctx context.Context, apiKey, model string) (*Client, error) {
	gc, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("genai client: %w", err)
	}
	return &Client{gc: gc, model: model}, nil
}

// noteSchema 强制结构化输出，字段与 note.Data 对应。
func noteSchema() *genai.Schema {
	str := &genai.Schema{Type: genai.TypeString}
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"summary":    str,
			"tags":       {Type: genai.TypeArray, Items: str},
			"key_points": {Type: genai.TypeArray, Items: str},
			"transcript": str,
		},
		Required:         []string{"summary", "tags", "key_points", "transcript"},
		PropertyOrdering: []string{"summary", "tags", "key_points", "transcript"},
	}
}

// Analyze 上传视频文件，等待处理完成，调用模型返回结构化笔记内容。
func (c *Client) Analyze(ctx context.Context, videoPath string) (note.Data, error) {
	f, err := os.Open(videoPath)
	if err != nil {
		return note.Data{}, fmt.Errorf("open video: %w", err)
	}
	defer f.Close()

	uploaded, err := c.gc.Files.Upload(ctx, f, &genai.UploadFileConfig{MIMEType: "video/mp4"})
	if err != nil {
		return note.Data{}, fmt.Errorf("upload: %w", err)
	}

	// 轮询直到 ACTIVE。
	for uploaded.State == genai.FileStateProcessing {
		time.Sleep(2 * time.Second)
		uploaded, err = c.gc.Files.Get(ctx, uploaded.Name, nil)
		if err != nil {
			return note.Data{}, fmt.Errorf("poll file: %w", err)
		}
	}
	if uploaded.State != genai.FileStateActive {
		return note.Data{}, fmt.Errorf("file not active, state=%s", uploaded.State)
	}

	contents := []*genai.Content{{
		Role: "user",
		Parts: []*genai.Part{
			genai.NewPartFromText(prompt.VideoNote),
			genai.NewPartFromURI(uploaded.URI, uploaded.MIMEType),
		},
	}}

	resp, err := c.gc.Models.GenerateContent(ctx, c.model, contents, &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   noteSchema(),
	})
	if err != nil {
		return note.Data{}, fmt.Errorf("generate: %w", err)
	}

	var out struct {
		Summary    string   `json:"summary"`
		Tags       []string `json:"tags"`
		KeyPoints  []string `json:"key_points"`
		Transcript string   `json:"transcript"`
	}
	if err := json.Unmarshal([]byte(resp.Text()), &out); err != nil {
		return note.Data{}, fmt.Errorf("parse model json: %w", err)
	}
	return note.Data{
		Summary:    out.Summary,
		Tags:       out.Tags,
		KeyPoints:  out.KeyPoints,
		Transcript: out.Transcript,
	}, nil
}
```

- [ ] **Step 2: 编译确认类型与 SDK 调用正确**

Run: `go build ./internal/gemini/`
Expected: 无输出（成功）。若 SDK 方法签名有差异，按编译错误修正（参考 https://pkg.go.dev/google.golang.org/genai）。

- [ ] **Step 3: 写集成测试（真 API + 真视频，gated）**

```go
// internal/gemini/integration_test.go
//go:build integration

package gemini

import (
	"context"
	"os"
	"testing"
)

func TestAnalyzeRealVideo(t *testing.T) {
	key := os.Getenv("GEMINI_API_KEY")
	video := os.Getenv("TEST_VIDEO") // 指向一个本地 mp4
	if key == "" || video == "" {
		t.Skip("set GEMINI_API_KEY and TEST_VIDEO to run")
	}
	c, err := New(context.Background(), key, "gemini-2.5-flash")
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Analyze(context.Background(), video)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if d.Summary == "" || d.Transcript == "" || len(d.KeyPoints) == 0 {
		t.Errorf("expected populated note data, got %+v", d)
	}
}
```

- [ ] **Step 4: 跑集成测试确认 Gemini 真能出结构化笔记**

Run:
```bash
GEMINI_API_KEY=<你的key> TEST_VIDEO=/tmp/best.mp4 \
  go test ./internal/gemini/ -tags integration -run TestAnalyzeRealVideo -v
```
Expected: PASS（summary/transcript/key_points 非空）。

- [ ] **Step 5: Commit**

```bash
git add internal/gemini/
git commit -m "feat: gemini video upload and structured note generation"
```

---

## Task 6: main.go 编排 + Telegram handler

**Files:**
- Create: `main.go`

- [ ] **Step 1: 写实现（long-polling + 进度回复 + 全流程编排）**

```go
// main.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"video-to-notes/internal/config"
	"video-to-notes/internal/douyin"
	"video-to-notes/internal/gemini"
	"video-to-notes/internal/note"
)

type app struct {
	cfg config.Config
	gem *gemini.Client
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	gem, err := gemini.New(ctx, cfg.GeminiAPIKey, cfg.Model)
	if err != nil {
		log.Fatalf("gemini: %v", err)
	}
	a := &app{cfg: cfg, gem: gem}

	b, err := bot.New(cfg.TelegramToken,
		bot.WithDefaultHandler(a.handle),
		bot.WithInitialOffset(-1), // 跳过启动前的积压消息
	)
	if err != nil {
		log.Fatalf("bot: %v", err)
	}
	log.Println("video-to-notes bot started")
	b.Start(ctx)
}

func (a *app) handle(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil || update.Message.Text == "" {
		return
	}
	chatID := update.Message.Chat.ID

	status, _ := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID, Text: "⬇️ 下载视频中…",
	})
	edit := func(text string) {
		if status != nil {
			b.EditMessageText(ctx, &bot.EditMessageTextParams{
				ChatID: chatID, MessageID: status.ID, Text: text,
			})
		}
	}

	videoPath, meta, err := douyin.Download(update.Message.Text, a.cfg.TmpDir)
	if err == douyin.ErrNoURL {
		edit("没找到抖音链接，请把分享口令或链接发给我。")
		return
	}
	if err != nil {
		edit(fmt.Sprintf("❌ 下载失败：%v", err))
		return
	}
	defer os.Remove(videoPath)

	edit("🧠 Gemini 分析中…")
	data, err := a.gem.Analyze(ctx, videoPath)
	if err != nil {
		edit(fmt.Sprintf("❌ 分析失败：%v", err))
		return
	}

	edit("📝 写入笔记中…")
	relPath, err := note.Write(note.Input{
		Title:     meta.Title,
		Author:    meta.Author,
		SourceURL: meta.SourceURL,
		Date:      time.Now().Format("2006-01-02"),
		Data:      data,
	}, a.cfg.VaultPath, a.cfg.NoteSubdir)
	if err != nil {
		edit(fmt.Sprintf("❌ 写入失败：%v", err))
		return
	}

	edit(fmt.Sprintf("✅ 已生成笔记\n%s\n\n%s", data.Summary, relPath))
}
```

- [ ] **Step 2: 编译整个项目**

Run: `go build ./...`
Expected: 无输出（成功）。

- [ ] **Step 3: 全量单测**

Run: `go test ./...`
Expected: 全 PASS（集成测试默认不跑）。

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "feat: telegram long-polling orchestration with progress updates"
```

---

## Task 7: 端到端手动验证 + 常驻

**Files:**
- Create: `com.user.video-to-notes.plist`（launchd，可选）

- [ ] **Step 1: 准备 .env**

```bash
cp .env.example .env
# 编辑 .env 填入 TELEGRAM_BOT_TOKEN / GEMINI_API_KEY / VAULT_PATH（真实绝对路径）
```

- [ ] **Step 2: 跑起来手动验证**

Run:
```bash
set -a; source .env; set +a
go run .
```
然后在 Telegram 给你的 bot 发那条抖音分享口令。
Expected: bot 依次显示「下载中→分析中→写入中→✅」，vault 的 `NOTE_SUBDIR` 下出现一篇 `.md`，含 frontmatter + 三段内容；Obsidian 自动识别。

- [ ] **Step 3: 验证笔记内容质量**

打开生成的 `.md`，确认：frontmatter 的 source/author/title 正确；一句话摘要、核心要点、完整转写三段齐全且贴合视频。若转写质量不足，调 `internal/prompt/prompt.go` 或换 `GEMINI_MODEL`。

- [ ] **Step 4（可选）: launchd 常驻**

```bash
cat > ~/Library/LaunchAgents/com.user.video-to-notes.plist <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.user.video-to-notes</string>
  <key>ProgramArguments</key>
  <array><string>$(pwd)/video-to-notes</string></array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>TELEGRAM_BOT_TOKEN</key><string>填入</string>
    <key>GEMINI_API_KEY</key><string>填入</string>
    <key>VAULT_PATH</key><string>填入</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict></plist>
EOF
go build -o video-to-notes .
launchctl load ~/Library/LaunchAgents/com.user.video-to-notes.plist
```
Expected: bot 随登录自启、崩溃自拉起。

- [ ] **Step 5: Commit**

```bash
git add com.user.video-to-notes.plist
git commit -m "chore: launchd agent for always-on bot"
```

---

## Self-Review 检查记录

- **Spec 覆盖**：视频获取(Task2)/Gemini 解析(Task5)/笔记三段内容(Task3+prompt)/写 vault(Task3)/本机常驻(Task7)/错误显式暴露(Task6 各分支 edit) 全部有对应任务。✅
- **类型一致**：`note.Data{Summary,Tags,KeyPoints,Transcript}` 在 gemini/note/main 中一致；`douyin.Meta{Title,Author,SourceURL}` 一致；`note.Input` 字段与 main 调用一致。✅
- **无占位**：每个代码步骤含完整代码与确切命令/预期。SDK 方法已用 context7 核实（`Files.Upload`/`Files.Get`/`FileStateActive`/`GenerateContentConfig.ResponseSchema`/`bot.New`/`Start`/`SendMessage`/`EditMessageText`）。
- **已知风险**：genai SDK 个别方法签名以 pkg.go.dev 为准，Task5 Step2 用 `go build` 兜底；抖音解析变动只影响 `douyin` 包。
