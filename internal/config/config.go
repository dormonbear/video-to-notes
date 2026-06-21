package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	TelegramToken string
	OpenRouterKey string
	Model         string
	Proxy         string // OpenRouter 请求走的代理；"direct" 表示不走代理
	VaultPath     string
	NoteSubdir    string
	TmpDir        string
	GitSync       bool          // 写完笔记后在 VaultPath 里 git add/commit/push
	NoteFormat    string        // "obsidian" 或 "blog"
	BlogDraft     bool          // blog 模式：是否以草稿发布
	BlogTag       string        // blog 模式：标记 tag
	BlogBaseURL   string        // blog 模式：站点域名（如 https://dormon.net），用于回执里给文章在线地址；空=不显示
	TelegramProxy string        // Telegram API 走的代理（国内被墙时用）；空=直连
	APIAddr       string        // HTTP ingest 端点监听地址（如 ":8787"）；空=不启用，供 iOS 快捷指令投递链接
	APIToken      string        // ingest 端点的 Bearer token（启用 API 时必填）
	NotifyChatID  int64         // 快捷指令触发的任务把进度/结果发到这个 Telegram 会话（启用 API 时必填）
	TwitterAuth   string        // X auth_token cookie：抓 X 长文 Article 全文（登录墙）；空=Article 跳过
	TwitterCT0    string        // X ct0 cookie（csrf），与 TwitterAuth 配套
	BookmarkPoll  time.Duration // 定时拉取 X 收藏并自动成文的间隔（如 "30m"）；0=关闭
}

// Load 从进程环境变量读取配置。
func Load() (Config, error) {
	env := map[string]string{}
	for _, k := range []string{
		"TELEGRAM_BOT_TOKEN", "OPENROUTER_API_KEY", "MODEL", "OPENROUTER_PROXY",
		"VAULT_PATH", "NOTE_SUBDIR", "TMP_DIR", "GIT_SYNC",
		"NOTE_FORMAT", "BLOG_DRAFT", "BLOG_TAG", "BLOG_BASE_URL", "TELEGRAM_PROXY",
		"API_ADDR", "API_TOKEN", "NOTIFY_CHAT_ID", "TWITTER_AUTH_TOKEN", "TWITTER_CT0",
		"BOOKMARK_POLL",
	} {
		env[k] = os.Getenv(k)
	}
	return loadFrom(env)
}

func loadFrom(env map[string]string) (Config, error) {
	c := Config{
		TelegramToken: env["TELEGRAM_BOT_TOKEN"],
		OpenRouterKey: env["OPENROUTER_API_KEY"],
		Model:         env["MODEL"],
		Proxy:         env["OPENROUTER_PROXY"],
		VaultPath:     env["VAULT_PATH"],
		NoteSubdir:    env["NOTE_SUBDIR"],
		TmpDir:        env["TMP_DIR"],
		GitSync:       env["GIT_SYNC"] == "true" || env["GIT_SYNC"] == "1",
		NoteFormat:    env["NOTE_FORMAT"],
		BlogDraft:     env["BLOG_DRAFT"] == "true" || env["BLOG_DRAFT"] == "1",
		BlogTag:       env["BLOG_TAG"],
		BlogBaseURL:   env["BLOG_BASE_URL"],
		TelegramProxy: env["TELEGRAM_PROXY"],
		APIAddr:       env["API_ADDR"],
		APIToken:      env["API_TOKEN"],
		TwitterAuth:   env["TWITTER_AUTH_TOKEN"],
		TwitterCT0:    env["TWITTER_CT0"],
	}
	if v := env["NOTIFY_CHAT_ID"]; v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("NOTIFY_CHAT_ID must be an integer: %w", err)
		}
		c.NotifyChatID = id
	}
	if v := env["BOOKMARK_POLL"]; v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("BOOKMARK_POLL must be a duration like 30m: %w", err)
		}
		c.BookmarkPoll = d
	}
	if c.APIAddr != "" && (c.APIToken == "" || c.NotifyChatID == 0) {
		return Config{}, fmt.Errorf("API_ADDR set: API_TOKEN and NOTIFY_CHAT_ID are required")
	}
	if c.BookmarkPoll > 0 && (c.TwitterAuth == "" || c.TwitterCT0 == "" || c.NotifyChatID == 0) {
		return Config{}, fmt.Errorf("BOOKMARK_POLL set: TWITTER_AUTH_TOKEN, TWITTER_CT0 and NOTIFY_CHAT_ID are required")
	}
	if c.TelegramToken == "" {
		return Config{}, fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}
	if c.OpenRouterKey == "" {
		return Config{}, fmt.Errorf("OPENROUTER_API_KEY is required")
	}
	if c.VaultPath == "" {
		return Config{}, fmt.Errorf("VAULT_PATH is required")
	}
	if c.Model == "" {
		c.Model = "google/gemini-2.5-flash"
	}
	if c.Proxy == "" {
		c.Proxy = "http://127.0.0.1:7897"
	}
	if c.NoteSubdir == "" {
		c.NoteSubdir = "video-notes"
	}
	if c.TmpDir == "" {
		c.TmpDir = "/tmp/video-to-notes"
	}
	if c.NoteFormat == "" {
		c.NoteFormat = "obsidian"
	}
	if c.BlogTag == "" {
		c.BlogTag = "video-note"
	}
	return c, nil
}
