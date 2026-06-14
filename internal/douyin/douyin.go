package douyin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

// ErrNoURL 表示消息文本里没有抖音链接。
var ErrNoURL = errors.New("no douyin url found in message")

// Meta 是从视频解析出的元数据。
type Meta struct {
	Title     string
	Author    string
	SourceURL string
}

var urlRe = regexp.MustCompile(`https?://[^\s]*douyin\.com/[^\s]+`)

func extractURL(s string) string {
	return urlRe.FindString(s)
}

// Fetch 提取分享文本里的抖音链接，下载最小体积的无水印视频，再用 ffmpeg 抽取
// 低码率单声道音频（删掉视频本体），返回音频文件路径与元数据。
// 只取音频：笔记内容（摘要/要点/转写）都来自语音，且音频远小于视频，可处理长视频
// 并绕开 OpenRouter 内联 ~20MB 的上限。
func Fetch(shareText, destDir string) (string, Meta, error) {
	url := extractURL(shareText)
	if url == "" {
		return "", Meta{}, ErrNoURL
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", Meta{}, fmt.Errorf("mkdir tmp: %w", err)
	}
	outTmpl := filepath.Join(destDir, "%(id)s.%(ext)s")

	// -S "+size" 优先最小体积的格式，减少下载量。
	var stdout bytes.Buffer
	cmd := exec.Command("yt-dlp", "--no-playlist", "--no-progress", "-S", "+size", "--print-json", "-o", outTmpl, url)
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", Meta{}, fmt.Errorf("yt-dlp failed: %w", err)
	}

	var info struct {
		ID         string `json:"id"`
		Ext        string `json:"ext"`
		Title      string `json:"title"`
		Channel    string `json:"channel"`
		Uploader   string `json:"uploader"`
		WebpageURL string `json:"webpage_url"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &info); err != nil {
		return "", Meta{}, fmt.Errorf("parse yt-dlp json: %w", err)
	}

	videoPath := filepath.Join(destDir, info.ID+"."+info.Ext)
	if _, err := os.Stat(videoPath); err != nil {
		return "", Meta{}, fmt.Errorf("downloaded file missing at %s: %w", videoPath, err)
	}
	defer os.Remove(videoPath) // 抽完音频就删视频

	audioPath := filepath.Join(destDir, info.ID+".mp3")
	// 单声道 16kHz 48kbps mp3：语音清晰、体积极小（约 0.36MB/分钟）。
	ff := exec.Command("ffmpeg", "-y", "-i", videoPath, "-vn", "-ac", "1", "-ar", "16000", "-b:a", "48k", audioPath)
	ff.Stderr = os.Stderr
	if err := ff.Run(); err != nil {
		return "", Meta{}, fmt.Errorf("ffmpeg extract audio failed: %w", err)
	}
	if fi, err := os.Stat(audioPath); err != nil || fi.Size() == 0 {
		return "", Meta{}, fmt.Errorf("extracted audio missing/empty at %s: %w", audioPath, err)
	}

	author := info.Channel
	if author == "" {
		author = info.Uploader
	}
	return audioPath, Meta{Title: info.Title, Author: author, SourceURL: info.WebpageURL}, nil
}
