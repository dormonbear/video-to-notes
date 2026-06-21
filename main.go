package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"video-to-notes/internal/config"
	"video-to-notes/internal/douyin"
	"video-to-notes/internal/gitsync"
	"video-to-notes/internal/jobqueue"
	"video-to-notes/internal/llm"
	"video-to-notes/internal/note"
	"video-to-notes/internal/prompt"
	"video-to-notes/internal/source"
	"video-to-notes/internal/twitter"
	"video-to-notes/internal/web"
)

// job 是一条待处理的抓取任务（抖音/Twitter/网页）。
type job struct {
	id       string // chatID:statusID，持久化主键
	chatID   int64
	statusID int // 队列回执消息 id，后续编辑它显示进度
	url      string
	kind     string // douyin | twitter | web
}

type app struct {
	cfg    config.Config
	gem    *llm.Client
	b      *bot.Bot
	store  *jobqueue.Store
	jobs   chan job
	queued int64 // atomic：排队中 + 处理中的任务数，用于回执里的队列位置

	bmMu   sync.Mutex      // 保护 bmSeen
	bmSeen map[string]bool // 本次进程已入队/已存在的收藏推文 id，避免重复入队
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	gem, err := llm.New(cfg.OpenRouterKey, cfg.Model, cfg.Proxy)
	if err != nil {
		log.Fatalf("llm: %v", err)
	}
	store, err := jobqueue.Open(filepath.Join(cfg.TmpDir, "queue.jsonl"))
	if err != nil {
		log.Fatalf("jobqueue: %v", err)
	}
	a := &app{cfg: cfg, gem: gem, store: store, jobs: make(chan job, 256), bmSeen: map[string]bool{}}

	// 子命令分发。任何未知参数都直接退出，避免误把 `--help` 之类的调用
	// 当成 bot 启动（曾因此意外多跑一个实例、与正式 bot 抢 getUpdates）。
	if len(os.Args) >= 2 {
		// selftest <抖音链接> [date]：跑完整 Fetch→Analyze→Write 链路并退出，不连 Telegram。
		// 可选 date（ISO 8601）覆盖发布时间，用于按原日期就地重生成已有文章。
		if os.Args[1] == "selftest" && len(os.Args) >= 3 {
			date := ""
			if len(os.Args) >= 4 {
				date = os.Args[3]
			}
			a.selfTest(ctx, os.Args[2], date)
			return
		}
		log.Fatalf("unknown command %q (usage: video-to-notes | video-to-notes selftest <url> [date])", os.Args[1])
	}

	opts := []bot.Option{
		bot.WithDefaultHandler(a.handle),
		bot.WithInitialOffset(-1), // 跳过启动前的积压消息
	}
	// Telegram API 在部分地区被墙：通过代理访问（不影响 yt-dlp 直连下载抖音）。
	if cfg.TelegramProxy != "" {
		u, err := url.Parse(cfg.TelegramProxy)
		if err != nil {
			log.Fatalf("bad TELEGRAM_PROXY %q: %v", cfg.TelegramProxy, err)
		}
		client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u)}}
		opts = append(opts, bot.WithHTTPClient(30*time.Second, client))
	}

	b, err := bot.New(cfg.TelegramToken, opts...)
	if err != nil {
		log.Fatalf("bot: %v", err)
	}
	a.b = b
	a.recover(ctx)   // 重启后恢复未完成任务
	go a.worker(ctx) // 单 worker 串行处理队列：避免并行大上传抢代理 + git 仓库写冲突
	if cfg.APIAddr != "" {
		go a.serveAPI(ctx) // iOS 快捷指令投递链接的 HTTP 端点
	}
	if cfg.BookmarkPoll > 0 {
		go a.pollBookmarks(ctx) // 定时拉取 X 收藏并自动成文
	}
	log.Println("video-to-notes bot started")
	b.Start(ctx)
}

// recover 重新载入上次未完成的任务，压缩日志，并重新入队续跑。
func (a *app) recover(ctx context.Context) {
	pending, err := a.store.LoadPending()
	if err != nil {
		log.Printf("jobqueue load pending: %v", err)
		return
	}
	if len(pending) == 0 {
		return
	}
	if err := a.store.Compact(pending); err != nil {
		log.Printf("jobqueue compact: %v", err)
	}
	log.Printf("recovering %d pending job(s) after restart", len(pending))
	for _, p := range pending {
		atomic.AddInt64(&a.queued, 1)
		a.b.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID: p.ChatID, MessageID: p.StatusID, Text: "♻️ 重启后恢复，排队中…",
		})
		kind := p.Kind
		if kind == "" {
			kind = "douyin" // legacy queue entries predate the kind field
		}
		a.jobs <- job{id: p.ID, chatID: p.ChatID, statusID: p.StatusID, url: p.URL, kind: kind}
	}
}

// selfTest 跑完整链路并把结果打到 stdout，用于服务器端真实验证（无需 Telegram）。
// 接受任意来源的 URL/分享文本。date 非空时覆盖发布时间（ISO 8601）。
func (a *app) selfTest(ctx context.Context, shareText, date string) {
	refs := source.Classify(shareText)
	if len(refs) == 0 {
		log.Fatalf("no supported URL in input")
	}
	item, err := a.fetch(refs[0])
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
	fmt.Printf("\n===== SELFTEST OK =====\nkind: %s\ntitle: %s\nsummary: %s\ntags: %v\nfile: %s\n--- article (first 600 runes) ---\n%s\n",
		item.Kind, data.Title, data.Summary, data.Tags, relPath, firstRunes(data.Article, 600))
}

// fetch dispatches to the per-source fetcher by kind.
func (a *app) fetch(ref source.Ref) (source.Item, error) {
	switch ref.Kind {
	case "twitter":
		return twitter.Fetch(ref.URL, a.cfg.TelegramProxy, a.cfg.TmpDir, a.cfg.TwitterAuth, a.cfg.TwitterCT0)
	case "web":
		return web.Fetch(ref.URL, a.cfg.TmpDir)
	default: // douyin
		return douyin.FetchItem(ref.URL, a.cfg.TmpDir)
	}
}

// promptFor picks the analysis prompt by media kind.
func promptFor(mediaKind string) string {
	if mediaKind == "video" {
		return prompt.VideoNote
	}
	return prompt.TextNote
}

func cleanup(paths []string) {
	for _, p := range paths {
		os.Remove(p)
	}
}

// bookmarkBatch caps how many new bookmarks are enqueued per poll cycle, so a
// large backlog drains gradually instead of flooding the queue/Telegram at once.
const bookmarkBatch = 20

// bookmarkPageCap bounds how deep a single scan paginates (pages × 20 tweets).
const bookmarkPageCap = 25

// pollBookmarks periodically scans the user's X bookmarks and enqueues new ones.
func (a *app) pollBookmarks(ctx context.Context) {
	t := time.NewTicker(a.cfg.BookmarkPoll)
	defer t.Stop()
	a.scanBookmarks(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.scanBookmarks(ctx)
		}
	}
}

// scanBookmarks paginates bookmarks newest-first and enqueues up to bookmarkBatch
// not-yet-processed tweets (skipping ones already published or seen this session).
func (a *app) scanBookmarks(ctx context.Context) {
	cursor := ""
	n := 0
	for page := 0; page < bookmarkPageCap && n < bookmarkBatch; page++ {
		ids, next, err := twitter.BookmarkPage(a.cfg.TwitterAuth, a.cfg.TwitterCT0, a.cfg.TelegramProxy, cursor)
		if err != nil {
			log.Printf("bookmarks: fetch page %d: %v", page, err)
			return
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if n >= bookmarkBatch {
				break
			}
			if a.bmSeenAdd(id) {
				continue // already enqueued/handled this session
			}
			if note.Exists(a.cfg.VaultPath, a.cfg.NoteSubdir, "twitter", id) {
				continue // already published
			}
			ref := source.Ref{Kind: "twitter", URL: "https://x.com/i/status/" + id}
			if err := a.enqueue(ctx, a.cfg.NotifyChatID, ref); err != nil {
				log.Printf("bookmarks: enqueue %s: %v", id, err)
				continue
			}
			n++
		}
		if next == "" || next == cursor {
			break
		}
		cursor = next
	}
	if n > 0 {
		log.Printf("bookmarks: enqueued %d new tweet(s)", n)
	}
}

// bmSeenAdd marks id seen and reports whether it was already seen.
func (a *app) bmSeenAdd(id string) bool {
	a.bmMu.Lock()
	defer a.bmMu.Unlock()
	if a.bmSeen[id] {
		return true
	}
	a.bmSeen[id] = true
	return false
}

func firstRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + " …"
	}
	return s
}

// handle 只做轻活：解析消息里的所有抖音链接、立刻回执、入队，然后返回。
// 重活由 worker 串行处理，因此并发/批量发送都能马上收到回执。
func (a *app) handle(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil || update.Message.Text == "" {
		return
	}
	chatID := update.Message.Chat.ID

	refs := source.Classify(update.Message.Text)
	if len(refs) == 0 {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID, Text: "没找到可处理的链接，请发抖音 / Twitter / 网页链接。",
		})
		return
	}

	for _, ref := range refs {
		if err := a.enqueue(ctx, chatID, ref); err != nil {
			log.Printf("send queue ack to chat %d: %v", chatID, err)
		}
	}
}

// enqueue 向 chatID 发一条队列回执、把任务落盘、推给 worker。Telegram handler
// 和 HTTP ingest 端点共用它，保证两条入口走完全相同的处理/持久化/恢复链路。
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
		log.Printf("jobqueue mark queued %s: %v", j.id, err) // 尽力而为：落盘失败仍走内存队列
	}
	a.jobs <- j
	return nil
}

// worker 串行消费队列。串行是有意为之：并行大上传会抢那条脆弱的代理出口、
// 并行 git push 会撞同一仓库的 index.lock，都会显著降低成功率。
func (a *app) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-a.jobs:
			err := a.process(ctx, j)
			if err == nil {
				if e := a.store.MarkDone(j.id); e != nil {
					log.Printf("jobqueue mark done %s: %v", j.id, e)
				}
			} else {
				if e := a.store.MarkFailed(j.id); e != nil {
					log.Printf("jobqueue mark failed %s: %v", j.id, e)
				}
			}
			atomic.AddInt64(&a.queued, -1)
		}
	}
}

// process 跑单条任务的完整链路并实时编辑回执消息。返回 nil 表示成功，
// 非 nil 表示已向用户上报 ❌ 的失败（worker 据此写终态，不再重试）。
func (a *app) process(ctx context.Context, j job) error {
	edit := func(text string) {
		a.b.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID: j.chatID, MessageID: j.statusID, Text: text,
		})
	}

	edit("⬇️ 抓取中…")
	item, err := a.fetch(source.Ref{Kind: j.kind, URL: j.url})
	if err != nil {
		edit(fmt.Sprintf("❌ 抓取失败：%v", err))
		return err
	}
	defer cleanup(item.MediaPaths)

	// 去重：同一内容已发布过就跳过（按 source+id，含跨天）。
	// 在抓取后、Gemini 分析前检查——抓取很便宜，省下的是分析费用与重复发布。
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
		Source:    item.Kind,
		Title:     item.Meta.Title,
		Author:    item.Meta.Author,
		SourceURL: item.Meta.SourceURL,
		VideoID:   item.Meta.ID,
		Date:      date,
		Data:      data,
	}, note.Options{
		Format: a.cfg.NoteFormat,
		Draft:  a.cfg.BlogDraft,
		Tag:    a.cfg.BlogTag,
	}, a.cfg.VaultPath, a.cfg.NoteSubdir)
	if err != nil {
		edit(fmt.Sprintf("❌ 写入失败：%v", err))
		return err
	}

	if a.cfg.GitSync {
		if err := gitsync.Push(a.cfg.VaultPath, relPath, "add note: "+relPath); err != nil {
			log.Printf("git sync failed: %v", err)
			edit(fmt.Sprintf("✅ 已生成笔记（⚠️ git 同步失败：%v）\n%s\n\n%s", err, data.Summary, relPath))
			return nil // 笔记已生成，视为成功；git 失败只提示，不重试
		}
	}

	link := ""
	if a.cfg.NoteFormat == "blog" {
		if u := note.PostURL(a.cfg.BlogBaseURL, relPath); u != "" {
			link = "\n🔗 " + u
		}
	}
	edit(fmt.Sprintf("✅ 已生成笔记\n%s\n\n%s%s", data.Summary, relPath, link))
	return nil
}
