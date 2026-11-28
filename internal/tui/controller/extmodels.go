package controller

import (
	"fmt"

	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/extension/core"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/project/model"
)

// extensionModelConfig converts an extension-registered model into a runtime
// config through the same preset resolution and validation as config.yaml
// entries, so extension models honor preset capabilities and think levels.
// Direct-connect registrations must carry a base_url — the host would
// otherwise dial an empty endpoint.
func extensionModelConfig(route core.ModelRoute, cfg *project.Config) (llm.ModelConfig, error) {
	reg := route.Reg
	if !route.ExtConn() && reg.BaseURL == "" {
		return llm.ModelConfig{}, fmt.Errorf("model %q: direct-connect registration needs base_url", reg.Name)
	}
	doc := project.ModelDoc{
		Name: reg.Name, Preset: reg.Preset, API: reg.API,
		BaseURL: reg.BaseURL, APIKey: reg.APIKey, ThinkLevel: reg.ThinkLevel,
	}
	if reg.ContextWindow > 0 {
		cw := int(reg.ContextWindow)
		doc.ContextWindow = &cw
	}
	imageEnabled, thinkEnabled := reg.ImageEnabled, reg.ThinkEnabled
	doc.ImageEnabled = &imageEnabled
	doc.ThinkEnabled = &thinkEnabled
	m, err := project.ResolveModelDoc(doc)
	if err != nil {
		return llm.ModelConfig{}, err
	}
	m.SkillPaths = cfg.SkillPaths
	return m, nil
}

// resolveModelConfig resolves a switchable model name. Extension models shadow
// config.yaml entries with the same name, matching the engine's transport
// selection; a name known to neither keeps the primary connection and only
// swaps the name (arbitrary-model workflow).
func resolveModelConfig(cfg *project.Config, r extension.Extensions, name string) (llm.ModelConfig, error) {
	// A nil runner means "no extensions" here — callers may not have loaded any.
	if r != nil {
		if route, ok := r.LookupModel(name); ok {
			return extensionModelConfig(route, cfg)
		}
	}
	if m, ok := cfg.FindModel(name); ok {
		return m, nil
	}
	m := cfg.Model()
	m.Name = name
	if _, ok := model.Lookup(name); ok {
		m.Preset = ""
	}
	if model.ValidateThinking(m) != nil {
		m.Think = fallbackThinking(m)
	}
	return m, nil
}

func fallbackThinking(m llm.ModelConfig) llm.ThinkConfig {
	if preset, ok := model.Lookup(m.Name); ok {
		cand := m
		cand.Think = preset.Config.Think
		if model.ValidateThinking(cand) == nil {
			return preset.Config.Think
		}
	}
	modes := model.ThinkingModes(m)
	mode := modes[len(modes)-1]
	return llm.ThinkConfig{Mode: mode, Enabled: mode != llm.Off}
}
