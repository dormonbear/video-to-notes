// Package twitter fetches a single tweet: yt-dlp first (video tweets), falling
// back to the public syndication endpoint (text + image tweets). All network
// goes through the supplied proxy (X is blocked on the bot's egress).
package twitter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"video-to-notes/internal/douyin"
	"video-to-notes/internal/source"
)

var idRe = regexp.MustCompile(`/status/(\d+)`)

func tweetID(rawURL string) (string, error) {
	m := idRe.FindStringSubmatch(rawURL)
	if m == nil {
		return "", fmt.Errorf("no /status/{id} in %q", rawURL)
	}
	return m[1], nil
}

// synToken mirrors react-tweet: ((id/1e15)*PI) in base36, with '0' and '.' stripped.
func synToken(id string) string {
	n, _ := strconv.ParseFloat(id, 64)
	v := (n / 1e15) * math.Pi
	s := base36(v)
	s = strings.ReplaceAll(s, "0", "")
	s = strings.ReplaceAll(s, ".", "")
	return s
}

func base36(f float64) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	i := int64(f)
	frac := f - float64(i)
	var ip string
	if i == 0 {
		ip = "0"
	}
	for i > 0 {
		ip = string(digits[i%36]) + ip
		i /= 36
	}
	var fp strings.Builder
	for k := 0; k < 16 && frac > 0; k++ {
		frac *= 36
		d := int(frac)
		fp.WriteByte(digits[d])
		frac -= float64(d)
	}
	return ip + "." + fp.String()
}

func parseSyndication(jsonBytes []byte) (text string, images []string, author string, err error) {
	var t struct {
		Text string `json:"text"`
		User struct {
			ScreenName string `json:"screen_name"`
		} `json:"user"`
		MediaDetails []struct {
			Type          string `json:"type"`
			MediaURLHTTPS string `json:"media_url_https"`
		} `json:"mediaDetails"`
	}
	if err = json.Unmarshal(jsonBytes, &t); err != nil {
		return "", nil, "", fmt.Errorf("parse syndication json: %w", err)
	}
	for _, m := range t.MediaDetails {
		if m.Type == "photo" && m.MediaURLHTTPS != "" {
			images = append(images, m.MediaURLHTTPS)
		}
	}
	return t.Text, images, t.User.ScreenName, nil
}

// Fetch tries yt-dlp (video tweets) then the syndication endpoint (text+image
// tweets). All HTTP/yt-dlp traffic uses proxy.
func Fetch(rawURL, proxy, tmpDir string) (source.Item, error) {
	id, err := tweetID(rawURL)
	if err != nil {
		return source.Item{}, err
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return source.Item{}, err
	}
	meta := source.Meta{SourceURL: rawURL, ID: id}

	// 1) yt-dlp — succeeds only when the tweet has video.
	if item, ok := fetchVideo(rawURL, proxy, tmpDir, meta); ok {
		return item, nil
	}

	// 2) syndication — text + images.
	text, images, author, err := fetchSyndication(id, proxy)
	if err != nil {
		return source.Item{}, fmt.Errorf("twitter fallback: %w", err)
	}
	meta.Author = author
	meta.Title = firstLine(text)
	var paths []string
	for i, u := range images {
		p := filepath.Join(tmpDir, fmt.Sprintf("%s-%d.jpg", id, i))
		if err := downloadFile(u, p, proxy); err == nil {
			paths = append(paths, p)
		}
	}
	if text == "" && len(paths) == 0 {
		return source.Item{}, fmt.Errorf("tweet %s has no text or media", id)
	}
	kind := ""
	if len(paths) > 0 {
		kind = "image"
	}
	return source.Item{Kind: "twitter", Meta: meta, MediaPaths: paths, MediaKind: kind, Text: text}, nil
}

func fetchVideo(rawURL, proxy, tmpDir string, meta source.Meta) (source.Item, bool) {
	outTmpl := filepath.Join(tmpDir, "%(id)s.%(ext)s")
	var stdout bytes.Buffer
	args := []string{"--no-playlist", "--no-progress", "-S", "+size", "--print-json", "-o", outTmpl}
	if proxy != "" {
		args = append(args, "--proxy", proxy)
	}
	args = append(args, rawURL)
	cmd := exec.Command("yt-dlp", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return source.Item{}, false // no video / yt-dlp failed → caller falls back
	}
	var info struct {
		ID, Ext, Title, Uploader, UploaderID, WebpageURL string
	}
	if json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &info) != nil {
		return source.Item{}, false
	}
	src := filepath.Join(tmpDir, info.ID+"."+info.Ext)
	defer os.Remove(src)
	dst := filepath.Join(tmpDir, info.ID+".small.mp4")
	if err := douyin.Transcode(src, dst); err != nil {
		return source.Item{}, false
	}
	meta.Author = info.UploaderID
	meta.Title = info.Title
	return source.Item{Kind: "twitter", Meta: meta, MediaPaths: []string{dst}, MediaKind: "video"}, true
}

func fetchSyndication(id, proxy string) (text string, images []string, author string, err error) {
	endpoint := fmt.Sprintf("https://cdn.syndication.twimg.com/tweet-result?id=%s&token=%s&lang=en", id, synToken(id))
	body, err := httpGet(endpoint, proxy)
	if err != nil {
		return "", nil, "", err
	}
	return parseSyndication(body)
}

func proxyClient(proxy string) *http.Client {
	tr := &http.Transport{}
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: tr}
}

func httpGet(u, proxy string) ([]byte, error) {
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := proxyClient(proxy).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func downloadFile(u, dst, proxy string) error {
	b, err := httpGet(u, proxy)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(strings.TrimSpace(s)); len(r) > 30 {
		return string(r[:30])
	}
	return strings.TrimSpace(s)
}
