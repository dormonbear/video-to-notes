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

// Download 提取分享文本里的抖音链接，用 yt-dlp 下载无水印视频到 destDir。
// 返回下载好的视频文件路径与元数据。yt-dlp 默认 format 已是无水印 playback 流。
func Download(shareText, destDir string) (string, Meta, error) {
	url := extractURL(shareText)
	if url == "" {
		return "", Meta{}, ErrNoURL
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", Meta{}, fmt.Errorf("mkdir tmp: %w", err)
	}
	outTmpl := filepath.Join(destDir, "%(id)s.%(ext)s")

	var stdout bytes.Buffer
	cmd := exec.Command("yt-dlp", "--no-playlist", "--no-progress", "--print-json", "-o", outTmpl, url)
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
	// --print-json 输出单行 JSON 到 stdout。
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &info); err != nil {
		return "", Meta{}, fmt.Errorf("parse yt-dlp json: %w", err)
	}

	author := info.Channel
	if author == "" {
		author = info.Uploader
	}
	path := filepath.Join(destDir, info.ID+"."+info.Ext)
	if _, err := os.Stat(path); err != nil {
		return "", Meta{}, fmt.Errorf("downloaded file missing at %s: %w", path, err)
	}
	return path, Meta{Title: info.Title, Author: author, SourceURL: info.WebpageURL}, nil
}
