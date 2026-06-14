package config

import (
	"fmt"
	"os"
)

type Config struct {
	TelegramToken string
	OpenRouterKey string
	Model         string
	Proxy         string // OpenRouter 请求走的代理；"direct" 表示不走代理
	VaultPath     string
	NoteSubdir    string
	TmpDir        string
	GitSync       bool // 写完笔记后在 VaultPath 里 git add/commit/push
}

// Load 从进程环境变量读取配置。
func Load() (Config, error) {
	env := map[string]string{}
	for _, k := range []string{
		"TELEGRAM_BOT_TOKEN", "OPENROUTER_API_KEY", "MODEL", "OPENROUTER_PROXY",
		"VAULT_PATH", "NOTE_SUBDIR", "TMP_DIR", "GIT_SYNC",
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
	return c, nil
}
