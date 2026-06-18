// Package llm calls an OpenRouter (OpenAI-compatible) chat model with a video
// attachment and returns structured note data. Works with any OpenRouter model
// that supports video input (e.g. google/gemini-2.5-flash, z-ai/glm-4.6v).
package llm

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"video-to-notes/internal/note"
	"video-to-notes/internal/prompt"
)

const endpoint = "https://openrouter.ai/api/v1/chat/completions"

type Client struct {
	apiKey string
	model  string
	http   *http.Client
}

// New builds a client. proxy is the OpenRouter proxy URL; "direct" disables the
// proxy, "" falls back to the standard HTTP(S)_PROXY environment variables.
func New(apiKey, model, proxy string) (*Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("openrouter api key required")
	}
	tr := &http.Transport{}
	switch proxy {
	case "direct":
		tr.Proxy = nil
	case "":
		tr.Proxy = http.ProxyFromEnvironment
	default:
		u, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("bad proxy url %q: %w", proxy, err)
		}
		tr.Proxy = http.ProxyURL(u)
	}
	// Force HTTP/1.1. Over a flaky obfs/relay proxy, large HTTP/2 uploads get the
	// h2 stream corrupted ("tls: bad record MAC") or reset (EOF); HTTP/1.1 is robust.
	tr.ForceAttemptHTTP2 = false
	tr.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	// Fresh connection per request. The pooled connection over this proxy can go
	// half-broken; reusing it makes every retry fail identically (EOF). A new
	// connection each attempt mirrors what reliably works (python urllib).
	tr.DisableKeepAlives = true
	return &Client{
		apiKey: apiKey,
		model:  model,
		http:   &http.Client{Timeout: 5 * time.Minute, Transport: tr},
	}, nil
}

// noteSchema is the OpenAI-style json_schema response_format payload, mirroring note.Data.
func noteSchema() map[string]any {
	str := map[string]any{"type": "string"}
	return map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   "video_note",
			"strict": true,
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":   str,
					"summary": str,
					"tags":    map[string]any{"type": "array", "items": str},
					"article": str,
				},
				"required":             []string{"title", "summary", "tags", "article"},
				"additionalProperties": false,
			},
		},
	}
}

// Analyze base64-encodes the video, sends it to the model and returns structured note data.
// The video is sent as a data URL via the video_url content part. base64 video is only
// accepted by Gemini on Vertex AI (AI Studio requires a YouTube link), so the request
// pins the provider to google-vertex with fallbacks disabled.
func (c *Client) Analyze(ctx context.Context, videoPath string) (note.Data, error) {
	raw, err := os.ReadFile(videoPath)
	if err != nil {
		return note.Data{}, fmt.Errorf("read video: %w", err)
	}
	dataURL := "data:video/mp4;base64," + base64.StdEncoding.EncodeToString(raw)

	reqBody := map[string]any{
		"model": c.model,
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": prompt.VideoNote},
					map[string]any{"type": "video_url", "video_url": map[string]any{"url": dataURL}},
				},
			},
		},
		"response_format": noteSchema(),
		"provider":        map[string]any{"order": []string{"google-vertex"}, "allow_fallbacks": false},
	}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return note.Data{}, fmt.Errorf("marshal request: %w", err)
	}

	// The proxy/relay path is flaky for large round-trips: requests/responses get
	// truncated, reset, or TLS-corrupted intermittently. Retry the whole exchange a
	// few times with backoff; a fresh connection usually succeeds within a couple tries.
	const attempts = 4
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return note.Data{}, ctx.Err()
			case <-time.After(time.Duration(i) * 2 * time.Second):
			}
		}
		data, err := c.exchange(ctx, buf)
		if err == nil {
			return data, nil
		}
		lastErr = err
	}
	return note.Data{}, fmt.Errorf("after %d attempts: %w", attempts, lastErr)
}

// exchange performs one request/response round-trip and parses the note.
func (c *Client) exchange(ctx context.Context, buf []byte) (note.Data, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return note.Data{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return note.Data{}, fmt.Errorf("openrouter request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return note.Data{}, fmt.Errorf("read response body (truncated): %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return note.Data{}, fmt.Errorf("openrouter HTTP %d: %s", resp.StatusCode, truncate(body, 400))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return note.Data{}, fmt.Errorf("decode response (%d bytes): %w", len(body), err)
	}
	if out.Error != nil {
		return note.Data{}, fmt.Errorf("openrouter error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content == "" {
		return note.Data{}, fmt.Errorf("empty response from model")
	}

	var d struct {
		Title   string   `json:"title"`
		Summary string   `json:"summary"`
		Tags    []string `json:"tags"`
		Article string   `json:"article"`
	}
	if err := json.Unmarshal([]byte(out.Choices[0].Message.Content), &d); err != nil {
		return note.Data{}, fmt.Errorf("parse note json: %w", err)
	}
	return note.Data{Title: d.Title, Summary: d.Summary, Tags: d.Tags, Article: d.Article}, nil
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n])
	}
	return string(b)
}
