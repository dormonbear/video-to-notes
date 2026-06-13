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
	"video-to-notes/internal/llm"
	"video-to-notes/internal/note"
)

type app struct {
	cfg config.Config
	gem *llm.Client
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

	status, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID, Text: "⬇️ 下载视频中…",
	})
	if err != nil {
		log.Printf("send initial status to chat %d: %v", chatID, err)
	}
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
