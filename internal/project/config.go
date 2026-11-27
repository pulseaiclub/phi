package project

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/project/model"
)

// Config is the project-level configuration loaded from ~/.phi/config.yaml.
// All models live in one flat list under the models key; DefaultModel names
// the entry used to start sessions (empty → the first entry).
type Config struct {
	Models       []llm.ModelConfig
	DefaultModel string // name of the default model; "" → first entry
	// SkillPath is the configured skill_path with "~" expanded; empty falls
	// back to ~/.phi/skills.
	SkillPath string
	// SkillPaths lists every root the agent reads skills from, in priority
	// order: SkillPath, then the .agents/skills locations.
	SkillPaths []string
	Agents     AgentsConfig
}

// AgentsConfig controls whether the main agent may spawn sub-agents
// (agent_spawn / agent_wait / …). Default is enabled; set enabled: false
// to keep ordinary sessions lean and avoid loading the extra tool schemas.
type AgentsConfig struct {
	Enabled bool // true when absent from config
	// Models names optional per-role defaults (explore|review|worker).
	// Empty → inherit the parent session model.
	Models AgentsRoleModels
}

// AgentsRoleModels maps sub-agent roles to configured model names.
type AgentsRoleModels struct {
	Explore string `yaml:"explore,omitempty"`
	Review  string `yaml:"review,omitempty"`
	Worker  string `yaml:"worker,omitempty"`
}

// ForRole returns the configured model name for a sub-agent role.
func (m AgentsRoleModels) ForRole(role job.Role) string {
	switch job.NormalizeRole(string(role)) {
	case job.RoleReview:
		return m.Review
	case job.RoleWorker:
		return m.Worker
	default:
		return m.Explore
	}
}

// RoleOverrides returns configured non-empty model names keyed by sub-agent role.
func (m AgentsRoleModels) RoleOverrides() map[job.Role]string {
	overrides := make(map[job.Role]string, 3)
	for role, name := range map[job.Role]string{
		job.RoleExplore: m.Explore,
		job.RoleReview:  m.Review,
		job.RoleWorker:  m.Worker,
	} {
		if name != "" {
			overrides[role] = name
		}
	}
	return overrides
}

// Model returns the default model config with the skill roots and budget applied, ready
// for agent.NewEngine.
func (c *Config) Model() llm.ModelConfig {
	m := *c.defaultEntry()
	m.SkillPaths = c.SkillPaths
	return m
}

// AllModels returns every configured model with the skill roots and budget applied — the
// complete set of switchable models.
func (c *Config) AllModels() []llm.ModelConfig {
	all := make([]llm.ModelConfig, len(c.Models))
	copy(all, c.Models)
	for i := range all {
		all[i].SkillPaths = c.SkillPaths
	}
	return all
}

// FindModel returns the configured model whose name matches, so callers can
// switch to it with its own api_key/base_url/context_window.
func (c *Config) FindModel(name string) (llm.ModelConfig, bool) {
	for _, m := range c.AllModels() {
		if m.Name == name {
			return m, true
		}
	}
	return llm.ModelConfig{}, false
}

// defaultEntry returns the default model entry (DefaultModel by name, else
// the first entry), creating one if the config has no models yet so env-only
// setups can still apply PHI_* overrides.
func (c *Config) defaultEntry() *llm.ModelConfig {
	if c.DefaultModel != "" {
		for i := range c.Models {
			if c.Models[i].Name == c.DefaultModel {
				return &c.Models[i]
			}
		}
	}
	if len(c.Models) > 0 {
		return &c.Models[0]
	}
	c.Models = append(c.Models, llm.ModelConfig{})
	return &c.Models[0]
}

// loadConfig reads the config file, applies environment overrides, and fills
// in defaults. A missing file yields a zero Config so env-only setups work.
func loadConfig(global GlobalLayout, cwd string) (*Config, error) {
	cfg, err := parseConfigFileWithEnv(global.ConfigFile(), true)
	if err != nil {
		return nil, err
	}
	if len(cfg.Models) == 0 {
		return nil, fmt.Errorf("missing models (add at least one model in %s)", global.ConfigFile())
	}
	def := cfg.defaultEntry()
	if def.Name == "" {
		return nil, fmt.Errorf("missing model name (set PHI_MODEL or models[].name in %s)", global.ConfigFile())
	}
	if def.APIKey == "" {
		return nil, fmt.Errorf("missing api_key (set PHI_API_KEY or models[].api_key in %s)", global.ConfigFile())
	}
	// A "~" in skill_path is expanded before anything stats the path: both the
	// docs and the settings screen hand out ~-prefixed paths, and a literal
	// "~/…" fails os.Stat silently, so it would load zero skills with no
	// warning at all.
	cfg.SkillPath = expandHome(cfg.SkillPath)
	if cfg.SkillPath == "" {
		cfg.SkillPath = global.SkillsDir()
	}
	cfg.SkillPaths = resolveSkillPaths(cfg.SkillPath, global, cwd)
	return cfg, nil
}

// parseConfigFileWithEnv reads models and skill_path from the YAML config file.
// A missing file yields a zero Config; a malformed file is an error so bad
// config never silently degrades to defaults.
func parseConfigFileWithEnv(path string, withEnv bool) (*Config, error) {
	cfg := &Config{Agents: AgentsConfig{Enabled: true}}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	// Pointer fields distinguish "key absent" from "zero value", so per-key
	// defaults survive decoding and are only overridden by keys that are actually
	// present.
	var raw ConfigDoc
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	// Resolve defaults for the effective model, including env-only setups.
	if withEnv {
		applyEnvOverrides(&raw)
	}

	for _, m := range raw.Models {
		mc, err := ResolveModelDoc(m)
		if err != nil {
			return nil, fmt.Errorf("model %q: %w", m.Name, err)
		}
		if m.Default && cfg.DefaultModel == "" {
			cfg.DefaultModel = mc.Name
		}
		cfg.Models = append(cfg.Models, mc)
	}
	if raw.SkillPath != nil {
		cfg.SkillPath = *raw.SkillPath
	}
	if raw.Agents != nil {
		if raw.Agents.Enabled != nil {
			cfg.Agents.Enabled = *raw.Agents.Enabled
		}
		if raw.Agents.Models != nil {
			cfg.Agents.Models = AgentsRoleModels{
				Explore: strings.TrimSpace(raw.Agents.Models.Explore),
				Review:  strings.TrimSpace(raw.Agents.Models.Review),
				Worker:  strings.TrimSpace(raw.Agents.Models.Worker),
			}
		}
	}
	return cfg, nil
}

// ResolveModelDoc resolves defaults and validates overrides for the runtime and editor.
func ResolveModelDoc(m ModelDoc) (llm.ModelConfig, error) {
	cfg := llm.ModelConfig{
		Name: m.Name, Preset: m.Preset, APIKey: m.APIKey, BaseURL: m.BaseURL,
		API:               llm.RouterType(m.API),
		RequestParameters: m.RequestParameters,
	}
	// Explicit fields below override preset defaults.
	preset, ok := model.LookupBound(m.Preset, m.Name)
	if m.Preset != "" && !ok {
		return llm.ModelConfig{}, fmt.Errorf("unknown preset %q; use a built-in model name", m.Preset)
	}
	if ok {
		pc := preset.Config
		if cfg.BaseURL == "" {
			cfg.BaseURL = pc.BaseURL
		}
		if pc.ContextWindow > 0 {
			cfg.ContextWindow = pc.ContextWindow
		}
		if pc.InputLimit > 0 {
			cfg.InputLimit = pc.InputLimit
		}
		if pc.MaxTokens > 0 {
			cfg.MaxTokens = pc.MaxTokens
		}
		cfg.ImageEnabled = pc.ImageEnabled
		if cfg.API == "" {
			cfg.API = pc.API
		}
		cfg.Think = pc.Think
	}
	if m.ContextWindow != nil && *m.ContextWindow > 0 {
		cfg.ContextWindow = *m.ContextWindow
		// Keep the preset's InputLimit when tightening ContextWindow to a value
		// still above InputLimit (e.g. a 1M gateway cap on a 1.05M / 922K
		// preset); clear it when expanding the window or capping at/below InputLimit.
		if !ok || *m.ContextWindow > preset.Config.ContextWindow || *m.ContextWindow <= preset.Config.InputLimit {
			cfg.InputLimit = 0
		}
	}
	if m.MaxTokens != nil && *m.MaxTokens > 0 {
		cfg.MaxTokens = *m.MaxTokens
	}
	if m.ImageEnabled != nil {
		cfg.ImageEnabled = *m.ImageEnabled
	}
	if m.ThinkEnabled != nil {
		cfg.Think.Enabled = *m.ThinkEnabled
	}
	if m.ThinkLevel != "" {
		cfg.Think.Mode = llm.ThinkMode(m.ThinkLevel)
		cfg.Think.Enabled = cfg.Think.Mode != llm.Off
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if err := model.ValidateThinking(cfg); err != nil {
		return cfg, err
	}
	if err := cfg.RequestParameters.Validate(cfg.API); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func applyEnvOverrides(doc *ConfigDoc) {
	index := 0
	for i, m := range doc.Models {
		if m.Default {
			index = i
			break
		}
	}
	modelEnv := firstEnv("PHI_MODEL")
	if modelEnv != "" && len(doc.Models) == 0 {
		doc.Models = append(doc.Models, ModelDoc{})
		doc.Models[0].Name = modelEnv
		doc.Models[0].Default = true
	}
	entry := func() *ModelDoc {
		if len(doc.Models) == 0 {
			doc.Models = append(doc.Models, ModelDoc{})
		}
		return &doc.Models[index]
	}
	if v := firstEnv("PHI_API_KEY"); v != "" {
		entry().APIKey = v
	}
	if v := firstEnv("PHI_BASE_URL"); v != "" {
		entry().BaseURL = v
	}
	if v := firstEnv("PHI_SKILL_PATH"); v != "" {
		doc.SkillPath = &v
	}
	if v := firstEnv("PHI_THINK_LEVEL"); v != "" {
		entry().ThinkLevel = v
	}
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}
