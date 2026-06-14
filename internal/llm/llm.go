// Package llm calls an OpenRouter (OpenAI-compatible) chat model with a video
// attachment and returns structured note data. Works with any OpenRouter model
// that supports video input (e.g. google/gemini-2.5-flash, z-ai/glm-4.6v).
package llm

import (
	"bytes"
	"context"
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
					"title":      str,
					"summary":    str,
					"tags":       map[string]any{"type": "array", "items": str},
					"key_points": map[string]any{"type": "array", "items": str},
					"transcript": str,
				},
				"required":             []string{"title", "summary", "tags", "key_points", "transcript"},
				"additionalProperties": false,
			},
		},
	}
}

// Analyze base64-encodes the audio, sends it to the model and returns structured note data.
func (c *Client) Analyze(ctx context.Context, audioPath string) (note.Data, error) {
	raw, err := os.ReadFile(audioPath)
	if err != nil {
		return note.Data{}, fmt.Errorf("read audio: %w", err)
	}
	b64 := base64.StdEncoding.EncodeToString(raw)

	reqBody := map[string]any{
		"model": c.model,
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": prompt.VideoNote},
					map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": b64, "format": "mp3"}},
				},
			},
		},
		"response_format": noteSchema(),
	}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return note.Data{}, fmt.Errorf("marshal request: %w", err)
	}

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
	body, _ := io.ReadAll(resp.Body)
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
		return note.Data{}, fmt.Errorf("decode response: %w", err)
	}
	if out.Error != nil {
		return note.Data{}, fmt.Errorf("openrouter error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content == "" {
		return note.Data{}, fmt.Errorf("empty response from model")
	}

	var d struct {
		Title      string   `json:"title"`
		Summary    string   `json:"summary"`
		Tags       []string `json:"tags"`
		KeyPoints  []string `json:"key_points"`
		Transcript string   `json:"transcript"`
	}
	if err := json.Unmarshal([]byte(out.Choices[0].Message.Content), &d); err != nil {
		return note.Data{}, fmt.Errorf("parse note json: %w", err)
	}
	return note.Data{Title: d.Title, Summary: d.Summary, Tags: d.Tags, KeyPoints: d.KeyPoints, Transcript: d.Transcript}, nil
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n])
	}
	return string(b)
}
