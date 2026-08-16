package llm

import (
	"fmt"
	"strings"
)

// Provider selects the wire protocol used to communicate with a model.
type Provider string

const (
	ProviderAuto      Provider = "auto"
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"
)

// ParseProvider validates and canonicalizes a provider value from config or an
// API request. An empty value keeps the automatic detection behavior.
func ParseProvider(value string) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(ProviderAuto):
		return ProviderAuto, nil
	case string(ProviderOpenAI):
		return ProviderOpenAI, nil
	case string(ProviderAnthropic):
		return ProviderAnthropic, nil
	default:
		return "", fmt.Errorf("unknown provider %q; want auto, openai, or anthropic", strings.TrimSpace(value))
	}
}

// DetectProvider preserves the legacy provider heuristic for automatic mode.
func DetectProvider(cfg ModelConfig) Provider {
	base := strings.ToLower(cfg.BaseURL)
	if strings.Contains(base, "anthropic") ||
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(cfg.Name)), "claude") {
		return ProviderAnthropic
	}
	return ProviderOpenAI
}

// ResolveProvider gives an explicit provider precedence over automatic
// detection. Config and request boundaries must validate Provider with
// ParseProvider before calling this function.
func ResolveProvider(cfg ModelConfig) Provider {
	switch cfg.Provider {
	case ProviderOpenAI, ProviderAnthropic:
		return cfg.Provider
	case "", ProviderAuto:
		return DetectProvider(cfg)
	default:
		return DetectProvider(cfg)
	}
}
