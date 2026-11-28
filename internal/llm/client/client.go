package client

import (
	"context"
	"iter"
	"net/http"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/llm/anthropic"
	"github.com/pulseaiclub/phi/internal/llm/gemini"
	"github.com/pulseaiclub/phi/internal/llm/openai"
	"github.com/pulseaiclub/phi/internal/llm/openai/responses"
	"github.com/pulseaiclub/phi/internal/util"
)

// Client talks to the configured LLM endpoint: OpenAI chat-completions by
// default, or Anthropic / Gemini / OpenAI Responses when cfg.API is set.
type Client struct {
	httpClient *http.Client
	cfg        llm.ModelConfig
	hooks      Hooks
	tools      []llm.ToolDefinition
	system     string
}

// NewClient builds a streaming chat client.
func NewClient(cfg llm.ModelConfig, hooks Hooks, tools []llm.ToolDefinition, systemPrompt string) *Client {
	return &Client{
		httpClient: util.DefaultHTTPClient(),
		cfg:        cfg,
		hooks:      hooks,
		tools:      tools,
		system:     systemPrompt,
	}
}

// Stream runs a streaming chat completion over messages (+ optional system prompt / tools).
// All providers validate overrides before network I/O.
func (c *Client) Stream(ctx context.Context, messages []llm.Message) iter.Seq2[llm.StreamEvent, error] {
	switch c.cfg.API {
	case llm.Anthropic:
		cfg := c.cfg
		if c.hooks.AnthropicRequiresThinking && (!cfg.Think.Enabled || cfg.Think.Mode == llm.Off) {
			// Adaptive thinking has no fixed token budget. The preset selects the fallback effort.
			cfg.Think = llm.ThinkConfig{Enabled: true}
		}
		req := anthropic.BuildRequest(cfg, c.system, messages, c.tools)
		if err := applyHook(ctx, c.hooks.Anthropic, &req, c.cfg); err != nil {
			return errorSeq(err)
		}
		return anthropic.Stream(ctx, c.httpClient, c.cfg, &req)
	case llm.Gemini:
		req := gemini.BuildRequest(c.system, messages, c.tools)
		if err := c.applyGeminiThinking(ctx, &req); err != nil {
			return errorSeq(err)
		}
		return gemini.Stream(ctx, c.httpClient, c.cfg, &req)
	case llm.OpenAIResponses:
		req := responses.BuildRequest(c.cfg, c.system, messages, c.tools)
		if err := applyHook(ctx, c.hooks.OpenAIResponses, req, c.cfg); err != nil {
			return errorSeq(err)
		}
		return responses.Stream(ctx, c.httpClient, c.cfg, req)
	default: // llm.OpenAI or empty — both route to OpenAI-compatible chat completions
		req := openai.BuildRequest(c.cfg, c.system, messages, c.tools)
		if err := applyHook(ctx, c.hooks.OpenAI, req, c.cfg); err != nil {
			return errorSeq(err)
		}
		return openai.StreamChatCompletion(ctx, c.httpClient, c.cfg, req)
	}
}

// Compact sends a single non-streaming chat request and returns the
// assistant text. It satisfies llm.Compactor for session compaction.
func (c *Client) Compact(ctx context.Context, req llm.CompactRequest) (llm.CompactResult, error) {
	switch c.cfg.API {
	case llm.Anthropic:
		antReq := anthropic.NewCompactRequest(c.cfg.Name, req.Prompt, req.MaxTokens)
		if err := applyHook(ctx, c.hooks.Anthropic, antReq, c.cfg); err != nil {
			return llm.CompactResult{}, err
		}
		return anthropic.CompactRequest(ctx, c.httpClient, c.cfg, antReq)
	case llm.Gemini:
		greReq := gemini.BuildRequest("", []llm.Message{{Role: llm.RoleUser, Content: req.Prompt}}, nil)
		greReq.SetMaxOutputTokens(req.MaxTokens)
		if err := c.applyGeminiThinking(ctx, &greReq); err != nil {
			return llm.CompactResult{}, err
		}
		return gemini.CompactRequest(ctx, c.httpClient, c.cfg, &greReq)
	case llm.OpenAIResponses:
		respReq := responses.NewCompactRequest(c.cfg, req.Prompt, req.MaxTokens)
		if err := applyHook(ctx, c.hooks.OpenAIResponses, respReq, c.cfg); err != nil {
			return llm.CompactResult{}, err
		}
		return responses.CompactRequest(ctx, c.httpClient, c.cfg, respReq)
	default:
		oaiReq := openai.NewCompactRequest(c.cfg, req.Prompt, req.MaxTokens)
		if err := applyHook(ctx, c.hooks.OpenAI, oaiReq, c.cfg); err != nil {
			return llm.CompactResult{}, err
		}
		return openai.CompactRequest(ctx, c.httpClient, c.cfg, oaiReq)
	}
}

// applyGeminiThinking runs the Gemini hook. Without a preset hook, budget
// style is the safe default for unknown models.
func (c *Client) applyGeminiThinking(ctx context.Context, req *gemini.GeminiRequest) error {
	if c.hooks.Gemini != nil {
		return applyHook(ctx, c.hooks.Gemini, req, c.cfg)
	}
	req.ApplyBudgetThinking(c.cfg.Think)
	return nil
}

func errorSeq(err error) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.StreamEvent{}, err)
	}
}
