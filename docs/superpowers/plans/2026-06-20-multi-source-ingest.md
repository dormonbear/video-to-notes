# Multi-Source Ingest (Twitter + Web) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the bot ingest Twitter single tweets (text+images+video) and generic webpage URLs, producing the same Chinese article notes as the existing Douyin pipeline.

**Architecture:** Introduce a source-dispatch layer in front of the unchanged Analyze→Write→Push backend. A new `internal/source` package classifies URLs and defines a normalized `Item`. Per-source fetchers (`douyin`, `web`, `twitter`) each return an `Item`. `llm.Analyze` is generalized from "video-only" to a `Content` payload (video / image / text). `note.Write`/`Exists`/attribution gain a `Source` field.

**Tech Stack:** Go, yt-dlp + ffmpeg (existing), `golang.org/x/net/html` (web text extraction), OpenRouter (Gemini via google-vertex), Telegram bot.

## Global Constraints

- Output unchanged: `note.Data{Title,Summary,Tags,Article}` → blog/obsidian markdown → git push. Reuse existing render/dedup/push.
- Entry points unchanged: Telegram text (`handle`) + HTTP `/ingest` (`serveAPI`), both via `enqueue`.
- Douyin behavior must not regress: blog filename `{date}-douyin-{id}.md`, source line `> 来源：[抖音 @{author}]({url})`, dedup glob `*-douyin-{id}.md` all stay identical.
- Twitter fetch uses `cfg.TelegramProxy` (the over-the-wall channel). Web fetch is direct (no proxy).
- OpenRouter request keeps: provider pinned `google-vertex` (`allow_fallbacks:false`), forced HTTP/1.1, fresh connection per request, 4-attempt retry with backoff.
- Go module path is `video-to-notes`. New packages: `video-to-notes/internal/source`, `.../internal/web`, `.../internal/twitter`.
- No new config keys. `golang.org/x/net` may be added to go.mod (run `go get golang.org/x/net/html`).

---

# Phase 1 — Skeleton refactor (Douyin stays green)

### Task 1: `internal/source` — types + URL classification

**Files:**
- Create: `internal/source/source.go`
- Test: `internal/source/source_test.go`

**Interfaces:**
- Produces:
  - `type Meta struct { Title, Author, SourceURL, ID string }`
  - `type Item struct { Kind string; Meta Meta; MediaPaths []string; MediaKind string; Text string }` (`MediaKind` ∈ `"video"|"image"|""`)
  - `type Ref struct { Kind, URL string }`
  - `func Classify(text string) []Ref` — kinds `"douyin"|"twitter"|"web"`, in-order dedup
  - `func HostOf(raw string) string` — lowercase hostname, "" on parse error

- [ ] **Step 1: Write the failing test**

```go
package source

import "testing"

func TestClassify(t *testing.T) {
	in := `看这个 https://v.douyin.com/abc/ 和 https://x.com/foo/status/123 ` +
		`还有 https://example.com/post?a=1。 重复 https://v.douyin.com/abc/ ` +
		`别误判 https://netflix.com/title`
	got := Classify(in)
	want := []Ref{
		{Kind: "douyin", URL: "https://v.douyin.com/abc/"},
		{Kind: "twitter", URL: "https://x.com/foo/status/123"},
		{Kind: "web", URL: "https://example.com/post?a=1"}, // trailing 。 trimmed
		{Kind: "web", URL: "https://netflix.com/title"},    // NOT twitter (x.com substring)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d refs %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ref %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestHostOf(t *testing.T) {
	if h := HostOf("https://Sub.X.com/a"); h != "sub.x.com" {
		t.Errorf("HostOf = %q, want sub.x.com", h)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/source/ -run TestClassify -v`
Expected: FAIL (package/identifiers undefined).

- [ ] **Step 3: Write minimal implementation**

```go
// Package source classifies input URLs and defines the normalized Item that
// every per-source fetcher produces. It depends on nothing else in the project
// (fetchers import source, never the reverse).
package source

import (
	"net/url"
	"regexp"
	"strings"
)

type Meta struct {
	Title     string
	Author    string
	SourceURL string
	ID        string
}

// Item is the normalized fetch result handed to the LLM/write backend.
type Item struct {
	Kind       string // douyin | twitter | web
	Meta       Meta
	MediaPaths []string // local media files (video/images); empty for text-only
	MediaKind  string   // "video" | "image" | ""
	Text       string   // extracted text (tweet/article body); may be empty
}

// Ref is one classified URL extracted from a message.
type Ref struct {
	Kind string
	URL  string
}

var urlRe = regexp.MustCompile(`https?://[^\s]+`)

// HostOf returns the lowercase hostname, or "" if raw is unparseable.
func HostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func kindOf(raw string) string {
	h := HostOf(raw)
	switch {
	case h == "douyin.com" || strings.HasSuffix(h, ".douyin.com"):
		return "douyin"
	case h == "twitter.com" || strings.HasSuffix(h, ".twitter.com") ||
		h == "x.com" || strings.HasSuffix(h, ".x.com"):
		return "twitter"
	default:
		return "web"
	}
}

// Classify extracts every http(s) URL from text, trims trailing punctuation,
// dedups in order, and tags each with its source kind.
func Classify(text string) []Ref {
	seen := map[string]bool{}
	var out []Ref
	for _, raw := range urlRe.FindAllString(text, -1) {
		u := strings.TrimRight(raw, ".,;:!?。，、）)]}>\"'")
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, Ref{Kind: kindOf(u), URL: u})
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/source/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/source/
git commit -m "feat: source package with URL classification"
```

---

### Task 2: `job`/`jobqueue` carry `kind`

**Files:**
- Modify: `internal/jobqueue/jobqueue.go` (add `Kind` to persisted `Job`)
- Modify: `main.go` (`job` struct, `enqueue`, `recover`)
- Modify: `http.go` (`serveAPI`)
- Test: `internal/jobqueue/jobqueue_test.go` (extend round-trip)

**Interfaces:**
- Consumes: `source.Ref`, `source.Classify` (Task 1).
- Produces:
  - `jobqueue.Job` gains field `Kind string` (json `"kind"`).
  - `main.job` gains field `kind string`.
  - `enqueue(ctx context.Context, chatID int64, ref source.Ref) error` (signature change: `u string` → `ref source.Ref`).

- [ ] **Step 1: Write the failing test** — extend persistence round-trip to assert `Kind` survives.

Add to `internal/jobqueue/jobqueue_test.go` a test that marks a job queued with `Kind: "web"`, reloads pending, and asserts the reloaded job's `Kind == "web"`. (Mirror the existing round-trip test; add the `Kind` field to the `Job` literal and the assertion.)

```go
func TestJobKindRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "q.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	j := Job{ID: "1:2", ChatID: 1, StatusID: 2, URL: "https://example.com", Kind: "web"}
	if err := s.MarkQueued(j); err != nil {
		t.Fatal(err)
	}
	pending, err := s.LoadPending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Kind != "web" {
		t.Fatalf("kind not preserved: %+v", pending)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/jobqueue/ -run TestJobKindRoundTrip -v`
Expected: FAIL (`Job` has no field `Kind`).

- [ ] **Step 3: Write minimal implementation**

In `internal/jobqueue/jobqueue.go`, add the field to the `Job` struct (keep existing json tags):

```go
type Job struct {
	ID       string `json:"id"`
	ChatID   int64  `json:"chat_id"`
	StatusID int    `json:"status_id"`
	URL      string `json:"url"`
	Kind     string `json:"kind"` // douyin | twitter | web ("" = legacy douyin)
	Status   string `json:"status"`
	// ...keep any remaining existing fields unchanged...
}
```

In `main.go`:
```go
type job struct {
	id       string
	chatID   int64
	statusID int
	url      string
	kind     string
}
```

Change `enqueue` signature and body:
```go
func (a *app) enqueue(ctx context.Context, chatID int64, ref source.Ref) error {
	pos := atomic.AddInt64(&a.queued, 1)
	text := "✅ 已加入队列，开始处理…"
	if pos > 1 {
		text = fmt.Sprintf("✅ 已加入队列（第 %d 位），排队中…", pos)
	}
	status, err := a.b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: text})
	if err != nil {
		atomic.AddInt64(&a.queued, -1)
		return err
	}
	j := job{id: fmt.Sprintf("%d:%d", chatID, status.ID), chatID: chatID, statusID: status.ID, url: ref.URL, kind: ref.Kind}
	if err := a.store.MarkQueued(jobqueue.Job{ID: j.id, ChatID: j.chatID, StatusID: j.statusID, URL: j.url, Kind: j.kind}); err != nil {
		log.Printf("jobqueue mark queued %s: %v", j.id, err)
	}
	a.jobs <- j
	return nil
}
```

In `recover`, pass kind through with a legacy default:
```go
kind := p.Kind
if kind == "" {
	kind = "douyin" // legacy queue entries predate the kind field
}
a.jobs <- job{id: p.ID, chatID: p.ChatID, statusID: p.StatusID, url: p.URL, kind: kind}
```

Update `handle` (main.go) and `serveAPI` (http.go) to use `source.Classify` and pass `source.Ref` to `enqueue` (full wiring lands in Task 5; for now just make it compile — change the `enqueue` call sites to wrap existing douyin URLs as `source.Ref{Kind:"douyin", URL:u}` if Task 5 is done later, OR do Task 5's routing change here). Simplest: do the call-site routing now since signature changed — see Task 5 Step 3 for the exact `handle`/`serveAPI` bodies and apply them here.

- [ ] **Step 4: Run tests**

Run: `go build ./... && go test ./internal/jobqueue/ -v`
Expected: build OK, PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/jobqueue/ main.go http.go
git commit -m "feat: carry source kind through job queue"
```

---

### Task 3: Generalize `llm.Analyze` to a `Content` payload

**Files:**
- Modify: `internal/llm/llm.go`
- Test: `internal/llm/content_test.go` (new — pure builder test, no network)

**Interfaces:**
- Produces:
  - `type Content struct { Prompt string; Text string; MediaKind string; MediaPaths []string }`
  - `func (c *Client) Analyze(ctx context.Context, in Content) (note.Data, error)` (was `Analyze(ctx, videoPath string)`)
  - `func buildContentParts(in Content) ([]any, error)` (unexported, testable)

- [ ] **Step 1: Write the failing test**

```go
package llm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildContentParts(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "a.png")
	// 1x1 PNG header bytes are enough for DetectContentType to say image/png.
	os.WriteFile(img, []byte("\x89PNG\r\n\x1a\n................"), 0o644)

	parts, err := buildContentParts(Content{
		Prompt:     "写文章",
		Text:       "正文素材",
		MediaKind:  "image",
		MediaPaths: []string{img},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 {
		t.Fatalf("want 2 parts (text+image), got %d", len(parts))
	}
	first := parts[0].(map[string]any)
	if first["type"] != "text" || !strings.Contains(first["text"].(string), "正文素材") {
		t.Errorf("text part wrong: %+v", first)
	}
	second := parts[1].(map[string]any)
	if second["type"] != "image_url" {
		t.Errorf("media part type = %v, want image_url", second["type"])
	}
}

func TestBuildContentPartsTextOnly(t *testing.T) {
	parts, err := buildContentParts(Content{Prompt: "p", Text: "t", MediaKind: ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 {
		t.Fatalf("text-only should yield 1 part, got %d", len(parts))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/llm/ -run TestBuildContentParts -v`
Expected: FAIL (`Content`/`buildContentParts` undefined).

- [ ] **Step 3: Write minimal implementation**

Replace the top of `Analyze` and add the builder. Add imports `net/http` (already present) for `http.DetectContentType`.

```go
// Content is the analysis payload: a prompt, optional text context, and zero or
// more local media files of a single MediaKind ("video" | "image" | "").
type Content struct {
	Prompt     string
	Text       string
	MediaKind  string
	MediaPaths []string
}

// buildContentParts turns Content into OpenAI-style content parts. Media is
// base64-inlined as a data URL (video_url for video, image_url for images).
func buildContentParts(in Content) ([]any, error) {
	text := in.Prompt
	if strings.TrimSpace(in.Text) != "" {
		text += "\n\n以下是素材内容：\n" + in.Text
	}
	parts := []any{map[string]any{"type": "text", "text": text}}
	for _, p := range in.MediaPaths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read media %s: %w", p, err)
		}
		b64 := base64.StdEncoding.EncodeToString(raw)
		switch in.MediaKind {
		case "video":
			parts = append(parts, map[string]any{"type": "video_url",
				"video_url": map[string]any{"url": "data:video/mp4;base64," + b64}})
		case "image":
			mime := http.DetectContentType(raw)
			parts = append(parts, map[string]any{"type": "image_url",
				"image_url": map[string]any{"url": "data:" + mime + ";base64," + b64}})
		default:
			return nil, fmt.Errorf("media supplied but MediaKind is %q", in.MediaKind)
		}
	}
	return parts, nil
}

// Analyze sends the content payload to the model and returns structured note data.
func (c *Client) Analyze(ctx context.Context, in Content) (note.Data, error) {
	parts, err := buildContentParts(in)
	if err != nil {
		return note.Data{}, err
	}
	reqBody := map[string]any{
		"model": c.model,
		"messages": []any{
			map[string]any{"role": "user", "content": parts},
		},
		"response_format": noteSchema(),
		"provider":        map[string]any{"order": []string{"google-vertex"}, "allow_fallbacks": false},
	}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return note.Data{}, fmt.Errorf("marshal request: %w", err)
	}
	// ...keep the existing 4-attempt retry loop calling c.exchange(ctx, buf)...
}
```

Add `"strings"` to the import block. Remove the now-unused `videoPath`-reading code (the old `os.ReadFile(videoPath)` + dataURL lines at the top of the old `Analyze`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/llm/ -run TestBuildContentParts -v`
Expected: PASS. (Callers won't compile yet — fixed in Tasks 4–5; `go build ./...` will fail until then, that's expected mid-phase.)

- [ ] **Step 5: Commit**

```bash
git add internal/llm/llm.go internal/llm/content_test.go
git commit -m "feat: generalize llm.Analyze to Content payload"
```

---

### Task 4: `note` — `Source` field for filename, dedup, attribution

**Files:**
- Modify: `internal/note/note.go`
- Test: `internal/note/note_test.go`, `internal/note/exists_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces:
  - `note.Input` gains `Source string` (e.g. `"douyin"|"twitter"|"web"`).
  - `note.Write` blog filename becomes `{date}-{Source}-{VideoID}.md`.
  - `func Exists(vaultPath, subdir, source, videoID string) bool` (signature change: adds `source`).
  - `renderBlog` source line varies by `in.Source`.

- [ ] **Step 1: Write the failing test**

```go
func TestRenderBlogSourceLine(t *testing.T) {
	cases := []struct {
		source, author, title, url, want string
	}{
		{"douyin", "张三", "", "https://v.douyin.com/x", "> 来源：[抖音 @张三](https://v.douyin.com/x)"},
		{"twitter", "jack", "", "https://x.com/jack/status/1", "> 来源：[X @jack](https://x.com/jack/status/1)"},
		{"web", "", "某篇文章", "https://example.com/p", "> 来源：[某篇文章](https://example.com/p)"},
		{"web", "", "", "https://example.com/p", "> 来源：[example.com](https://example.com/p)"},
	}
	for _, c := range cases {
		out := renderBlog(Input{
			Source: c.source, Author: c.author, Title: c.title, SourceURL: c.url,
			Date: "2026-06-20T00:00:00Z",
			Data: Data{Title: "T", Summary: "S", Tags: []string{"a"}, Article: "body"},
		}, Options{Tag: "video-note"})
		if !strings.Contains(out, c.want) {
			t.Errorf("source=%s: missing %q in:\n%s", c.source, c.want, out)
		}
	}
}

func TestExistsBySource(t *testing.T) {
	dir := t.TempDir()
	sub := "posts"
	os.MkdirAll(filepath.Join(dir, sub), 0o755)
	os.WriteFile(filepath.Join(dir, sub, "2026-06-20-web-abc123.md"), []byte("x"), 0o644)
	if !Exists(dir, sub, "web", "abc123") {
		t.Error("web/abc123 should exist")
	}
	if Exists(dir, sub, "twitter", "abc123") {
		t.Error("twitter/abc123 should NOT match a web file")
	}
}
```

Also update the existing Douyin filename/Exists tests in `note_test.go`/`exists_test.go` to pass `Source: "douyin"` / the new `Exists(..., "douyin", id)` signature and assert filename `2026-06-20-douyin-{id}.md` (unchanged output).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/note/ -run 'SourceLine|BySource' -v`
Expected: FAIL (no `Source` field / `Exists` arity).

- [ ] **Step 3: Write minimal implementation**

Add to `Input`:
```go
type Input struct {
	Source    string // douyin | twitter | web (blog filename + source line)
	Title     string
	Author    string
	SourceURL string
	VideoID   string // generic id: video id / tweet id / url hash
	Date      string
	Data      Data
}
```

Replace the hardcoded source line in `renderBlog` (the `fmt.Fprintf(&b, "> 来源：...")` line) with:
```go
b.WriteString(sourceLine(in))
```
and add:
```go
func sourceLine(in Input) string {
	switch in.Source {
	case "twitter":
		return fmt.Sprintf("> 来源：[X @%s](%s)\n\n", in.Author, in.SourceURL)
	case "web":
		label := strings.TrimSpace(in.Title)
		if label == "" {
			if u, err := url.Parse(in.SourceURL); err == nil {
				label = u.Hostname()
			}
		}
		return fmt.Sprintf("> 来源：[%s](%s)\n\n", label, in.SourceURL)
	default: // douyin
		return fmt.Sprintf("> 来源：[抖音 @%s](%s)\n\n", in.Author, in.SourceURL)
	}
}
```
Add `"net/url"` to imports.

In `Write`, change the blog filename line:
```go
name = fmt.Sprintf("%s-%s-%s.md", date, in.Source, in.VideoID)
```

Change `Exists`:
```go
func Exists(vaultPath, subdir, source, videoID string) bool {
	if videoID == "" {
		return false
	}
	matches, _ := filepath.Glob(filepath.Join(vaultPath, subdir, "*-"+source+"-"+videoID+".md"))
	return len(matches) > 0
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/note/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/note/
git commit -m "feat: source-aware note filename, dedup, attribution"
```

---

### Task 5: Wire routing + dispatch in `main`/`http`; wrap Douyin as `Item`

**Files:**
- Modify: `main.go` (`handle`, `process`, `selfTest`, add `fetch`/`promptFor`/`cleanup`)
- Modify: `http.go` (`serveAPI`)
- Modify: `internal/douyin/douyin.go` (add `FetchItem`, export `Transcode`)
- Modify: `internal/llm/integration_test.go` (use `Content`)
- Test: covered by `go build ./...` + existing douyin integration test (build-tagged)

**Interfaces:**
- Consumes: `source.Classify`, `source.Item` (Task 1); `llm.Content`/`Analyze` (Task 3); `note.Input.Source`/`Exists` (Task 4); `jobqueue`/`job.kind` (Task 2).
- Produces:
  - `func (d) douyin.FetchItem(shareText, destDir string) (source.Item, error)`
  - `func douyin.Transcode(src, dst string) error` (rename of unexported `transcode`)
  - `func (a *app) fetch(ctx context.Context, j job) (source.Item, error)`
  - `func promptFor(mediaKind string) string`

- [ ] **Step 1: Write the failing test**

Add to `internal/douyin/douyin_test.go`:
```go
func TestFetchItemNoURL(t *testing.T) {
	_, err := FetchItem("no link here", t.TempDir())
	if err == nil {
		t.Fatal("want error when no douyin url present")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/douyin/ -run TestFetchItemNoURL -v`
Expected: FAIL (`FetchItem` undefined).

- [ ] **Step 3: Write minimal implementation**

In `internal/douyin/douyin.go`: rename `transcode` → `Transcode` (update its internal call site in `Fetch`), and add:
```go
// FetchItem runs Fetch and wraps the result as a source.Item (video media kind).
func FetchItem(shareText, destDir string) (source.Item, error) {
	path, m, err := Fetch(shareText, destDir)
	if err != nil {
		return source.Item{}, err
	}
	return source.Item{
		Kind:       "douyin",
		Meta:       source.Meta{Title: m.Title, Author: m.Author, SourceURL: m.SourceURL, ID: m.ID},
		MediaPaths: []string{path},
		MediaKind:  "video",
	}, nil
}
```
Add import `"video-to-notes/internal/source"`.

In `main.go`, add dispatch + helpers:
```go
func (a *app) fetch(ctx context.Context, j job) (source.Item, error) {
	switch j.kind {
	case "twitter":
		return twitter.Fetch(j.url, a.cfg.TelegramProxy, a.cfg.TmpDir) // Phase 3
	case "web":
		return web.Fetch(j.url, a.cfg.TmpDir) // Phase 2
	default:
		return douyin.FetchItem(j.url, a.cfg.TmpDir)
	}
}

func promptFor(mediaKind string) string {
	if mediaKind == "video" {
		return prompt.VideoNote
	}
	return prompt.TextNote // added in Phase 2
}

func cleanup(paths []string) {
	for _, p := range paths {
		os.Remove(p)
	}
}
```
> NOTE: `twitter` and `web` imports + `prompt.TextNote` don't exist until Phases 2–3. To keep Phase 1 compiling, temporarily make `fetch` handle only the `default` (douyin) branch and `promptFor` return `prompt.VideoNote` unconditionally; replace with the full versions above in Tasks 7 and 9.

Rewrite `process` body (download/dedup/analyze/write section) to use the Item:
```go
edit("⬇️ 抓取中…")
item, err := a.fetch(ctx, j)
if err != nil {
	edit(fmt.Sprintf("❌ 抓取失败：%v", err))
	return err
}
defer cleanup(item.MediaPaths)

if a.cfg.NoteFormat == "blog" && note.Exists(a.cfg.VaultPath, a.cfg.NoteSubdir, item.Kind, item.Meta.ID) {
	edit("ℹ️ 该内容已发布过，跳过")
	return nil
}

edit("🧠 Gemini 分析中…")
data, err := a.gem.Analyze(ctx, llm.Content{
	Prompt: promptFor(item.MediaKind), Text: item.Text,
	MediaKind: item.MediaKind, MediaPaths: item.MediaPaths,
})
if err != nil {
	edit(fmt.Sprintf("❌ 分析失败：%v", err))
	return err
}

edit("📝 写入笔记中…")
date := time.Now().Format("2006-01-02")
if a.cfg.NoteFormat == "blog" {
	date = time.Now().Format(time.RFC3339)
}
relPath, err := note.Write(note.Input{
	Source: item.Kind, Title: item.Meta.Title, Author: item.Meta.Author,
	SourceURL: item.Meta.SourceURL, VideoID: item.Meta.ID, Date: date, Data: data,
}, note.Options{Format: a.cfg.NoteFormat, Draft: a.cfg.BlogDraft, Tag: a.cfg.BlogTag},
	a.cfg.VaultPath, a.cfg.NoteSubdir)
// ...keep the rest (write error handling, gitsync, link, final edit) unchanged...
```

Rewrite `selfTest` to use the source path so it works for any URL:
```go
func (a *app) selfTest(ctx context.Context, shareText, date string) {
	refs := source.Classify(shareText)
	if len(refs) == 0 {
		log.Fatalf("no supported URL in input")
	}
	j := job{url: refs[0].URL, kind: refs[0].Kind}
	item, err := a.fetch(ctx, j)
	if err != nil {
		log.Fatalf("fetch failed: %v", err)
	}
	defer cleanup(item.MediaPaths)
	data, err := a.gem.Analyze(ctx, llm.Content{
		Prompt: promptFor(item.MediaKind), Text: item.Text,
		MediaKind: item.MediaKind, MediaPaths: item.MediaPaths,
	})
	if err != nil {
		log.Fatalf("analyze failed: %v", err)
	}
	if date == "" {
		date = time.Now().Format(time.RFC3339)
	}
	relPath, err := note.Write(note.Input{
		Source: item.Kind, Title: item.Meta.Title, Author: item.Meta.Author,
		SourceURL: item.Meta.SourceURL, VideoID: item.Meta.ID, Date: date, Data: data,
	}, note.Options{Format: a.cfg.NoteFormat, Draft: a.cfg.BlogDraft, Tag: a.cfg.BlogTag},
		a.cfg.VaultPath, a.cfg.NoteSubdir)
	if err != nil {
		log.Fatalf("write failed: %v", err)
	}
	fmt.Printf("\n===== SELFTEST OK =====\ntitle: %s\nfile: %s\n--- article (first 600 runes) ---\n%s\n",
		data.Title, relPath, firstRunes(data.Article, 600))
}
```

Update `handle` (main.go) and the `/ingest` handler (http.go) to classify and enqueue refs:
```go
// handle
refs := source.Classify(update.Message.Text)
if len(refs) == 0 {
	b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "没找到可处理的链接，请发抖音/Twitter/网页链接。"})
	return
}
for _, ref := range refs {
	if err := a.enqueue(ctx, chatID, ref); err != nil {
		log.Printf("send queue ack to chat %d: %v", chatID, err)
	}
}
```
```go
// serveAPI /ingest (replace douyin.ExtractURLs block)
refs := source.Classify(text)
if len(refs) == 0 {
	log.Printf("ingest: no link in %d-char input: %.120q", len(text), text)
	http.Error(w, "no supported link found", http.StatusBadRequest)
	return
}
n := 0
for _, ref := range refs {
	if err := a.enqueue(ctx, a.cfg.NotifyChatID, ref); err != nil {
		log.Printf("api enqueue: %v", err)
		continue
	}
	n++
}
fmt.Fprintf(w, "✅ 已加入队列（%d 个链接），进度看 Telegram", n)
```
Update `http.go` imports: drop `"video-to-notes/internal/douyin"`, add `"video-to-notes/internal/source"`.

Update `internal/llm/integration_test.go` to call `Analyze(ctx, Content{MediaKind:"video", MediaPaths:[]string{path}, Prompt: prompt.VideoNote})` instead of the old `Analyze(ctx, path)`.

- [ ] **Step 4: Run tests**

Run: `go build ./... && go test ./... -short`
Expected: build OK; all non-integration tests PASS. Douyin output (filename/source line/dedup) unchanged.

- [ ] **Step 5: Commit**

```bash
git add main.go http.go internal/douyin/ internal/llm/
git commit -m "feat: route by source kind, dispatch fetch, wrap douyin as Item"
```

---

# Phase 2 — Web source

### Task 6: `internal/web` — fetch + extract article text

**Files:**
- Create: `internal/web/web.go`
- Test: `internal/web/web_test.go`
- Modify: `go.mod`/`go.sum` (`go get golang.org/x/net/html`)

**Interfaces:**
- Consumes: `source.Item`/`source.Meta` (Task 1).
- Produces:
  - `func Fetch(rawURL, tmpDir string) (source.Item, error)`
  - `func extract(htmlBytes []byte) (title, text string)` (unexported, testable)
  - `func urlID(rawURL string) string` (sha1 hex, first 12 chars)

- [ ] **Step 1: Write the failing test**

```go
package web

import "testing"

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
		if !contains(text, want) {
			t.Errorf("text missing %q; got %q", want, text)
		}
	}
	for _, bad := range []string{"var x=1", ".a{}", "ns"} {
		if contains(text, bad) {
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

func contains(s, sub string) bool { return len(s) >= len(sub) && (indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int   { return stringsIndex(s, sub) }
```
> Replace the `contains` helper with `strings.Contains` and import `strings` — the inline helpers above are placeholders to keep the test self-contained; prefer `strings.Contains` directly.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/ -v`
Expected: FAIL (package undefined).

- [ ] **Step 3: Write minimal implementation**

```bash
go get golang.org/x/net/html
```

```go
// Package web fetches a webpage and extracts its readable text for the LLM.
// ponytail: lightweight tag-stripping, not a full readability algorithm; the LLM
// distills the article from noisy text. Upgrade to go-readability if quality lags.
package web

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"

	"video-to-notes/internal/source"
)

const (
	maxBody = 4 << 20 // 4 MB cap on the fetched HTML
	maxText = 40000   // rune cap on extracted text (token control)
	minText = 200     // below this we treat the page as un-fetchable (JS/paywall)
	ua      = "Mozilla/5.0 (compatible; video-to-notes/1.0)"
)

func urlID(rawURL string) string {
	sum := sha1.Sum([]byte(rawURL))
	return hex.EncodeToString(sum[:])[:12]
}

func extract(htmlBytes []byte) (title, text string) {
	doc, err := html.Parse(strings.NewReader(string(htmlBytes)))
	if err != nil {
		return "", ""
	}
	var b strings.Builder
	skip := map[string]bool{"script": true, "style": true, "noscript": true, "head": true}
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inHead bool) {
		if n.Type == html.ElementNode && n.Data == "title" && n.FirstChild != nil {
			title = strings.TrimSpace(n.FirstChild.Data)
		}
		if n.Type == html.ElementNode && skip[n.Data] {
			return
		}
		if n.Type == html.TextNode {
			if t := strings.TrimSpace(n.Data); t != "" {
				b.WriteString(t)
				b.WriteByte('\n')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inHead)
		}
	}
	walk(doc, false)
	text = strings.Join(strings.Fields(strings.ReplaceAll(b.String(), "\n", " \n ")), " ")
	// collapse but keep paragraph breaks
	text = strings.TrimSpace(b.String())
	if r := []rune(text); len(r) > maxText {
		text = string(r[:maxText])
	}
	return title, text
}

// Fetch downloads rawURL (direct, no proxy) and returns a text-only Item.
func Fetch(rawURL, _ string) (source.Item, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return source.Item{}, err
	}
	req.Header.Set("User-Agent", ua)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return source.Item{}, fmt.Errorf("web get: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return source.Item{}, fmt.Errorf("web HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return source.Item{}, fmt.Errorf("web read: %w", err)
	}
	title, text := extract(body)
	if len([]rune(text)) < minText {
		return source.Item{}, fmt.Errorf("extracted too little text (%d runes); page may need JS/login", len([]rune(text)))
	}
	return source.Item{
		Kind:      "web",
		Meta:      source.Meta{Title: title, SourceURL: rawURL, ID: urlID(rawURL)},
		MediaKind: "",
		Text:      text,
	}, nil
}
```
> In the test, use `strings.Contains` instead of the placeholder helpers.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/web/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/web/ go.mod go.sum
git commit -m "feat: web source fetch + text extraction"
```

---

### Task 7: `prompt.TextNote` + wire web into dispatch

**Files:**
- Modify: `internal/prompt/prompt.go`
- Modify: `main.go` (`fetch` web branch, `promptFor` real version)
- Test: `go build ./...` + manual `selftest` against a real article URL

**Interfaces:**
- Consumes: `web.Fetch` (Task 6).
- Produces: `prompt.TextNote string`.

- [ ] **Step 1: Add the prompt**

```go
// TextNote instructs the model to turn supplied text (a webpage article or a
// tweet) into a standalone Chinese article, same 4-field schema as VideoNote.
const TextNote = `你是一个资深内容编辑。下面会给你一段素材文本（网页正文或社交媒体帖子），请认真阅读后用中文输出：
1. title：不超过 20 字、能概括主题的标题（用作博客标题，不含特殊符号）。
2. summary：一句话概括全文主旨（用作摘要/description）。
3. tags：3-6 个主题标签（不带 # 号，简短名词）。
4. article：一篇结构化、可独立阅读的中文文章正文（markdown 格式）。要求：
   - 这是二次创作整理，不是原文复制——重新组织语言、提炼逻辑、去掉网页噪音（导航/广告/无关片段）。
   - 用 ## 二级标题分节，可用列表、代码块、引用等 markdown 元素。
   - 不要出现"原文说""这篇文章里"这类表述，直接以文章口吻陈述。
   - 不要把 title 作为一级标题重复，正文直接从内容开始。
严格按要求的 JSON schema 输出，不要输出多余文字。`
```

- [ ] **Step 2: Wire web into `main`**

Replace the temporary `fetch`/`promptFor` from Task 5 with the full versions: add the `web` import and the `case "web": return web.Fetch(j.url, a.cfg.TmpDir)` branch; make `promptFor` return `prompt.TextNote` for non-video.

- [ ] **Step 3: Build + verify**

Run: `go build ./... && go vet ./...`
Expected: OK.

- [ ] **Step 4: Manual integration check** (real network; run on a machine with outbound access)

Run: `go run . selftest "https://<some-article-url>"`
Expected: `===== SELFTEST OK =====` with a sensible title and article; a `{date}-web-{hash}.md` written under the vault.

- [ ] **Step 5: Commit**

```bash
git add internal/prompt/prompt.go main.go
git commit -m "feat: wire web source with TextNote prompt"
```

---

# Phase 3 — Twitter source

### Task 8: `internal/twitter` — URL parsing + syndication parsing

**Files:**
- Create: `internal/twitter/twitter.go` (parsing helpers only this task)
- Test: `internal/twitter/twitter_test.go`

**Interfaces:**
- Produces:
  - `func tweetID(rawURL string) (string, error)` — extracts the `/status/{id}` digits
  - `func synToken(id string) string` — react-tweet syndication token
  - `func parseSyndication(jsonBytes []byte) (text string, images []string, author string, err error)`

- [ ] **Step 1: Write the failing test**

```go
package twitter

import "testing"

func TestTweetID(t *testing.T) {
	cases := map[string]string{
		"https://x.com/jack/status/1234567890123456789":          "1234567890123456789",
		"https://twitter.com/foo/status/42?s=20":                 "42",
		"https://mobile.twitter.com/a/status/99/photo/1":         "99",
	}
	for in, want := range cases {
		got, err := tweetID(in)
		if err != nil || got != want {
			t.Errorf("tweetID(%q) = %q,%v want %q", in, got, err, want)
		}
	}
	if _, err := tweetID("https://x.com/jack"); err == nil {
		t.Error("want error when no /status/ segment")
	}
}

func TestParseSyndication(t *testing.T) {
	js := []byte(`{"text":"hello world","user":{"screen_name":"jack"},
		"mediaDetails":[{"type":"photo","media_url_https":"https://pbs.twimg.com/a.jpg"},
		{"type":"video","media_url_https":"https://pbs.twimg.com/poster.jpg"}]}`)
	text, imgs, author, err := parseSyndication(js)
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello world" || author != "jack" {
		t.Errorf("text/author = %q/%q", text, author)
	}
	if len(imgs) != 1 || imgs[0] != "https://pbs.twimg.com/a.jpg" {
		t.Errorf("images = %v (want only the photo)", imgs)
	}
}

func TestSynTokenStable(t *testing.T) {
	if synToken("1234567890123456789") == "" {
		t.Error("token should be non-empty")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/twitter/ -v`
Expected: FAIL (package undefined).

- [ ] **Step 3: Write minimal implementation**

```go
// Package twitter fetches a single tweet: yt-dlp first (video tweets), falling
// back to the public syndication endpoint (text + image tweets). All network
// goes through the supplied proxy (X is blocked on the bot's egress).
package twitter

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var idRe = regexp.MustCompile(`/status/(\d+)`)

func tweetID(rawURL string) (string, error) {
	m := idRe.FindStringSubmatch(rawURL)
	if m == nil {
		return "", fmt.Errorf("no /status/{id} in %q", rawURL)
	}
	return m[1], nil
}

// synToken mirrors react-tweet: ((id/1e15)*PI) in base36, with '0' and '.' stripped.
func synToken(id string) string {
	n, _ := strconv.ParseFloat(id, 64)
	v := (n / 1e15) * math.Pi
	s := base36(v)
	s = strings.ReplaceAll(s, "0", "")
	s = strings.ReplaceAll(s, ".", "")
	return s
}

func base36(f float64) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	i := int64(f)
	frac := f - float64(i)
	var ip string
	if i == 0 {
		ip = "0"
	}
	for i > 0 {
		ip = string(digits[i%36]) + ip
		i /= 36
	}
	var fp strings.Builder
	for k := 0; k < 16 && frac > 0; k++ {
		frac *= 36
		d := int(frac)
		fp.WriteByte(digits[d])
		frac -= float64(d)
	}
	return ip + "." + fp.String()
}

func parseSyndication(jsonBytes []byte) (text string, images []string, author string, err error) {
	var t struct {
		Text string `json:"text"`
		User struct {
			ScreenName string `json:"screen_name"`
		} `json:"user"`
		MediaDetails []struct {
			Type         string `json:"type"`
			MediaURLHTTPS string `json:"media_url_https"`
		} `json:"mediaDetails"`
	}
	if err = json.Unmarshal(jsonBytes, &t); err != nil {
		return "", nil, "", fmt.Errorf("parse syndication json: %w", err)
	}
	for _, m := range t.MediaDetails {
		if m.Type == "photo" && m.MediaURLHTTPS != "" {
			images = append(images, m.MediaURLHTTPS)
		}
	}
	return t.Text, images, t.User.ScreenName, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/twitter/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/twitter/
git commit -m "feat: twitter url + syndication parsing"
```

---

### Task 9: `twitter.Fetch` (yt-dlp + syndication fallback) + wire into dispatch

**Files:**
- Modify: `internal/twitter/twitter.go` (add `Fetch`, image download, yt-dlp call)
- Create: `internal/twitter/integration_test.go` (build tag `integration`, real network)
- Modify: `main.go` (`fetch` twitter branch)

**Interfaces:**
- Consumes: `tweetID`, `synToken`, `parseSyndication` (Task 8); `douyin.Transcode` (Task 5); `source.Item`.
- Produces: `func Fetch(rawURL, proxy, tmpDir string) (source.Item, error)`.

- [ ] **Step 1: Write the failing (integration) test**

```go
//go:build integration

package twitter

import "testing"

// Run: go test -tags integration ./internal/twitter/ -run TestFetchLive -v
// Requires PROXY env pointing at the over-the-wall channel.
func TestFetchLive(t *testing.T) {
	proxy := "http://127.0.0.1:7897" // adjust to your TelegramProxy
	item, err := Fetch("https://x.com/Interior/status/463440424141459456", proxy, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if item.Meta.ID == "" || (item.Text == "" && len(item.MediaPaths) == 0) {
		t.Fatalf("empty item: %+v", item)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/twitter/ -run TestFetchLive -v`
Expected: FAIL (`Fetch` undefined).

- [ ] **Step 3: Write minimal implementation**

Add to `internal/twitter/twitter.go` (new imports: `bytes`, `net/http`, `net/url`, `os`, `os/exec`, `path/filepath`, `time`, `io`, and `"video-to-notes/internal/douyin"`, `"video-to-notes/internal/source"`):

```go
const maxInlineBytes = 18 << 20

// Fetch tries yt-dlp (video tweets) then the syndication endpoint (text+image
// tweets). All HTTP/yt-dlp traffic uses proxy.
func Fetch(rawURL, proxy, tmpDir string) (source.Item, error) {
	id, err := tweetID(rawURL)
	if err != nil {
		return source.Item{}, err
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return source.Item{}, err
	}
	meta := source.Meta{SourceURL: rawURL, ID: id}

	// 1) yt-dlp — succeeds only when the tweet has video.
	if item, ok := fetchVideo(rawURL, proxy, tmpDir, meta); ok {
		return item, nil
	}

	// 2) syndication — text + images.
	text, images, author, err := fetchSyndication(id, proxy)
	if err != nil {
		return source.Item{}, fmt.Errorf("twitter fallback: %w", err)
	}
	meta.Author = author
	meta.Title = firstLine(text)
	var paths []string
	for i, u := range images {
		p := filepath.Join(tmpDir, fmt.Sprintf("%s-%d.jpg", id, i))
		if err := downloadFile(u, p, proxy); err == nil {
			paths = append(paths, p)
		}
	}
	if text == "" && len(paths) == 0 {
		return source.Item{}, fmt.Errorf("tweet %s has no text or media", id)
	}
	kind := ""
	if len(paths) > 0 {
		kind = "image"
	}
	return source.Item{Kind: "twitter", Meta: meta, MediaPaths: paths, MediaKind: kind, Text: text}, nil
}

func fetchVideo(rawURL, proxy, tmpDir string, meta source.Meta) (source.Item, bool) {
	outTmpl := filepath.Join(tmpDir, "%(id)s.%(ext)s")
	var stdout bytes.Buffer
	args := []string{"--no-playlist", "--no-progress", "-S", "+size", "--print-json", "-o", outTmpl}
	if proxy != "" {
		args = append(args, "--proxy", proxy)
	}
	args = append(args, rawURL)
	cmd := exec.Command("yt-dlp", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return source.Item{}, false // no video / yt-dlp failed → caller falls back
	}
	var info struct {
		ID, Ext, Title, Uploader, UploaderID, WebpageURL string
	}
	if json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &info) != nil {
		return source.Item{}, false
	}
	src := filepath.Join(tmpDir, info.ID+"."+info.Ext)
	defer os.Remove(src)
	dst := filepath.Join(tmpDir, info.ID+".small.mp4")
	if err := douyin.Transcode(src, dst); err != nil {
		return source.Item{}, false
	}
	meta.Author = info.UploaderID
	meta.Title = info.Title
	return source.Item{Kind: "twitter", Meta: meta, MediaPaths: []string{dst}, MediaKind: "video"}, true
}

func fetchSyndication(id, proxy string) (text string, images []string, author string, err error) {
	endpoint := fmt.Sprintf("https://cdn.syndication.twimg.com/tweet-result?id=%s&token=%s&lang=en", id, synToken(id))
	body, err := httpGet(endpoint, proxy)
	if err != nil {
		return "", nil, "", err
	}
	return parseSyndication(body)
}

func proxyClient(proxy string) *http.Client {
	tr := &http.Transport{}
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: tr}
}

func httpGet(u, proxy string) ([]byte, error) {
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := proxyClient(proxy).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func downloadFile(u, dst, proxy string) error {
	b, err := httpGet(u, proxy)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(strings.TrimSpace(s)); len(r) > 30 {
		return string(r[:30])
	}
	return strings.TrimSpace(s)
}
```
> `maxInlineBytes` is referenced for parity with douyin; `douyin.Transcode` already enforces the size ceiling, so it is informational here (remove if `go vet` flags it unused).

- [ ] **Step 4: Run tests**

Run: `go test ./internal/twitter/ -v` (unit), then if outbound access + proxy available: `go test -tags integration ./internal/twitter/ -run TestFetchLive -v`
Expected: unit PASS; integration PASS (verifies live syndication token + endpoint).

- [ ] **Step 5: Wire into `main` + commit**

In `main.go` `fetch`, ensure the `case "twitter": return twitter.Fetch(j.url, a.cfg.TelegramProxy, a.cfg.TmpDir)` branch is active and `twitter` is imported.

Run: `go build ./... && go test ./... -short`
```bash
git add internal/twitter/ main.go
git commit -m "feat: twitter fetch with yt-dlp + syndication fallback"
```

---

## Final verification

- [ ] `go build ./... && go vet ./... && go test ./... -short` all green.
- [ ] Douyin regression: a douyin link still writes `{date}-douyin-{id}.md` with `> 来源：[抖音 @{author}]` and dedups.
- [ ] `selftest` works for a web URL and (with proxy) a tweet URL.
- [ ] Update `docs/ios-shortcut.md` if the "no link found" copy or accepted inputs changed (now accepts twitter/web links too).
