# Multi-Source Ingest (Twitter + Web) — Design

Date: 2026-06-20
Status: Direction confirmed with the user; authorized to land the spec + plan and implement in phases.

## Background & Goal

The video-to-notes bot currently handles only Douyin links:
`douyin.ExtractURLs → douyin.Fetch (yt-dlp+ffmpeg) → llm.Analyze (video) → note.Write (blog/obsidian) → gitsync.Push`.
The back half (Analyze→Write→Push) is actually source-agnostic; only the front half ("get content +
shape it for the LLM") is hardcoded to Douyin.

The user wants two new sources besides Douyin, with **identical output** (Chinese article note →
blog/obsidian → git publish) and **unchanged entry points** (Telegram text message + HTTP `/ingest`):

1. **Twitter single tweet** — text + images + video all included in analysis.
2. **Web URL** — extract article text only.

### Explicitly out of scope

- **Screenshots**: skipped this round (user decision).
- **Blog DB migration**: investigated. dormon.net is Astro 6 fully static (SSG, Content Layer `glob()`
  reading `src/content/posts/`). Moving to a DB requires converting the whole site to SSR + an adapter +
  a self-hosted server/database + moving shiki/MDX/OG rendering from build time to request time
  (≈ forking the theme). The cost is wildly disproportionate to the benefit ("a post goes live 1–3 min
  later"). Conclusion: **keep static**. If rebuilds feel slow, first measure the actual time, then
  consider build optimization or ISR — none of which is part of this project.

## Current facts (verified)

- Both entry points share `enqueue`: `handle` (Telegram text) and `serveAPI` (HTTP `/ingest`, for the
  iOS Shortcut). Both currently recognize only `douyin.ExtractURLs`.
- `job{ id, chatID, statusID, url }`; the worker consumes serially; `jobqueue` persists to disk and
  recovers on restart.
- `llm.Analyze(ctx, videoPath)`: reads video → base64 data URL → `video_url` content part → provider
  pinned to `google-vertex` (base64 video is only accepted by Vertex) → forced HTTP/1.1 + a fresh
  connection per request + 4 retries (flaky proxy egress) → json_schema producing
  `note.Data{Title,Summary,Tags,Article}`.
- `note.Write`: blog filename `{date}-douyin-{videoID}.md`; the body's top line is hardcoded
  `> 来源：[抖音 @author](url)`. `note.Exists` dedups via the `*-douyin-{id}.md` glob.
- Proxies: OpenRouter uses `cfg.Proxy`; Telegram uses `cfg.TelegramProxy` (the over-the-wall channel).

## Architecture: source dispatch

The back half (Analyze→Write→Push) stays unchanged; a source abstraction is introduced in the front half.

### 1. Routing layer

Classify URLs and turn each URL in a message / ingest call into its own job (a single message may mix
multiple URL kinds):

- contains `douyin.com` → `kind = "douyin"`
- contains `twitter.com` or `x.com` → `kind = "twitter"`
- any other `http(s)://` → `kind = "web"`

Implementation: a new `internal/source` package exposing `Classify(text string) []Ref`, where
`Ref{ Kind string; URL string }` (in-order dedup). The dedup logic from `douyin.ExtractURLs` is reused here.

The `job` struct gains a `kind` field; `jobqueue.Job` gains `Kind` (persistence + recovery must carry the
source). `enqueue(ctx, chatID, ref)` takes a `Ref` instead of a bare url.

> Compatibility: old queue entries have no `kind` field → deserialize to "" → default to `"douyin"` on recovery.

### 2. Fetchers (one per source, producing a normalized intermediate)

```go
// internal/source
type Item struct {
    Kind       string   // douyin | twitter | web
    Meta       Meta     // Title, Author, SourceURL, ID
    MediaPaths []string // ready local media files (video/images); empty for text-only
    MediaKind  string   // "video" | "image" | "" (chooses the LLM content part type)
    Text       string   // extracted text (tweet / article body); may be empty
}
```

- **douyin**: reuse the existing `douyin.Fetch`, wrapped into `Item{Kind:"douyin", MediaPaths:[small.mp4], MediaKind:"video"}`.
- **twitter** (`internal/twitter`):
  1. yt-dlp first (via `cfg.TelegramProxy`, `--proxy`). Got video → reuse douyin's transcode →
     `Item{MediaKind:"video", Text: tweet text (yt-dlp description)}`.
  2. yt-dlp has no video format / fails → fall back to syndication:
     `GET https://cdn.syndication.twimg.com/tweet-result?id={id}&token={t}&lang=en` (via `TelegramProxy`;
     `token` is the endpoint's public computed value) → parse text + image direct URLs → download images →
     `Item{MediaKind:"image", MediaPaths:[imgs...], Text: tweet text}`.
  3. Tweet id parsed from the URL path `/status/{id}`; `Meta.ID = id`, `Meta.Author = author handle`,
     `Meta.SourceURL = canonical tweet URL`.
- **web** (`internal/web`):
  1. HTTP GET (direct, no proxy; common UA, capped body size + timeout).
  2. `golang.org/x/net/html` parse → drop `script`/`style`/`noscript` → collect visible text nodes →
     collapse whitespace → truncate to a sane cap (token control). `<title>` becomes `Meta.Title`.
     ponytail: avoid pulling in a heavy readability library; the LLM distills the article from noisy text;
     upgrade to go-readability if quality lags.
  3. `Meta.ID = hash(normalized URL)` (short hex, e.g. first 12 of sha1), `Meta.Author = ""`,
     `Meta.SourceURL = original URL`. `Item{MediaKind:"", Text: body}`.

Each fetcher cleans up its own downloaded temp files (keep the `defer os.Remove` pattern).

### 3. Generalize llm.Analyze

`Analyze` changes from "video path only" to a content payload:

```go
type Content struct {
    Prompt     string
    Text       string      // appended to the prompt as context (tweet / article body)
    MediaKind  string      // "video" | "image" | ""
    MediaPaths []string    // base64-inlined local files
}
func (c *Client) Analyze(ctx context.Context, in Content) (note.Data, error)
```

- Build content parts: a text part (prompt + optional `Text`) + one part per media file
  (video→`video_url`, image→`image_url`, both base64 data URLs).
- The `google-vertex` provider pin + HTTP/1.1 + fresh connection + 4 retries stay unchanged (harmless for
  text-only requests).
- json_schema, parsing, and `exchange` logic unchanged.
- Existing callers (`main.process`, `selfTest`, integration test) switch to building `Content`.

### 4. Prompts

- Keep the video prompt (`prompt.VideoNote`).
- Add `prompt.TextNote` (web / text-only tweet): rewrite the given text into a Chinese article, same
  4-field schema as VideoNote, dropping "screen/voiceover" wording.
- The image+text case (tweet with images) reuses TextNote ("combine the following text and images").
- Selection rule: `MediaKind == "video"` → VideoNote; otherwise → TextNote.

> Note: the generated articles stay **Chinese** — that is the product's content language. Only the prompt
> wording is in English; it still instructs the model to output in Chinese.

### 5. Generalize note.Write / dedup / attribution

- `note.Input` gains `Source string` (kind, e.g. `douyin`/`twitter`/`web`).
- blog filename: `{date}-{source}-{id}.md` (Douyin output stays identical to today — no break).
- `note.Exists(vaultPath, subdir, source, id)`: glob `*-{source}-{id}.md`. Callers pass source.
- Body source line (`renderBlog`) varies by source:
  - douyin: `> 来源：[抖音 @{author}]({url})`
  - twitter: `> 来源：[X @{author}]({url})`
  - web: `> 来源：[{title or domain}]({url})` (no author)
  - Implementation: pick a template by `in.Source`; fall back to title/domain when author is empty.
- obsidian format: frontmatter already has `source`, no attribution change; filename still uses the title (no id).

### 6. Config

- Reuse `cfg.TelegramProxy` as the twitter fetch proxy (yt-dlp `--proxy` + syndication HTTP client).
- The syndication `token` is a public computed value, hardcoded in `internal/twitter`, not in config.
- Web is direct, no new config.
- No breaking config changes.

## Error handling

- Routing finds no supported URL → same as today: reply "no processable link found".
- A fetcher fails → `process` edits the receipt to `❌ {stage} failed: {err}`; the worker writes the
  terminal state without retry (same as today).
- twitter: only a failure if both yt-dlp and syndication fail; syndication with neither text nor images → failure.
- web: extracted body too short (e.g. < N chars) → treat as fetch failure, tell the user (likely a JS site / paywall).
- Dedup hit (`Exists`) → `ℹ️ already published, skipping` (same as today, now across all sources).

## Testing strategy

- `internal/source`: table-driven `Classify` unit tests (mixed URLs, dedup, unknown URLs).
- `internal/web`: extraction tested against local HTML strings (drop script/style, collapse whitespace,
  read title); no real network.
- `internal/twitter`: URL→id parsing unit test; syndication JSON parsing tested with a fixed sample string;
  yt-dlp/network goes in `integration_test` (`//go:build integration`, same pattern as douyin).
- `internal/note`: table-driven tests for filename / `Exists` / source line by source (covers all three).
- `internal/llm`: content-parts builder unit test (no request); real calls go in the integration test.
- `selftest` CLI extended to accept a URL/text of any source and run the full pipeline to stdout
  (real-world server-side verification).
- Regression: Douyin unit/integration tests stay green (filename, attribution, dedup output unchanged).

## Phased implementation

Phases are independently verifiable; the Douyin path stays green throughout.

- **Phase 1 — Skeleton refactor**: `internal/source` (Classify + Item + Ref); add `kind` to `job`/`jobqueue`
  (with legacy fallback); generalize `llm.Analyze` to `Content`; add `Source` to `note.Write`/`Exists`/
  attribution; wrap douyin as an `Item`. Douyin regression fully green afterward.
- **Phase 2 — web source**: `internal/web` fetch + extraction; wire into routing; `prompt.TextNote`;
  selftest supports web pages.
- **Phase 3 — twitter source**: `internal/twitter` (yt-dlp first + syndication fallback + proxy + image
  download); image/video paths into `llm.Analyze`; wire into routing; selftest supports tweets.

Each phase: add tests, extend selftest, atomic commits.
