package client

import (
	"context"
	"iter"
	"net/http"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/llm/anthropic"
	"github.com/pulseaiclub/phi/internal/llm/openai"
	"github.com/pulseaiclub/phi/internal/util"
)

// Client talks to the configured LLM endpoint: the OpenAI-compatible
// /chat/completions API by default, or the Anthropic Messages API when the
// config targets Anthropic.
type Client struct {
	httpClient *http.Client
	cfg        llm.ModelConfig
	tools      []llm.ToolDefinition
	system     string
	provider   llm.Provider
}

// NewClient builds a streaming chat client.
func NewClient(cfg llm.ModelConfig, tools []llm.ToolDefinition, systemPrompt string) *Client {
	return &Client{
		httpClient: util.DefaultHTTPClient(),
		cfg:        cfg,
		tools:      tools,
		system:     systemPrompt,
		provider:   llm.ResolveProvider(cfg),
	}
}

// Stream runs a streaming chat completion over messages (+ optional system prompt / tools).
func (c *Client) Stream(ctx context.Context, messages []llm.Message) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		if c.provider == llm.ProviderAnthropic {
			req := anthropic.BuildRequest(c.cfg, c.system, messages, c.tools)
			for ev, err := range anthropic.Stream(ctx, c.httpClient, c.cfg, &req) {
				if !yield(ev, err) {
					return
				}
			}
			return
		}
		req := openai.BuildRequest(c.cfg, c.system, messages, c.tools)
		for ev, err := range openai.StreamChatCompletion(ctx, c.httpClient, c.cfg.BaseURL, c.cfg.APIKey, req) {
			if !yield(ev, err) {
				return
			}
		}
	}
}

// Compact sends a single non-streaming chat request and returns the
// assistant text. It satisfies llm.Compactor for session compaction.
func (c *Client) Compact(ctx context.Context, prompt string) (string, error) {
	if c.provider == llm.ProviderAnthropic {
		return anthropic.Compact(ctx, c.httpClient, c.cfg, prompt)
	}
	return openai.Compact(ctx, c.httpClient, c.cfg, prompt)
}

// isAnthropicProvider reports whether the config resolves to the Anthropic
// Messages API. It remains as a small package-local compatibility helper for
// the routing tests.
func isAnthropicProvider(cfg llm.ModelConfig) bool {
	return llm.ResolveProvider(cfg) == llm.ProviderAnthropic
}
