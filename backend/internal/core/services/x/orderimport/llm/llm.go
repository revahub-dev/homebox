// Package llm is the fallback extractor for order emails the deterministic
// parsers can't read, and the vision extractor for receipt/screenshot photos.
// It talks to any OpenAI-compatible /chat/completions endpoint and validates
// the returned JSON before anything reaches the review inbox.
package llm

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
	"time"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/orderimport/parsers"
)

// Extractor turns unstructured order content into an Order.
type Extractor interface {
	ExtractText(ctx context.Context, subject, text string) (*parsers.Order, error)
	ExtractImage(ctx context.Context, mimeType string, data []byte) (*parsers.Order, error)
}

// Config for the OpenAI-compatible client. The extractor is disabled when
// BaseURL or Model is empty.
type Config struct {
	BaseURL     string
	APIKey      string
	Model       string
	VisionModel string // defaults to Model
	Timeout     time.Duration
}

// Enabled reports whether enough is configured to call an endpoint.
func (c Config) Enabled() bool { return c.BaseURL != "" && c.Model != "" }

// New returns an Extractor, or nil when the config is not enabled.
func New(cfg Config) Extractor {
	if !cfg.Enabled() {
		return nil
	}
	if cfg.VisionModel == "" {
		cfg.VisionModel = cfg.Model
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	return &client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}
}

const (
	maxPromptChars   = 20000
	maxResponseBytes = 1 << 20
	maxItems         = 100
)

const systemPrompt = `You extract purchase data from Japanese or English online-store order confirmations and receipts.
Reply with a single JSON object and nothing else, using exactly this shape:
{"store":"","order_number":"","order_date":"YYYY-MM-DD","total":0,"currency":"JPY",
 "items":[{"name":"","quantity":1,"unit_price":0,"asin":"","jan":"","model_number":""}]}
Rules: include only purchased products (not shipping, fees, points or discounts). unit_price is per unit.
Use "" or 0 when unknown. Never invent identifiers. If nothing was purchased, return "items": [].`

type client struct {
	cfg  Config
	http *http.Client
}

type chatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

func (c *client) ExtractText(ctx context.Context, subject, text string) (*parsers.Order, error) {
	if len(text) > maxPromptChars {
		text = text[:maxPromptChars]
	}
	user := "Subject: " + subject + "\n\n" + text
	return c.complete(ctx, c.cfg.Model, []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: user},
	})
}

func (c *client) ExtractImage(ctx context.Context, mimeType string, data []byte) (*parsers.Order, error) {
	url := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
	return c.complete(ctx, c.cfg.VisionModel, []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: []contentPart{
			{Type: "text", Text: "Extract the purchase from this receipt or order screenshot."},
			{Type: "image_url", ImageURL: &imageURL{URL: url}},
		}},
	})
}

func (c *client) complete(ctx context.Context, model string, msgs []chatMessage) (*parsers.Order, error) {
	body, err := json.Marshal(map[string]any{
		"model":           model,
		"messages":        msgs,
		"temperature":     0,
		"response_format": map[string]string{"type": "json_object"},
	})
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm request: status %d", res.StatusCode)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxResponseBytes)).Decode(&out); err != nil {
		return nil, fmt.Errorf("llm response: %w", err)
	}
	if len(out.Choices) == 0 {
		return nil, errors.New("llm response: no choices")
	}
	return Validate(out.Choices[0].Message.Content)
}
