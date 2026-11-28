package client

import (
	"context"

	"github.com/pulseaiclub/phi/internal/core"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/llm/anthropic"
	"github.com/pulseaiclub/phi/internal/llm/gemini"
	"github.com/pulseaiclub/phi/internal/llm/openai"
	"github.com/pulseaiclub/phi/internal/llm/openai/responses"
)

// Hooks holds optional per-provider request interceptors. Only the field
// matching cfg.API is invoked; nil means no customization.
type Hooks struct {
	OpenAI          llm.RequestInterceptor[openai.Request]
	OpenAIResponses llm.RequestInterceptor[responses.Request]
	Anthropic       llm.RequestInterceptor[anthropic.AnthropicRequest]
	Gemini          llm.RequestInterceptor[gemini.GeminiRequest]
}

// ThinkingResolver reports the effective ThinkConfig when a preset hook forces
// thinking on despite a disabled config, so request builders preserve signed
// thinking history before Before runs.
type ThinkingResolver interface {
	EffectiveThinking(cfg core.ModelConfig) core.ThinkConfig
}

func applyHook[Req any](ctx context.Context, h llm.RequestInterceptor[Req], req *Req, cfg core.ModelConfig) error {
	if h == nil {
		return nil
	}
	return h.Before(ctx, req, cfg)
}
