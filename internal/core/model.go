package core

// RouterType selects an API endpoint protocol.
type RouterType string

// Supported endpoint protocols.
const (
	OpenAI          RouterType = "OpenAI"
	OpenAIResponses RouterType = "OpenAIResponses"
	Anthropic       RouterType = "Anthropic"
	Gemini          RouterType = "Gemini"
)

// ThinkMode is the provider-neutral reasoning effort.
type ThinkMode string

// Reasoning effort levels.
const (
	Off     ThinkMode = "off"
	Minimal ThinkMode = "minimal"
	Low     ThinkMode = "low"
	Medium  ThinkMode = "medium"
	High    ThinkMode = "high"
	XHigh   ThinkMode = "xhigh"
	Max     ThinkMode = "max"
)

// AllThinkModes returns a fresh list so pickers cannot mutate shared choices.
func AllThinkModes() []ThinkMode {
	return []ThinkMode{Off, Minimal, Low, Medium, High, XHigh, Max}
}

// ThinkConfig is the provider-agnostic reasoning level. Provider adapters map
// it to their own reasoning wire options.
type ThinkConfig struct {
	Mode    ThinkMode
	Enabled bool
}

// RequestParameters is a JSON merge patch for provider request options.
type RequestParameters map[string]any

// ModelConfig is the connection and capability configuration for one model endpoint.
type ModelConfig struct {
	Name string
	// Preset binds an opaque routed ID to recommended model capabilities.
	Preset  string
	APIKey  string
	BaseURL string
	// SkillPaths lists directories scanned for SKILL.md files, in priority order.
	SkillPaths []string
	// ContextWindow is the model's context window in tokens. Zero disables compaction.
	ContextWindow int
	// InputLimit is the model's maximum input tokens when lower than ContextWindow.
	InputLimit int
	// MaxTokens is the model's output limit. Zero falls back to a provider default.
	MaxTokens int
	// ImageEnabled opts this model into image attachments.
	ImageEnabled bool
	API          RouterType
	Think        ThinkConfig
	// RequestParameters overrides options after endpoint adaptation.
	RequestParameters RequestParameters
}

// CompactionWindow returns the token ceiling used to trigger proactive
// compaction: InputLimit when tighter than ContextWindow, otherwise ContextWindow.
func (c ModelConfig) CompactionWindow() int {
	if c.ContextWindow <= 0 {
		return 0
	}
	if c.InputLimit > 0 && c.InputLimit < c.ContextWindow {
		return c.InputLimit
	}
	return c.ContextWindow
}
