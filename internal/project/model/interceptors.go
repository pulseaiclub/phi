package model

import (
	"context"

	"github.com/pulseaiclub/phi/internal/core"
	"github.com/pulseaiclub/phi/internal/llm/anthropic"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/llm/gemini"
	"github.com/pulseaiclub/phi/internal/llm/openai/responses"
)

// responsesNoneOff maps disabled thinking to reasoning.effort="none" on
// Responses models that default to medium when reasoning is omitted.
type responsesNoneOff struct{}

func (responsesNoneOff) Before(_ context.Context, req *responses.Request, cfg core.ModelConfig) error {
	if !cfg.Think.Enabled || cfg.Think.Mode == core.Off {
		req.Reasoning = &responses.Reasoning{Effort: "none"}
	}
	return nil
}

func responsesNoneOffHooks() llmclient.Hooks {
	return llmclient.Hooks{OpenAIResponses: responsesNoneOff{}}
}

// claudeAdaptiveThinking maps ThinkConfig to Anthropic adaptive thinking + effort.
type claudeAdaptiveThinking struct {
	forced     bool
	allowXHigh bool
}

func (h claudeAdaptiveThinking) Before(_ context.Context, req *anthropic.AnthropicRequest, cfg core.ModelConfig) error {
	req.ApplyAdaptiveThinking(cfg.Think, h.forced, h.allowXHigh)
	return nil
}

func claudeAdaptiveHooks(forced, allowXHigh bool) llmclient.Hooks {
	return llmclient.Hooks{Anthropic: claudeAdaptiveThinking{forced: forced, allowXHigh: allowXHigh}}
}

// geminiBudgetThinking maps ThinkMode → thinkingBudget (Gemini 2.x).
type geminiBudgetThinking struct{}

func (geminiBudgetThinking) Before(_ context.Context, req *gemini.GeminiRequest, cfg core.ModelConfig) error {
	req.ApplyBudgetThinking(cfg.Think)
	return nil
}

// geminiLevelThinking maps ThinkMode → thinkingLevel (Gemini 3+/4).
// offLevel is the minimum level; allowXHigh enables "XHIGH" on Gemini 4 models.
type geminiLevelThinking struct {
	offLevel   string
	allowXHigh bool
}

func (h geminiLevelThinking) Before(_ context.Context, req *gemini.GeminiRequest, cfg core.ModelConfig) error {
	req.ApplyLevelThinking(cfg.Think, h.offLevel, h.allowXHigh)
	return nil
}

func geminiBudgetHooks() llmclient.Hooks {
	return llmclient.Hooks{Gemini: geminiBudgetThinking{}}
}

func geminiLevelHooks(offLevel string, allowXHigh bool) llmclient.Hooks {
	return llmclient.Hooks{Gemini: geminiLevelThinking{offLevel: offLevel, allowXHigh: allowXHigh}}
}
