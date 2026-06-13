package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"google.golang.org/genai"

	"video-to-notes/internal/note"
	"video-to-notes/internal/prompt"
)

// Client 包一个 genai 客户端与模型名。
type Client struct {
	gc    *genai.Client
	model string
}

func New(ctx context.Context, apiKey, model string) (*Client, error) {
	gc, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("genai client: %w", err)
	}
	return &Client{gc: gc, model: model}, nil
}

// noteSchema 强制结构化输出，字段与 note.Data 对应。
func noteSchema() *genai.Schema {
	str := &genai.Schema{Type: genai.TypeString}
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"summary":    str,
			"tags":       {Type: genai.TypeArray, Items: str},
			"key_points": {Type: genai.TypeArray, Items: str},
			"transcript": str,
		},
		Required:         []string{"summary", "tags", "key_points", "transcript"},
		PropertyOrdering: []string{"summary", "tags", "key_points", "transcript"},
	}
}

// Analyze 上传视频文件，等待处理完成，调用模型返回结构化笔记内容。
func (c *Client) Analyze(ctx context.Context, videoPath string) (note.Data, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	f, err := os.Open(videoPath)
	if err != nil {
		return note.Data{}, fmt.Errorf("open video: %w", err)
	}
	defer f.Close()

	uploaded, err := c.gc.Files.Upload(ctx, f, &genai.UploadFileConfig{MIMEType: "video/mp4"})
	if err != nil {
		return note.Data{}, fmt.Errorf("upload: %w", err)
	}
	defer c.gc.Files.Delete(ctx, uploaded.Name, nil) //nolint:errcheck

	// 轮询直到 ACTIVE。
	for uploaded.State == genai.FileStateProcessing {
		time.Sleep(2 * time.Second)
		uploaded, err = c.gc.Files.Get(ctx, uploaded.Name, nil)
		if err != nil {
			return note.Data{}, fmt.Errorf("poll file: %w", err)
		}
	}
	if uploaded.State != genai.FileStateActive {
		return note.Data{}, fmt.Errorf("file not active, state=%s", uploaded.State)
	}

	contents := []*genai.Content{{
		Role: "user",
		Parts: []*genai.Part{
			genai.NewPartFromText(prompt.VideoNote),
			genai.NewPartFromURI(uploaded.URI, uploaded.MIMEType),
		},
	}}

	resp, err := c.gc.Models.GenerateContent(ctx, c.model, contents, &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   noteSchema(),
	})
	if err != nil {
		return note.Data{}, fmt.Errorf("generate: %w", err)
	}

	var out struct {
		Summary    string   `json:"summary"`
		Tags       []string `json:"tags"`
		KeyPoints  []string `json:"key_points"`
		Transcript string   `json:"transcript"`
	}
	if err := json.Unmarshal([]byte(resp.Text()), &out); err != nil {
		return note.Data{}, fmt.Errorf("parse model json: %w", err)
	}
	return note.Data{
		Summary:    out.Summary,
		Tags:       out.Tags,
		KeyPoints:  out.KeyPoints,
		Transcript: out.Transcript,
	}, nil
}
