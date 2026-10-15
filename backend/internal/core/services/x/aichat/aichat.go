// Package aichat is a minimal OpenAI-compatible chat client shared by the
// fork's AI features ("What can I build?", sale-listing drafts). It works with
// any /chat/completions endpoint, including local Ollama or llama.cpp.
package aichat

// Fork addition. See FORK_NOTES.md.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrDisabled is returned by callers when no endpoint is configured.
var ErrDisabled = errors.New("ai: no LLM endpoint configured")

// Chatter sends one system+user exchange and returns the assistant text.
// With jsonMode the endpoint is asked for a JSON object.
type Chatter interface {
	Chat(ctx context.Context, system, user string, jsonMode bool) (string, error)
}

// Config for the OpenAI-compatible endpoint.
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
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
}

func (c *client) Chat(ctx context.Context, system, user string, jsonMode bool) (string, error) {
	req := map[string]any{
		"model": c.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0.4,
	}
	if jsonMode {
		req["response_format"] = map[string]string{"type": "json_object"}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/chat/completions"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	hr.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	res, err := c.http.Do(hr)
	if err != nil {
		return "", fmt.Errorf("ai request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ai request: status %d", res.StatusCode)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxResponseBytes)).Decode(&out); err != nil {
		return "", fmt.Errorf("ai response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("ai response: no choices")
	}
	return out.Choices[0].Message.Content, nil
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
