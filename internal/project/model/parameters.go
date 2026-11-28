package model

import (
	"context"
	"net/url"
	"strings"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/llm/anthropic"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/llm/openai"
)

type chatParameters struct {
	forced bool
}

func (h chatParameters) Before(_ context.Context, req *openai.Request, cfg llm.ModelConfig) error {
	endpoint := NormalizeEndpoint(cfg.BaseURL)
	if isOpenCodeChat(cfg.API, endpoint) {
		req.Thinking = nil
		return nil
	}
	req.Thinking = &openai.ThinkingConfig{Type: "enabled"}
	if !h.forced && !cfg.Think.Enabled {
		req.Thinking.Type = "disabled"
	}
	// Enable preserved thinking only for GLM on native APIs that accept it.
	if h.forced {
		switch endpoint {
		case "https://api.z.ai/api/coding/paas/v4", "https://api.z.ai/api/paas/v4",
			"https://open.bigmodel.cn/api/coding/paas/v4", "https://open.bigmodel.cn/api/paas/v4":
			preserve := false
			req.Thinking.ClearThinking = &preserve
		}
	}
	return nil
}

func chatHooks(forced bool) llmclient.Hooks {
	return llmclient.Hooks{OpenAI: chatParameters{forced: forced}, Anthropic: vendorMessagesParameters{forced: forced}}
}

type vendorMessagesParameters struct {
	forced bool
}

func (h vendorMessagesParameters) EffectiveThinking(cfg llm.ModelConfig) llm.ThinkConfig {
	if h.forced && !isOpenRouterAnthropic(cfg.BaseURL) && (!cfg.Think.Enabled || cfg.Think.Mode == llm.Off) {
		return llm.ThinkConfig{Enabled: true, Mode: llm.Low}
	}
	return cfg.Think
}

func (h vendorMessagesParameters) Before(
	_ context.Context,
	req *anthropic.AnthropicRequest,
	cfg llm.ModelConfig,
) error {
	adaptive := isOpenRouterAnthropic(cfg.BaseURL)
	req.ApplyVendorThinking(cfg.Think, h.forced && !adaptive, adaptive)
	return nil
}

func isOpenRouterAnthropic(baseURL string) bool {
	switch NormalizeEndpoint(baseURL) {
	case "https://openrouter.ai/api", "https://openrouter.ai/api/v1":
		return true
	default:
		return false
	}
}

func isOpenCodeChat(api llm.RouterType, endpoint string) bool {
	return (api == "" || api == llm.OpenAI) && endpoint == "https://opencode.ai/zen/go/v1"
}

// NormalizeEndpoint canonicalizes hosts and ports, treating base and Chat URLs alike.
// Credentials, queries and fragments exclude native rules; paths remain exact.
func NormalizeEndpoint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		u.Host = u.Hostname()
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = strings.TrimRight(u.RawPath, "/")
	u.Path = strings.TrimSuffix(u.Path, "/chat/completions")
	u.RawPath = strings.TrimSuffix(u.RawPath, "/chat/completions")
	return u.String()
}
