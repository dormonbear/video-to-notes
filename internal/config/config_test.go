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
