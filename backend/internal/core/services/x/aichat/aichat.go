// Package aichat is a minimal OpenAI-compatible chat client shared by the
// fork's AI features ("What can I build?", sale-listing drafts). It works with
// any /chat/completions endpoint, including local Ollama or llama.cpp.
package aichat

// Fork addition. See FORK_NOTES.md.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// ErrDisabled is returned by callers when no endpoint is configured.
var ErrDisabled = errors.New("ai: no LLM endpoint configured")

// Chatter sends one system+user exchange and returns the assistant text.
// With jsonMode the endpoint is asked for a JSON object.
type Chatter interface {
	Chat(ctx context.Context, system, user string, jsonMode bool) (string, error)
}

// Image is an image attached to a chat request.
type Image struct {
	MIME string
	Data []byte
}

// VisionChatter is a Chatter that can also look at images. The built-in
// client implements it; tests can fake it.
type VisionChatter interface {
	Chatter
	ChatVision(ctx context.Context, system, user string, images []Image, jsonMode bool) (string, error)
}

// Config for the OpenAI-compatible endpoint.
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	// VisionModel is used for image requests; defaults to Model.
	VisionModel string
	// ResponseFormat selects how JSON mode is requested: "json_object"
	// (default), "json_schema" (LM Studio) or "none".
	ResponseFormat string
	Timeout        time.Duration
}

// Enabled reports whether enough is configured to call an endpoint.
func (c Config) Enabled() bool { return c.BaseURL != "" && c.Model != "" }

// New returns a Chatter, or nil when the config is not enabled.
func New(cfg Config) Chatter {
	if !cfg.Enabled() {
		return nil
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 90 * time.Second
	}
	return &client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}
}

const maxResponseBytes = 1 << 20

type client struct {
	cfg  Config
	http *http.Client
	// formatRejected is set once the server refused response_format and
	// accepted the same request without it.
	formatRejected atomic.Bool
}

func (c *client) Chat(ctx context.Context, system, user string, jsonMode bool) (string, error) {
	return c.do(ctx, c.cfg.Model, []map[string]any{
		{"role": "system", "content": system},
		{"role": "user", "content": user},
	}, jsonMode)
}

func (c *client) ChatVision(ctx context.Context, system, user string, images []Image, jsonMode bool) (string, error) {
	parts := []map[string]any{{"type": "text", "text": user}}
	for _, im := range images {
		parts = append(parts, map[string]any{
			"type":      "image_url",
			"image_url": map[string]string{"url": "data:" + im.MIME + ";base64," + base64.StdEncoding.EncodeToString(im.Data)},
		})
	}
	model := c.cfg.VisionModel
	if model == "" {
		model = c.cfg.Model
	}
	return c.do(ctx, model, []map[string]any{
		{"role": "system", "content": system},
		{"role": "user", "content": parts},
	}, jsonMode)
}

// jsonSchemaFormat is the loosest schema that still satisfies servers (LM
// Studio) that only accept json_schema: any JSON object.
var jsonSchemaFormat = map[string]any{
	"type": "json_schema",
	"json_schema": map[string]any{
		"name":   "reply",
		"strict": false,
		"schema": map[string]any{"type": "object"},
	},
}

func (c *client) do(ctx context.Context, model string, msgs []map[string]any, jsonMode bool) (string, error) {
	format := strings.ToLower(c.cfg.ResponseFormat)
	useFormat := jsonMode && format != "none" && !c.formatRejected.Load()
	out, status, err := c.post(ctx, model, msgs, useFormat, format)
	if err != nil && useFormat && status == http.StatusBadRequest {
		// Some servers reject response_format outright; the prompts already
		// demand JSON, so retry once without it. If that works, remember it
		// so later calls skip the doomed first attempt.
		var rerr error
		out, _, rerr = c.post(ctx, model, msgs, false, format)
		if rerr == nil {
			c.formatRejected.Store(true)
		}
		err = rerr
	}
	return out, err
}

func (c *client) post(ctx context.Context, model string, msgs []map[string]any, withFormat bool, format string) (string, int, error) {
	req := map[string]any{"model": model, "messages": msgs, "temperature": 0.4}
	if withFormat {
		if format == "json_schema" {
			req["response_format"] = jsonSchemaFormat
		} else {
			req["response_format"] = map[string]string{"type": "json_object"}
		}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", 0, err
	}
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/chat/completions"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	hr.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	res, err := c.http.Do(hr)
	if err != nil {
		return "", 0, fmt.Errorf("ai request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", res.StatusCode, fmt.Errorf("ai request: status %d", res.StatusCode)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxResponseBytes)).Decode(&out); err != nil {
		return "", res.StatusCode, fmt.Errorf("ai response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", res.StatusCode, errors.New("ai response: no choices")
	}
	return out.Choices[0].Message.Content, res.StatusCode, nil
}

// ExtractJSON trims code fences and surrounding prose from a model reply and
// returns the outermost JSON object.
func ExtractJSON(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return ""
	}
	return s[start : end+1]
}
