// Package model holds built-in model presets — connection defaults for the
// models providers advertise, so a config.yaml entry can omit base_url,
// context_window, and image_enabled and still get the right values. Presets
// are keyed by the exact model name the API documents; unknown or legacy
// names fall through to the generic OpenAI defaults the config loader applies.
package model

import (
	"strings"

	"github.com/pulseaiclub/phi/internal/llm"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
)

// Preset is a built-in model catalog entry: connection defaults plus optional
// request Hooks for vendor-specific wire shape.
type Preset struct {
	Config llm.ModelConfig
	Hooks  llmclient.Hooks
}

// Lookup returns the built-in preset for a model name. ok is false for names
// without a preset, so callers fall back to the generic OpenAI endpoint.
// The returned config carries no api_key or skill path; the caller layers
// those on top and may override any field.
func Lookup(name string) (Preset, bool) {
	for _, p := range presets {
		if p.Config.Name == name {
			return p, true
		}
	}
	if cfg, ok := orcaPreset(name); ok {
		return Preset{Config: cfg}, true
	}
	return Preset{}, false
}

// HooksFor returns request hooks from the built-in catalog, or a zero Hooks
// when the name has no preset (or the preset has no interceptors).
func HooksFor(name string) llmclient.Hooks {
	if p, ok := Lookup(name); ok {
		return p.Hooks
	}
	return llmclient.Hooks{}
}

// OrcaRouterProvider is the named provider value a config entry selects with
// `api: OrcaRouter`. It is a first-class route, not a custom base URL: the
// editor shows it in the API dropdown and the model catalog filters by it.
const OrcaRouterProvider = llm.OrcaRouter

// OrcaRouterBaseURL is the inference endpoint every OrcaRouter entry routes to.
const OrcaRouterBaseURL = "https://api.orcarouter.ai/v1"

// orcaPreset resolves an OrcaRouter model name to its connection defaults.
//
// OrcaRouter model IDs keep the vendor namespace ("openai/gpt-5.5"), so the
// prefix identifies the route while the model keeps its own context window,
// image support, and reasoning ladder. A name that matches no verified entry
// still routes to OrcaRouter with conservative defaults rather than falling
// through to api.openai.com, which would send the user's OrcaRouter key to a
// host that does not accept it.
func orcaPreset(name string) (llm.ModelConfig, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return llm.ModelConfig{}, false
	}
	if !isOrcaModelName(name) {
		return llm.ModelConfig{}, false
	}
	cfg := llm.ModelConfig{
		Name:    name,
		BaseURL: OrcaRouterBaseURL,
		API:     OrcaRouterProvider,
	}
	if verified, ok := verifiedOrcaModels[name]; ok {
		cfg.ContextWindow = verified.contextWindow
		cfg.ImageEnabled = verified.imageEnabled
		if verified.think != nil {
			cfg.Think = *verified.think
		}
	}
	// An unknown OrcaRouter model keeps the route but leaves the context window
	// unset, so compaction stays disabled (the safe default), and leaves images
	// off until the catalog proves the model accepts them.
	return cfg, true
}

// isOrcaModelName reports whether a name belongs to the OrcaRouter namespace.
// The vendor prefix is the catalog's own convention; anything else is a user's
// custom model and must not be silently rerouted.
func isOrcaModelName(name string) bool {
	first, _, ok := strings.Cut(name, "/")
	if !ok {
		return false
	}
	switch first {
	case "orcarouter", "openai", "anthropic", "google", "deepseek":
		return true
	}
	return false
}

// verifiedOrcaModel is a verified fallback entry for one OrcaRouter model.
type verifiedOrcaModel struct {
	contextWindow int
	imageEnabled  bool
	// think is the verified reasoning-effort default. nil means the model has
	// no documented reasoning ladder, so none is invented for it.
	think *llm.ThinkConfig
}

// verifiedOrcaModels covers the cold-start seed. An entry is listed only when
// its context window and input modalities are documented; everything else
// keeps the conservative defaults above.
//
// Sources (verified 2026-09-22):
//   - https://api.orcarouter.ai/v1/models — live catalog
//   - https://www.orcarouter.ai — documented model list and reasoning levels
var verifiedOrcaModels = map[string]verifiedOrcaModel{
	"openai/gpt-5.5": {
		contextWindow: 272_000,
		imageEnabled:  true,
		// gpt-5.5 documents low/medium/high/xhigh; high is the default.
		think: &orcaThinkHigh,
	},
	"anthropic/claude-opus-4.8": {
		contextWindow: 200_000,
		imageEnabled:  true,
	},
	"google/gemini-3.5-flash": {
		contextWindow: 1_000_000,
		imageEnabled:  true,
		think:         &orcaThinkHigh,
	},
	"deepseek/deepseek-v4-pro": {
		contextWindow: 1_048_576,
	},
	"deepseek/deepseek-v4-flash": {
		contextWindow: 1_048_576,
	},
	"deepseek/deepseek-v4.1-flash": {
		contextWindow: 1_048_576,
		imageEnabled:  true,
	},
	"deepseek/deepseek-v4-flash-vision-exp": {
		contextWindow: 1_048_576,
		imageEnabled:  true,
	},
	"orcarouter/auto": {},
}

// OrcaAutoModel is the catalog's own auto-routing model, used in the guidance
// printed after a successful connect.
const OrcaAutoModel = "orcarouter/auto"

// OrcaReasoningLevels returns the verified reasoning-effort ladder for an
// OrcaRouter model, or nil when it has none. It lets a caller restore a
// fallback model without dropping its reasoning metadata.
func OrcaReasoningLevels(name string) []llm.ThinkMode {
	m, ok := verifiedOrcaModels[name]
	if !ok || m.think == nil {
		return nil
	}
	return []llm.ThinkMode{llm.Low, llm.Medium, llm.High, llm.XHigh}
}

// VerifiedOrcaModelIDs returns the verified OrcaRouter fallback identifiers.
func VerifiedOrcaModelIDs() []string {
	out := make([]string, 0, len(verifiedOrcaModels))
	for name := range verifiedOrcaModels {
		out = append(out, name)
	}
	return out
}

var (
	orcaThinkHigh = llm.ThinkConfig{Enabled: true, Mode: llm.High}
	thinkHigh     = llm.ThinkConfig{Enabled: true, Mode: llm.High}
	thinkMax      = llm.ThinkConfig{Enabled: true, Mode: llm.Max}
)

// presets is the built-in catalog, keyed by model name. Values mirror each
// provider's public API docs; re-check the linked page when refreshing a
// model — context length, base URL, and capabilities change between versions.
var presets = []Preset{
	// Source: https://platform.openai.com/docs/models
	{
		Config: llm.ModelConfig{
			Name:          "gpt-6-astra",
			BaseURL:       "https://api.openai.com/v1",
			ContextWindow: 272_000,
			ImageEnabled:  true,
			API:           llm.OpenAIResponses,
			Think:         thinkMax,
		},
	},
	{
		Config: llm.ModelConfig{
			Name:          "gpt-5.6-sol",
			BaseURL:       "https://api.openai.com/v1",
			ContextWindow: 272_000,
			ImageEnabled:  true,
			API:           llm.OpenAIResponses,
			Think:         thinkHigh,
		},
	},
	{
		Config: llm.ModelConfig{
			Name:          "gpt-5.6-terra",
			BaseURL:       "https://api.openai.com/v1",
			ContextWindow: 272_000,
			ImageEnabled:  true,
			API:           llm.OpenAIResponses,
			Think:         thinkHigh,
		},
	},
	{
		Config: llm.ModelConfig{
			Name:          "gpt-5.6-luna",
			BaseURL:       "https://api.openai.com/v1",
			ContextWindow: 272_000,
			ImageEnabled:  true,
			API:           llm.OpenAIResponses,
			Think:         thinkHigh,
		},
	},
	{
		Config: llm.ModelConfig{
			Name:          "gpt-5-chat-latest",
			BaseURL:       "https://api.openai.com/v1",
			ContextWindow: 128_000,
			ImageEnabled:  true,
			API:           llm.OpenAIResponses,
		},
	},
	// Source: https://platform.openai.com/docs/models
	{
		Config: llm.ModelConfig{
			Name:          "gpt-5.5",
			BaseURL:       "https://api.openai.com/v1",
			ContextWindow: 272_000,
			ImageEnabled:  true,
			API:           llm.OpenAIResponses,
			Think:         thinkHigh,
		},
	},
	{
		Config: llm.ModelConfig{
			Name:          "gpt-5.5-pro",
			BaseURL:       "https://api.openai.com/v1",
			ContextWindow: 1_050_000,
			ImageEnabled:  true,
			API:           llm.OpenAIResponses,
			Think:         thinkHigh,
		},
	},
	// Source: https://api-docs.deepseek.com/zh-cn/quick_start/pricing
	{
		Config: llm.ModelConfig{
			Name:          "deepseek-flash",
			BaseURL:       "https://api.deepseek.com",
			ContextWindow: 1_000_000,
			ImageEnabled:  true,
			API:           llm.OpenAI,
			Think:         thinkHigh,
		},
		Hooks: deepseekHooks(),
	},
	{
		Config: llm.ModelConfig{
			Name:          "deepseek-v4-pro",
			BaseURL:       "https://api.deepseek.com",
			ContextWindow: 1_000_000,
			API:           llm.OpenAI,
			Think:         thinkHigh,
		},
		Hooks: deepseekHooks(),
	},
	// Gemini 2.5 — thinkingBudget (token cap). Source: https://ai.google.dev/gemini-api/docs/models
	{
		Config: llm.ModelConfig{
			Name:          "gemini-2.5-pro",
			BaseURL:       "https://generativelanguage.googleapis.com/v1beta",
			ContextWindow: 1_000_000,
			ImageEnabled:  true,
			API:           llm.Gemini,
			Think:         thinkHigh,
		},
		Hooks: geminiBudgetHooks(),
	},
	{
		Config: llm.ModelConfig{
			Name:          "gemini-2.5-flash",
			BaseURL:       "https://generativelanguage.googleapis.com/v1beta",
			ContextWindow: 1_000_000,
			ImageEnabled:  true,
			API:           llm.Gemini,
			Think:         thinkHigh,
		},
		Hooks: geminiBudgetHooks(),
	},
	// Gemini 3 — thinkingLevel. Pro floor LOW; Flash floor MINIMAL. Neither can fully disable.
	{
		Config: llm.ModelConfig{
			Name:          "gemini-3-pro",
			BaseURL:       "https://generativelanguage.googleapis.com/v1beta",
			ContextWindow: 1_000_000,
			ImageEnabled:  true,
			API:           llm.Gemini,
			Think:         thinkHigh,
		},
		Hooks: geminiLevelHooks("LOW"),
	},
	{
		Config: llm.ModelConfig{
			Name:          "gemini-3-flash",
			BaseURL:       "https://generativelanguage.googleapis.com/v1beta",
			ContextWindow: 1_000_000,
			ImageEnabled:  true,
			API:           llm.Gemini,
			Think:         thinkHigh,
		},
		Hooks: geminiLevelHooks("MINIMAL"),
	},
	{
		Config: llm.ModelConfig{
			Name:          "kimi-k3",
			BaseURL:       "https://api.moonshot.cn/v1",
			ContextWindow: 1_000_000,
			ImageEnabled:  true,
			API:           llm.OpenAI,
			Think:         thinkMax,
		},
	},
	{
		Config: llm.ModelConfig{
			Name:          "kimi-k2.7-code",
			BaseURL:       "https://api.moonshot.cn/v1",
			ContextWindow: 10_000_000,
			ImageEnabled:  true,
			API:           llm.OpenAI,
		},
	},
	// GLM on the z.ai coding plan. Both presets are forced-thinking, and
	// thinking rides in extra_body.thinking with clear_thinking false (Preserved
	// Thinking), so reasoning survives across turns.
	// Sources: https://docs.z.ai/api-reference/llm/chat-completion
	//          https://docs.z.ai/guides/capabilities/thinking
	{
		Config: llm.ModelConfig{
			Name:          "glm-5.3-flash",
			BaseURL:       "https://api.z.ai/api/coding/paas/v4",
			ContextWindow: 1_048_576,
			ImageEnabled:  true,
			API:           llm.OpenAI,
			Think:         thinkMax,
		},
		Hooks: glmHooks(),
	},
	{
		Config: llm.ModelConfig{
			Name:          "glm-5.3",
			BaseURL:       "https://api.z.ai/api/coding/paas/v4",
			ContextWindow: 1_048_576,
			API:           llm.OpenAI,
			Think:         thinkMax,
		},
		Hooks: glmHooks(),
	},
}
