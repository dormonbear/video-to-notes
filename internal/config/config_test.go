package config

import "testing"

func TestLoadValidatesRequiredFields(t *testing.T) {
	env := map[string]string{
		"OPENROUTER_API_KEY": "key",
		"VAULT_PATH":         "/v",
	} // 缺 TELEGRAM_BOT_TOKEN
	_, err := loadFrom(env)
	if err == nil {
		t.Fatal("expected error for missing TELEGRAM_BOT_TOKEN, got nil")
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	env := map[string]string{
		"TELEGRAM_BOT_TOKEN": "tok",
		"OPENROUTER_API_KEY": "key",
		"VAULT_PATH":         "/v",
	}
	c, err := loadFrom(env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Model != "google/gemini-2.5-flash" {
		t.Errorf("Model default = %q, want google/gemini-2.5-flash", c.Model)
	}
	if c.Proxy != "http://127.0.0.1:7897" {
		t.Errorf("Proxy default = %q, want http://127.0.0.1:7897", c.Proxy)
	}
	if c.NoteSubdir != "video-notes" {
		t.Errorf("NoteSubdir default = %q, want video-notes", c.NoteSubdir)
	}
	if c.TmpDir != "/tmp/video-to-notes" {
		t.Errorf("TmpDir default = %q", c.TmpDir)
	}
	if c.NoteFormat != "obsidian" {
		t.Errorf("NoteFormat default = %q, want obsidian", c.NoteFormat)
	}
	if c.BlogTag != "video-note" {
		t.Errorf("BlogTag default = %q, want video-note", c.BlogTag)
	}
	if c.BlogDraft != false {
		t.Errorf("BlogDraft default = %v, want false", c.BlogDraft)
	}
}
