package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// StructuredRequest asks the model for a JSON object that matches a strict
// schema, optionally about an image or a PDF. No tools are ever sent with
// it: whatever the document says cannot trigger an action.
//
// It talks to the Chat Completions API over plain HTTP because the go-openai
// version in use has no "file" content part (needed for PDFs).
type StructuredRequest struct {
	Model      string
	System     string
	Text       string
	File       []byte
	Mime       string
	FileName   string
	SchemaName string
	Schema     map[string]any
}

// StructuredClient calls the API. BaseURL and HTTP are overridable in tests.
type StructuredClient struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
}

// NewStructuredClient uses the process OpenAI key.
func NewStructuredClient() *StructuredClient {
	return &StructuredClient{APIKey: os.Getenv("OPENAI_API_KEY"), BaseURL: "https://api.openai.com/v1", HTTP: &http.Client{Timeout: 90 * time.Second}}
}

// ErrRefused is returned when the model declines to answer.
var ErrRefused = errors.New("openai refused the request")

func (c *StructuredClient) Complete(ctx context.Context, r StructuredRequest) ([]byte, error) {
	if c.APIKey == "" {
		return nil, errors.New("openai error: OPENAI_API_KEY not set")
	}
	content := []map[string]any{{"type": "text", "text": r.Text}}
	if len(r.File) > 0 {
		dataURL := "data:" + r.Mime + ";base64," + base64.StdEncoding.EncodeToString(r.File)
		if r.Mime == "application/pdf" {
			content = append(content, map[string]any{"type": "file", "file": map[string]any{"filename": r.FileName, "file_data": dataURL}})
		} else {
			content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL, "detail": "high"}})
		}
	}
	body, err := json.Marshal(map[string]any{
		"model":       r.Model,
		"temperature": 0,
		"messages": []map[string]any{
			{"role": "system", "content": r.System},
			{"role": "user", "content": content},
		},
		"response_format": map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": r.SchemaName, "strict": true, "schema": r.Schema},
		},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai error: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("openai error: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// The body may echo request details; only the status is reported.
		return nil, fmt.Errorf("openai error: status %d", resp.StatusCode)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string  `json:"content"`
				Refusal *string `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
		return nil, errors.New("openai error: unexpected response")
	}
	if out.Choices[0].Message.Refusal != nil {
		return nil, ErrRefused
	}
	return []byte(out.Choices[0].Message.Content), nil
}
