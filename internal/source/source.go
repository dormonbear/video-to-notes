// Package source classifies input URLs and defines the normalized Item that
// every per-source fetcher produces. It depends on nothing else in the project
// (fetchers import source, never the reverse).
package source

import (
	"net/url"
	"regexp"
	"strings"
)

// Meta is the metadata parsed from a source.
type Meta struct {
	Title     string
	Author    string
	SourceURL string
	ID        string // generic id: video id / tweet id / url hash
}

// Item is the normalized fetch result handed to the LLM/write backend.
type Item struct {
	Kind       string // douyin | twitter | web
	Meta       Meta
	MediaPaths []string // local media files (video/images); empty for text-only
	MediaKind  string   // "video" | "image" | ""
	Text       string   // extracted text (tweet/article body); may be empty
}

// Ref is one classified URL extracted from a message.
type Ref struct {
	Kind string
	URL  string
}

var urlRe = regexp.MustCompile(`https?://[^\s]+`)

// HostOf returns the lowercase hostname, or "" if raw is unparseable.
func HostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func kindOf(raw string) string {
	h := HostOf(raw)
	switch {
	case h == "douyin.com" || strings.HasSuffix(h, ".douyin.com"):
		return "douyin"
	case h == "twitter.com" || strings.HasSuffix(h, ".twitter.com") ||
		h == "x.com" || strings.HasSuffix(h, ".x.com"):
		return "twitter"
	default:
		return "web"
	}
}

// Classify extracts every http(s) URL from text, trims trailing punctuation,
// dedups in order, and tags each with its source kind.
func Classify(text string) []Ref {
	seen := map[string]bool{}
	var out []Ref
	for _, raw := range urlRe.FindAllString(text, -1) {
		u := strings.TrimRight(raw, ".,;:!?。，、）)]}>\"'")
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, Ref{Kind: kindOf(u), URL: u})
	}
	return out
}
