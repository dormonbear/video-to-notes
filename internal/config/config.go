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
