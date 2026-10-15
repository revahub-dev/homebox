package vision

import (
	"context"
	"errors"
	"fmt"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/aichat"
)

// OpenAI is a Provider for any OpenAI-compatible /chat/completions endpoint
// that accepts image_url content: OpenAI and compatible clouds, Ollama
// (http://host:11434/v1), LM Studio (http://host:1234/v1), llama.cpp server.
type OpenAI struct {
	chat aichat.VisionChatter
	name string
}

// NewOpenAI wraps a vision-capable chat client. Returns nil when chat is nil
// or cannot do vision (callers treat nil as "disabled").
func NewOpenAI(chat aichat.Chatter, name string) *OpenAI {
	vc, ok := chat.(aichat.VisionChatter)
	if !ok || vc == nil {
		return nil
	}
	if name == "" {
		name = "openai-compatible"
	}
	return &OpenAI{chat: vc, name: name}
}

func (o *OpenAI) Name() string { return o.name }

func (o *OpenAI) Detect(ctx context.Context, req DetectRequest) (DetectResult, error) {
	if !req.Mode.Valid() {
		req.Mode = ModeItem
	}
	if len(req.Image) == 0 {
		return DetectResult{}, errors.New("vision: empty image")
	}
	mime := req.MIME
	if mime == "" {
		mime = "image/jpeg"
	}
	reply, err := o.chat.ChatVision(ctx, systemPrompt, userPrompt(req), []aichat.Image{{MIME: mime, Data: req.Image}}, true)
	if err != nil {
		return DetectResult{}, fmt.Errorf("vision request: %w", err)
	}
	return ParseReply(reply, req.Mode, req.Width, req.Height)
}
