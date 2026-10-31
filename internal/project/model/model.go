// Package model supplies presets and request hooks for native and gateway model IDs.
package model

import (
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/pulseaiclub/phi/internal/llm"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
)

// Preset holds model defaults, request hooks and an optional gateway alias pattern.
type Preset struct {
	Config        llm.ModelConfig
	Hooks         llmclient.Hooks
	Alias         string
	thinkModes    []llm.ThinkMode
	openCodeModes []llm.ThinkMode
}

// Lookup inherits capabilities and hooks for aliases, keeping native connections separate.
func Lookup(name string) (Preset, bool) {
	for _, p := range presets {
		if strings.EqualFold(p.Config.Name, name) {
			p.Config.Name = name
			return p, true
		}
	}
	// A model-like namespace must not override a later model segment. Slashes
	// inside colon or bracket qualifiers belong to the qualifier, not the route.
	route := name
	if i := strings.IndexAny(route, ":["); i >= 0 {
		route = route[:i]
	}
	for {
		start := strings.LastIndexByte(route, '/') + 1
		for _, rule := range aliasRules() {
			if rule.match.MatchString(name[start:]) {
				p := rule.preset
				p.Config.Name = name
				p.Config.BaseURL, p.Config.API = "", ""
				return p, true
			}
		}
		if start == 0 {
			break
		}
		route = route[:start-1]
	}
	return Preset{}, false
}

type aliasRule struct {
	match  *regexp.Regexp
	preset Preset
}

// LookupBound uses bound when set; a different name inherits capabilities without connection defaults.
func LookupBound(bound, name string) (Preset, bool) {
	identity := name
	if bound != "" {
		identity = bound
	}
	if identity == "" {
		return Preset{}, false
	}
	p, ok := Lookup(identity)
	if ok && bound != "" && !strings.EqualFold(name, bound) {
		p.Config.BaseURL, p.Config.API = "", ""
	}
	return p, ok
}

// Specific variants take precedence over their families. Only documented
// routing prefixes, build suffixes and gateway qualifiers are interchangeable.
// A build suffix is a date (-20250901, -2025-09-01) or a four-digit tag
// (-1017) that some routes pin; any other trailing text is a different model.
var aliasRules = sync.OnceValue(buildAliasRules)

func buildAliasRules() []aliasRule {
	rules := make([]aliasRule, 0, len(presets))
	for _, p := range presets {
		pattern := p.Alias
		if pattern == "" {
			pattern = regexp.QuoteMeta(p.Config.Name)
		}
		rules = append(rules, aliasRule{
			match:  regexp.MustCompile(`(?i)^` + pattern + `(?:-\d{8}|-\d{4}-\d{2}-\d{2}|-\d{4})?(?:[:/\[].*)?$`),
			preset: p,
		})
	}
	sort.SliceStable(rules, func(i, j int) bool {
		return len(rules[i].preset.Config.Name) > len(rules[j].preset.Config.Name)
	})
	return rules
}

// HooksFor returns preset request hooks, or empty hooks for unknown names.
func HooksFor(name string) llmclient.Hooks {
	if p, ok := Lookup(name); ok {
		return p.Hooks
	}
	return llmclient.Hooks{}
}

// HooksForConfig honors explicit preset bindings for opaque gateway IDs.
func HooksForConfig(cfg llm.ModelConfig) llmclient.Hooks {
	if cfg.Preset != "" {
		return HooksFor(cfg.Preset)
	}
	return HooksFor(cfg.Name)
}

var (
	thinkHigh = llm.ThinkConfig{Enabled: true, Mode: llm.High}
	thinkMax  = llm.ThinkConfig{Enabled: true, Mode: llm.Max}
)

// presets is the built-in catalog, keyed by model name. Values mirror each
// provider's public API docs; re-check the linked page when refreshing a
// model — context length, base URL, and capabilities change between versions.
// MaxTokens (output limit) is set only where a source states it; presets
// without it fall back to the compaction default. GPT values come from each
// model's page under the OpenAI source below; DeepSeek, Kimi and GLM follow the
// limits ZCode ships for the same models.
var presets = []Preset{
	// Source: https://platform.openai.com/docs/models
	{
		Config: llm.ModelConfig{
			Name:          "gpt-6-astra",
			BaseURL:       "https://api.openai.com/v1",
			ContextWindow: 272_000,
			MaxTokens:     128_000,
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
			MaxTokens:     128_000,
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
			MaxTokens:     128_000,
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
			MaxTokens:     128_000,
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
			MaxTokens:     384_000,
			ImageEnabled:  true,
			API:           llm.OpenAI,
			Think:         thinkHigh,
		},
		Hooks:         chatHooks(false),
		thinkModes:    []llm.ThinkMode{llm.Off, llm.Low, llm.High, llm.Max},
		openCodeModes: []llm.ThinkMode{llm.Low, llm.High, llm.Max},
		// V4.1 Flash shares the Flash recommendation under both separators.
		Alias: `deepseek-(?:v4[.\-]1-)?flash`,
	},
	{
		Config: llm.ModelConfig{
			Name:          "deepseek-v4-pro",
			BaseURL:       "https://api.deepseek.com",
			ContextWindow: 1_000_000,
			MaxTokens:     384_000,
			API:           llm.OpenAI,
			Think:         thinkHigh,
		},
		Hooks:         chatHooks(false),
		thinkModes:    []llm.ThinkMode{llm.Off, llm.Low, llm.High, llm.Max},
		openCodeModes: []llm.ThinkMode{llm.High, llm.Max},
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
			MaxTokens:     131_072,
			ImageEnabled:  true,
			API:           llm.OpenAI,
			Think:         thinkMax,
		},
		Alias:      `(?:auto-)?kimi-k3`,
		thinkModes: []llm.ThinkMode{llm.Low, llm.High, llm.Max},
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
	// GLM on the z.ai coding plan. Both presets are forced-thinking; endpoint
	// rules enable preserved history only on known native APIs.
	// Sources: https://docs.z.ai/api-reference/llm/chat-completion
	//          https://docs.z.ai/guides/capabilities/thinking
	{
		Config: llm.ModelConfig{
			Name:          "glm-5.3-flash",
			BaseURL:       "https://api.z.ai/api/coding/paas/v4",
			ContextWindow: 1_048_576,
			MaxTokens:     128_000,
			ImageEnabled:  true,
			API:           llm.OpenAI,
			Think:         thinkMax,
		},
		Hooks:      chatHooks(true),
		thinkModes: []llm.ThinkMode{llm.Low, llm.High, llm.Max},
		// group/auto-glm-5-3-flash routes with a dash for the version dot.
		Alias: `(?:auto-)?glm-5[.\-]3-flash`,
	},
	{
		Config: llm.ModelConfig{
			Name:          "glm-5.3",
			BaseURL:       "https://api.z.ai/api/coding/paas/v4",
			ContextWindow: 1_048_576,
			MaxTokens:     128_000,
			API:           llm.OpenAI,
			Think:         thinkMax,
		},
		Hooks:      chatHooks(true),
		thinkModes: []llm.ThinkMode{llm.Low, llm.High, llm.Max},
		Alias:      `(?:auto-)?glm-5[.\-]3`,
	},
}
