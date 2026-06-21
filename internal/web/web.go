// Package web fetches a webpage and extracts its readable text for the LLM.
// ponytail: lightweight tag-stripping, not a full readability algorithm; the LLM
// distills the article from noisy text. Upgrade to go-readability if quality lags.
package web

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"

	"video-to-notes/internal/source"
)

const (
	maxBody = 4 << 20 // 4 MB cap on the fetched HTML
	maxText = 40000   // rune cap on extracted text (token control)
	minText = 200     // below this we treat the page as un-fetchable (JS/paywall)
	ua      = "Mozilla/5.0 (compatible; video-to-notes/1.0)"
)

func urlID(rawURL string) string {
	sum := sha1.Sum([]byte(rawURL))
	return hex.EncodeToString(sum[:])[:12]
}

// extract drops script/style/head, collects visible text nodes, collapses
// whitespace, reads <title>, and truncates to maxText runes.
func extract(htmlBytes []byte) (title, text string) {
	doc, err := html.Parse(strings.NewReader(string(htmlBytes)))
	if err != nil {
		return "", ""
	}
	skip := map[string]bool{"script": true, "style": true, "noscript": true}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "title" {
			if n.FirstChild != nil {
				title = strings.TrimSpace(n.FirstChild.Data)
			}
			return // don't fold the title into the body text
		}
		if n.Type == html.ElementNode && skip[n.Data] {
			return
		}
		if n.Type == html.TextNode {
			if t := strings.TrimSpace(n.Data); t != "" {
				b.WriteString(t)
				b.WriteByte('\n')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	// Collapse runs of blank lines but keep paragraph breaks.
	var lines []string
	for _, ln := range strings.Split(b.String(), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			lines = append(lines, ln)
		}
	}
	text = strings.Join(lines, "\n")
	if r := []rune(text); len(r) > maxText {
		text = string(r[:maxText])
	}
	return title, text
}

// Fetch downloads rawURL (direct, no proxy) and returns a text-only Item.
func Fetch(rawURL, _ string) (source.Item, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return source.Item{}, err
	}
	req.Header.Set("User-Agent", ua)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return source.Item{}, fmt.Errorf("web get: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return source.Item{}, fmt.Errorf("web HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return source.Item{}, fmt.Errorf("web read: %w", err)
	}
	title, text := extract(body)
	if len([]rune(text)) < minText {
		return source.Item{}, fmt.Errorf("extracted too little text (%d runes); page may need JS/login", len([]rune(text)))
	}
	return source.Item{
		Kind:      "web",
		Meta:      source.Meta{Title: title, SourceURL: rawURL, ID: urlID(rawURL)},
		MediaKind: "",
		Text:      text,
	}, nil
}
