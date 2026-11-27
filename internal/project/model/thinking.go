package model

import (
	"fmt"
	"slices"
	"strings"

	"github.com/pulseaiclub/phi/internal/core"
)

// ThinkingModes gives both pickers the model's supported levels, or generic levels when unknown.
func ThinkingModes(cfg core.ModelConfig) []core.ThinkMode {
	preset, _ := LookupBound(cfg.Preset, cfg.Name)
	modes := preset.thinkModes
	if len(preset.openCodeModes) > 0 && isOpenCodeChat(cfg.API, NormalizeEndpoint(cfg.BaseURL)) {
		modes = preset.openCodeModes
	}
	if len(modes) == 0 {
		return core.AllThinkModes()
	}
	return slices.Clone(modes)
}

// ValidateThinking rejects unsupported selections before they become requests.
func ValidateThinking(cfg core.ModelConfig) error {
	mode := cfg.Think.Mode
	if !cfg.Think.Enabled {
		mode = core.Off
	}
	if mode == "" {
		return nil
	}
	modes := ThinkingModes(cfg)
	if slices.Contains(modes, mode) {
		return nil
	}
	allowed := make([]string, len(modes))
	for i, mode := range modes {
		allowed[i] = string(mode)
	}
	return fmt.Errorf(
		"think_level %q is unsupported for %q at this endpoint; use %s",
		mode,
		cfg.Name,
		strings.Join(allowed, ", "),
	)
}
