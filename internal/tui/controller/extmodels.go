package controller

import (
	"fmt"

	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/project"
)

// extensionModelConfig converts an extension-registered model into a runtime
// config through the same preset resolution and validation as config.yaml
// entries, so extension models honor preset capabilities and think levels.
// Direct-connect registrations must carry a base_url — the host would
// otherwise dial an empty endpoint.
func extensionModelConfig(route extension.ModelRoute, cfg *project.Config, maxRounds int) (llm.ModelConfig, error) {
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
	m.MaxRounds = maxRounds
	m.SkillPaths = cfg.SkillPaths
	return m, nil
}

// resolveModelConfig resolves a switchable model name. Extension models shadow
// config.yaml entries with the same name, matching the engine's transport
// selection; a name known to neither keeps the primary connection and only
// swaps the name (arbitrary-model workflow).
func resolveModelConfig(cfg *project.Config, r *extension.Runner, name string) (llm.ModelConfig, error) {
	if route, ok := r.LookupModel(name); ok {
		return extensionModelConfig(route, cfg, cfg.MaxRounds)
	}
	if m, ok := cfg.FindModel(name); ok {
		return m, nil
	}
	m := cfg.Model()
	m.Name = name
	return m, nil
}
