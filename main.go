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
)

// job 是一条待处理的抖音链接任务。
type job struct {
	id       string // chatID:statusID，持久化主键
	chatID   int64
	statusID int // 队列回执消息 id，后续编辑它显示进度
	url      string
}

type app struct {
	cfg    config.Config
	gem    *llm.Client
	b      *bot.Bot
	store  *jobqueue.Store
	jobs   chan job
	queued int64 // atomic：排队中 + 处理中的任务数，用于回执里的队列位置
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
	a := &app{cfg: cfg, gem: gem, store: store, jobs: make(chan job, 256)}

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
		a.jobs <- job{id: p.ID, chatID: p.ChatID, statusID: p.StatusID, url: p.URL}
	}
}

// selfTest 跑完整链路并把结果打到 stdout，用于服务器端真实验证（无需 Telegram）。
// date 非空时覆盖发布时间（ISO 8601），用于按原日期就地重生成已有文章。
func (a *app) selfTest(ctx context.Context, shareText, date string) {
	mediaPath, meta, err := douyin.Fetch(shareText, a.cfg.TmpDir)
	if err != nil {
		log.Fatalf("download failed: %v", err)
	}
	defer os.Remove(mediaPath)
	if fi, err := os.Stat(mediaPath); err == nil {
		log.Printf("media ready: %s (%.2f MB)", mediaPath, float64(fi.Size())/(1<<20))
	}

	data, err := a.gem.Analyze(ctx, mediaPath)
	if err != nil {
		log.Fatalf("analyze failed: %v", err)
	}

	if date == "" {
		date = time.Now().Format(time.RFC3339)
	}
	relPath, err := note.Write(note.Input{
		Title: meta.Title, Author: meta.Author, SourceURL: meta.SourceURL,
		VideoID: meta.ID, Date: date, Data: data,
	}, note.Options{Format: a.cfg.NoteFormat, Draft: a.cfg.BlogDraft, Tag: a.cfg.BlogTag},
		a.cfg.VaultPath, a.cfg.NoteSubdir)
	if err != nil {
		log.Fatalf("write failed: %v", err)
	}
	fmt.Printf("\n===== SELFTEST OK =====\ntitle: %s\nsummary: %s\ntags: %v\nfile: %s\n--- article (first 600 runes) ---\n%s\n",
		data.Title, data.Summary, data.Tags, relPath, firstRunes(data.Article, 600))
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

	urls := douyin.ExtractURLs(update.Message.Text)
	if len(urls) == 0 {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID, Text: "没找到抖音链接，请把分享口令或链接发给我。",
		})
		return
	}

	for _, u := range urls {
		pos := atomic.AddInt64(&a.queued, 1)
		text := "✅ 已加入队列，开始处理…"
		if pos > 1 {
			text = fmt.Sprintf("✅ 已加入队列（第 %d 位），排队中…", pos)
		}
		status, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: text})
		if err != nil {
			log.Printf("send queue ack to chat %d: %v", chatID, err)
			atomic.AddInt64(&a.queued, -1)
			continue
		}
		j := job{id: fmt.Sprintf("%d:%d", chatID, status.ID), chatID: chatID, statusID: status.ID, url: u}
		if err := a.store.MarkQueued(jobqueue.Job{ID: j.id, ChatID: j.chatID, StatusID: j.statusID, URL: j.url}); err != nil {
			log.Printf("jobqueue mark queued %s: %v", j.id, err) // 尽力而为：落盘失败仍走内存队列
		}
		a.jobs <- j
	}
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

	edit("⬇️ 下载视频中…")
	mediaPath, meta, err := douyin.Fetch(j.url, a.cfg.TmpDir)
	if err != nil {
		edit(fmt.Sprintf("❌ 下载失败：%v", err))
		return err
	}
	defer os.Remove(mediaPath)

	edit("🧠 Gemini 分析中…")
	data, err := a.gem.Analyze(ctx, mediaPath)
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
