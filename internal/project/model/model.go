// Package model supplies presets and request hooks for native and gateway model IDs.
package model

import (
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/pulseaiclub/phi/internal/core"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
)

// Preset holds model defaults, request hooks and an optional gateway alias pattern.
type Preset struct {
	Config        core.ModelConfig
	Hooks         llmclient.Hooks
	Alias         string
	thinkModes    []core.ThinkMode
	openCodeModes []core.ThinkMode
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
func HooksForConfig(cfg core.ModelConfig) llmclient.Hooks {
	if cfg.Preset != "" {
		return HooksFor(cfg.Preset)
	}
	return HooksFor(cfg.Name)
}

var (
	thinkMedium          = core.ThinkConfig{Enabled: true, Mode: core.Medium}
	thinkHigh            = core.ThinkConfig{Enabled: true, Mode: core.High}
	thinkMax             = core.ThinkConfig{Enabled: true, Mode: core.Max}
	lowToMaxModes        = []core.ThinkMode{core.Low, core.Medium, core.High, core.XHigh, core.Max}
	offLowToMaxModes     = []core.ThinkMode{core.Off, core.Low, core.Medium, core.High, core.XHigh, core.Max}
	claude46Modes        = []core.ThinkMode{core.Off, core.Low, core.Medium, core.High, core.Max}
	geminiLowToHighModes = []core.ThinkMode{core.Low, core.Medium, core.High}
	gemini4XHighModes    = []core.ThinkMode{core.Low, core.Medium, core.High, core.XHigh}
)

// withSyncedSpec fills zero ContextWindow, InputLimit, and MaxTokens from
// generated models.dev specs (preserving non-zero overrides) and sets ImageEnabled.
// An explicit ContextWindow override skips the synced InputLimit so the two
// limits never come from different tiers.
func withSyncedSpec(cfg core.ModelConfig) core.ModelConfig {
	spec, ok := syncedSpecs[cfg.Name]
	if !ok {
		return cfg
	}
	if cfg.ContextWindow == 0 {
		cfg.ContextWindow = spec.ContextWindow
		if cfg.InputLimit == 0 {
			cfg.InputLimit = spec.InputLimit
		}
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = spec.MaxTokens
	}
	cfg.ImageEnabled = spec.ImageEnabled
	return cfg
}

func claudePreset(name, alias string, forced, allowXHigh bool, modes []core.ThinkMode) Preset {
	return Preset{
		Config: withSyncedSpec(core.ModelConfig{
			Name:    name,
			BaseURL: "https://api.anthropic.com",
			API:     core.Anthropic,
			Think:   thinkHigh,
		}),
		Hooks:      claudeAdaptiveHooks(forced, allowXHigh),
		thinkModes: modes,
		Alias:      alias,
	}
}

func openAIPreset(name string, think core.ThinkConfig, hooks llmclient.Hooks, modes ...core.ThinkMode) Preset {
	return Preset{
		Config: withSyncedSpec(core.ModelConfig{
			Name:    name,
			BaseURL: "https://api.openai.com/v1",
			API:     core.OpenAIResponses,
			Think:   think,
		}),
		Hooks:      hooks,
		thinkModes: modes,
	}
}

func geminiPreset(name string, think core.ThinkConfig, hooks llmclient.Hooks, modes ...core.ThinkMode) Preset {
	return Preset{
		Config: withSyncedSpec(core.ModelConfig{
			Name:    name,
			BaseURL: "https://generativelanguage.googleapis.com/v1beta",
			API:     core.Gemini,
			Think:   think,
		}),
		Hooks:      hooks,
		thinkModes: modes,
	}
}

func kimiPreset(name, alias string) Preset {
	return Preset{
		Config: withSyncedSpec(core.ModelConfig{
			Name:    name,
			BaseURL: "https://api.moonshot.cn/v1",
			// Moonshot's models.dev entries set limit.output equal to the 1M context window.
			MaxTokens: 131_072,
			API:       core.OpenAI,
			Think:     thinkMax,
		}),
		Alias:      alias,
		thinkModes: []core.ThinkMode{core.Low, core.High, core.Max},
	}
}

func glmPreset(name, alias string) Preset {
	return Preset{
		Config: withSyncedSpec(core.ModelConfig{
			Name:    name,
			BaseURL: "https://api.z.ai/api/coding/paas/v4",
			API:     core.OpenAI,
			Think:   thinkMax,
		}),
		Hooks:      chatHooks(true),
		thinkModes: []core.ThinkMode{core.Low, core.High, core.Max},
		Alias:      alias,
	}
}

// presets is the built-in catalog, keyed by model name. Entries mapped in
// sources.json pull ContextWindow, InputLimit, MaxTokens, and ImageEnabled from
// specs_gen.go (`make sync-models`); non-zero limit fields below act as
// explicit overrides. Connection defaults, thinking hooks, and alias rules
// remain hand-maintained.
var presets = []Preset{
	// Claude Fable 5.2 and Opus/Sonnet 5.5 use forced adaptive thinking; Haiku 5.5
	// and Claude 4.6 accept thinking.type=disabled (4.6 omits xhigh).
	// Source: https://platform.claude.com/docs/en/build-with-claude/thinking
	claudePreset("claude-fable-5-2", `claude-fable-5[.\-]2`, true, true, lowToMaxModes),
	claudePreset("claude-opus-5-5", `claude-opus-5[.\-]5`, true, true, lowToMaxModes),
	claudePreset("claude-sonnet-5-5", `claude-sonnet-5[.\-]5`, true, true, lowToMaxModes),
	claudePreset("claude-haiku-5-5", `claude-haiku-5[.\-]5`, false, true, offLowToMaxModes),
	claudePreset("claude-opus-4-6", `claude-opus-4[.\-]6`, false, false, claude46Modes),
	claudePreset("claude-sonnet-4-6", `claude-sonnet-4[.\-]6`, false, false, claude46Modes),

	// Source: https://developers.openai.com/api/docs/models
	openAIPreset("gpt-6.2-astra", thinkMax, llmclient.Hooks{}, lowToMaxModes...),
	openAIPreset("gpt-6.2-sol", thinkHigh, llmclient.Hooks{}, lowToMaxModes...),
	openAIPreset("gpt-6.1-sol", thinkHigh, llmclient.Hooks{}, lowToMaxModes...),
	openAIPreset("gpt-6.1-terra", thinkHigh, responsesNoneOffHooks(), offLowToMaxModes...),
	openAIPreset("gpt-6-astra", thinkMax, llmclient.Hooks{}),
	openAIPreset("gpt-6-luna", thinkHigh, responsesNoneOffHooks(), offLowToMaxModes...),
	openAIPreset("gpt-5.6-sol", thinkHigh, llmclient.Hooks{}),
	openAIPreset("gpt-5.6-terra", thinkHigh, llmclient.Hooks{}),
	openAIPreset("gpt-5.6-luna", thinkHigh, llmclient.Hooks{}),
	// Retired upstream and absent from models.dev; retained for existing configs.
	{
		Config: core.ModelConfig{
			Name:          "gpt-5-chat-latest",
			BaseURL:       "https://api.openai.com/v1",
			ContextWindow: 128_000,
			ImageEnabled:  true,
			API:           core.OpenAIResponses,
		},
	},
	openAIPreset("gpt-5.5", thinkHigh, llmclient.Hooks{}),
	openAIPreset("gpt-5.5-pro", thinkHigh, llmclient.Hooks{}),

	// Source: https://api-docs.deepseek.com/zh-cn/quick_start/pricing
	{
		Config: withSyncedSpec(core.ModelConfig{
			Name:    "deepseek-flash",
			BaseURL: "https://api.deepseek.com",
			API:     core.OpenAI,
			Think:   thinkHigh,
		}),
		Hooks:         chatHooks(false),
		thinkModes:    []core.ThinkMode{core.Off, core.Low, core.High, core.Max},
		openCodeModes: []core.ThinkMode{core.Low, core.High, core.Max},
		// V4.1 Flash shares the Flash recommendation under both separators.
		Alias: `deepseek-(?:v4[.\-]1-)?flash`,
	},
	{
		Config: withSyncedSpec(core.ModelConfig{
			Name:    "deepseek-v4-pro",
			BaseURL: "https://api.deepseek.com",
			API:     core.OpenAI,
			Think:   thinkHigh,
		}),
		Hooks:         chatHooks(false),
		thinkModes:    []core.ThinkMode{core.Off, core.Low, core.High, core.Max},
		openCodeModes: []core.ThinkMode{core.High, core.Max},
	},

	// Gemini 4 — Carbon/Argon default medium (LOW..HIGH); Neon/Hydrogen default high (LOW..XHIGH).
	// Source: https://ai.google.dev/gemini-api/docs/generate-content/thinking
	geminiPreset("gemini-4-carbon", thinkMedium, geminiLevelHooks("LOW", false), geminiLowToHighModes...),
	geminiPreset("gemini-4-argon", thinkMedium, geminiLevelHooks("LOW", false), geminiLowToHighModes...),
	geminiPreset("gemini-4-neon", thinkHigh, geminiLevelHooks("LOW", true), gemini4XHighModes...),
	geminiPreset("gemini-4-hydrogen", thinkHigh, geminiLevelHooks("LOW", true), gemini4XHighModes...),

	// Gemini 3 — thinkingLevel. 3.8 Flash and Pro floor LOW; 3 Flash floor MINIMAL.
	geminiPreset("gemini-3.8-flash", thinkHigh, geminiLevelHooks("LOW", false), geminiLowToHighModes...),
	// gemini-3-pro is absent from models.dev; retained for existing configs.
	{
		Config: core.ModelConfig{
			Name:          "gemini-3-pro",
			BaseURL:       "https://generativelanguage.googleapis.com/v1beta",
			ContextWindow: 1_000_000,
			ImageEnabled:  true,
			API:           core.Gemini,
			Think:         thinkHigh,
		},
		Hooks: geminiLevelHooks("LOW", false),
	},
	geminiPreset("gemini-3-flash", thinkHigh, geminiLevelHooks("MINIMAL", false)),

	// Gemini 2.5 — thinkingBudget (token cap).
	geminiPreset("gemini-2.5-pro", thinkHigh, geminiBudgetHooks()),
	geminiPreset("gemini-2.5-flash", thinkHigh, geminiBudgetHooks()),

	kimiPreset("kimi-k3.1-pro", `(?:auto-)?kimi-k3[.\-]1-pro`),
	kimiPreset("kimi-k3", `(?:auto-)?kimi-k3`),
	// Coding-plan 10M tier; models.dev only lists the 256 KiB standard tier with
	// limit.output equal to context, so keep this entry hand-maintained.
	{
		Config: core.ModelConfig{
			Name:          "kimi-k2.7-code",
			BaseURL:       "https://api.moonshot.cn/v1",
			ContextWindow: 10_000_000,
			ImageEnabled:  true,
			API:           core.OpenAI,
		},
	},

	// GLM on the z.ai coding plan. All presets are forced-thinking; endpoint
	// rules enable preserved history only on known native APIs.
	// Sources: https://docs.z.ai/api-reference/llm/chat-completion
	//          https://docs.z.ai/guides/capabilities/thinking
	// group/auto-glm-5-3-flash routes with a dash for the version dot.
	glmPreset("glm-5.4", `(?:auto-)?glm-5[.\-]4`),
	glmPreset("glm-5.3-flash", `(?:auto-)?glm-5[.\-]3-flash`),
	glmPreset("glm-5.3", `(?:auto-)?glm-5[.\-]3`),
}
