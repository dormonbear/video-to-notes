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

	"video-to-notes/internal/source"
)

// ErrNoURL 表示消息文本里没有抖音链接。
var ErrNoURL = errors.New("no douyin url found in message")

// Meta 是从视频解析出的元数据。
type Meta struct {
	Title     string
	Author    string
	SourceURL string
	ID        string // 抖音视频 id，用于博客文件名/slug
}

var urlRe = regexp.MustCompile(`https?://[^\s]*douyin\.com/[^\s]+`)

func extractURL(s string) string {
	return urlRe.FindString(s)
}

// ExtractURLs 返回文本里所有抖音链接（按出现顺序去重），用于单条消息含多个链接的批量场景。
func ExtractURLs(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, u := range urlRe.FindAllString(s, -1) {
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// maxInlineBytes 是发给 OpenRouter 的视频上限（base64 内联约 20MB，留余量按 18MB）。
const maxInlineBytes = 18 << 20

// Fetch 提取分享文本里的抖音链接，下载无水印视频，再用 ffmpeg 压制成低分辨率、
// 1fps 的小体积 mp4（保留画面+音频），删掉原视频，返回压制后视频路径与元数据。
// 1fps 是 Gemini 视频理解的默认采样率，因此不损失模型可见信息，又让体积与时长解耦，
// 能稳定控制在 OpenRouter 内联上限以下。
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
	defer os.Remove(videoPath) // 压制后就删原视频

	outPath := filepath.Join(destDir, info.ID+".small.mp4")
	if err := Transcode(videoPath, outPath); err != nil {
		return "", Meta{}, err
	}

	author := info.Channel
	if author == "" {
		author = info.Uploader
	}
	return outPath, Meta{Title: info.Title, Author: author, SourceURL: info.WebpageURL, ID: info.ID}, nil
}

// FetchItem runs Fetch and wraps the result as a source.Item (video media kind).
func FetchItem(shareText, destDir string) (source.Item, error) {
	path, m, err := Fetch(shareText, destDir)
	if err != nil {
		return source.Item{}, err
	}
	return source.Item{
		Kind:       "douyin",
		Meta:       source.Meta{Title: m.Title, Author: m.Author, SourceURL: m.SourceURL, ID: m.ID},
		MediaPaths: []string{path},
		MediaKind:  "video",
	}, nil
}

// Transcode 用递进的压制档位生成小体积 mp4：先 480p，若仍超上限再降到 360p。
func Transcode(src, dst string) error {
	profiles := []struct{ scale, crf string }{
		{"480", "28"},
		{"360", "32"},
	}
	var lastErr error
	for _, p := range profiles {
		args := []string{
			"-y", "-i", src,
			"-vf", "scale=-2:" + p.scale, "-r", "1",
			"-c:v", "libx264", "-crf", p.crf, "-preset", "veryfast", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-b:a", "40k", "-ac", "1",
			"-movflags", "+faststart", dst,
		}
		ff := exec.Command("ffmpeg", args...)
		ff.Stderr = os.Stderr
		if err := ff.Run(); err != nil {
			lastErr = fmt.Errorf("ffmpeg transcode failed: %w", err)
			continue
		}
		fi, err := os.Stat(dst)
		if err != nil || fi.Size() == 0 {
			lastErr = fmt.Errorf("transcoded video missing/empty at %s: %w", dst, err)
			continue
		}
		if fi.Size() <= maxInlineBytes {
			return nil
		}
		lastErr = fmt.Errorf("transcoded video too large: %d bytes (limit %d)", fi.Size(), maxInlineBytes)
	}
	return lastErr
}
