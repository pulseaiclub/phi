package project

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/project/model"
)

// Config is the project-level configuration loaded from ~/.phi/config.yaml.
// All models live in one flat list under the models key; DefaultModel names
// the entry used to start sessions (empty → the first entry).
type Config struct {
	Models       []llm.ModelConfig
	DefaultModel string // name of the default model; "" → first entry
	SkillPath    string
	MaxRounds    int // tool round budget; 0 → agent default (64)
	Permissions  permission.Policy
	Agents       AgentsConfig
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

// Model returns the default model config with the skill path and budget applied, ready
// for agent.NewEngine.
func (c *Config) Model() llm.ModelConfig {
	m := *c.defaultEntry()
	m.MaxRounds = c.MaxRounds
	if m.SkillPath == "" {
		m.SkillPath = c.SkillPath
	}
	return m
}

// AllModels returns every configured model with the skill path and budget applied — the
// complete set of switchable models.
func (c *Config) AllModels() []llm.ModelConfig {
	all := make([]llm.ModelConfig, len(c.Models))
	copy(all, c.Models)
	for i := range all {
		all[i].MaxRounds = c.MaxRounds
		if all[i].SkillPath == "" {
			all[i].SkillPath = c.SkillPath
		}
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
func loadConfig(global GlobalLayout) (*Config, error) {
	cfg, err := parseConfigFile(global.ConfigFile())
	if err != nil {
		return nil, err
	}
	applyEnvOverrides(cfg)

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
	return cfg, nil
}

// parseConfigFile reads models, skill_path, and permissions from the YAML
// config file. A missing file yields a zero Config with DefaultPolicy for
// permissions; a malformed file is an error so bad config never silently
// degrades to defaults.
func parseConfigFile(path string) (*Config, error) {
	cfg := &Config{Permissions: permission.DefaultPolicy(), Agents: AgentsConfig{Enabled: true}}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	// Pointer fields distinguish "key absent" from "zero value", so per-key
	// defaults (and permission.DefaultPolicy) survive decoding and are only
	// overridden by keys that are actually present.
	var raw ConfigDoc
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	for _, m := range raw.Models {
		mc := modelDocToConfig(m)
		if m.Default && cfg.DefaultModel == "" {
			cfg.DefaultModel = mc.Name
		}
		cfg.Models = append(cfg.Models, mc)
	}
	if raw.SkillPath != nil {
		cfg.SkillPath = *raw.SkillPath
	}
	if raw.MaxRounds != nil && *raw.MaxRounds > 0 {
		cfg.MaxRounds = *raw.MaxRounds
	}
	if raw.Permissions != nil {
		applyPermissions(&cfg.Permissions, raw.Permissions)
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

func modelDocToConfig(m ModelDoc) llm.ModelConfig {
	cfg := llm.ModelConfig{Name: m.Name, APIKey: m.APIKey, BaseURL: m.BaseURL}
	// A built-in preset supplies base_url / context_window / image_enabled / api
	// when the entry omits them; the explicit fields below still win so
	// users can override any default.
	if preset, ok := model.Lookup(m.Name); ok {
		pc := preset.Config
		if cfg.BaseURL == "" {
			cfg.BaseURL = pc.BaseURL
		}
		if pc.ContextWindow > 0 {
			cfg.ContextWindow = pc.ContextWindow
		}
		cfg.ImageEnabled = pc.ImageEnabled
		if cfg.API == "" {
			cfg.API = pc.API
		}
		// Inherit the preset's thinking config so models like deepseek-flash
		// ship with sensible defaults (thinking enabled, high mode).
		cfg.Think = pc.Think
	}
	if m.API != "" {
		cfg.API = llm.RouterType(m.API)
	}
	if m.ContextWindow != nil && *m.ContextWindow > 0 {
		cfg.ContextWindow = *m.ContextWindow
	}
	if m.ImageEnabled != nil {
		cfg.ImageEnabled = *m.ImageEnabled
	}
	if m.ThinkEnabled != nil {
		cfg.Think.Enabled = *m.ThinkEnabled
	}
	if m.ThinkLevel != "" {
		mode := llm.ThinkMode(m.ThinkLevel)
		cfg.Think.Mode = mode
		cfg.Think.Enabled = mode != llm.Off
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	return cfg
}

// applyPermissions merges the file's permissions block over DefaultPolicy.
// An explicitly set list (even an empty one) replaces the default list.
func applyPermissions(p *permission.Policy, raw *PermDoc) {
	if raw.Mode != "" {
		p.Mode = permission.Mode(raw.Mode)
	}
	if raw.WorkspaceOnlyWrites != nil {
		p.WorkspaceOnlyWrites = *raw.WorkspaceOnlyWrites
	}
	if raw.AskTimeoutSec != nil && *raw.AskTimeoutSec > 0 {
		p.AskTimeoutSec = *raw.AskTimeoutSec
	}
	if raw.DangerouslyAllowAll != nil {
		p.DangerouslyAllowAll = *raw.DangerouslyAllowAll
	}
	if b := raw.Bash; b != nil {
		if b.Default != "" {
			p.BashDefault = parseDecision(b.Default, p.BashDefault)
		}
		if b.Allow != nil {
			p.BashAllow = *b.Allow
		}
		if b.Deny != nil {
			p.BashDeny = *b.Deny
		}
	}
}

func countIndent(line string) int {
	n := 0
	for _, r := range line {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 2
		default:
			return n / 2
		}
	}
	// Treat 2 spaces as one indent level for our hand-rolled parser.
	return n / 2
}

func parseDecision(val string, def permission.Decision) permission.Decision {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "allow":
		return permission.Allow
	case "deny", "reject":
		return permission.Deny
	case "ask":
		return permission.Ask
	default:
		return def
	}
}

func applyEnvOverrides(c *Config) {
	if v := firstEnv("PHI_API_KEY"); v != "" {
		c.defaultEntry().APIKey = v
	}
	if v := firstEnv("PHI_BASE_URL"); v != "" {
		c.defaultEntry().BaseURL = v
	}
	if v := firstEnv("PHI_MODEL"); v != "" {
		c.defaultEntry().Name = v
		c.DefaultModel = v
	}
	if v := firstEnv("PHI_SKILL_PATH"); v != "" {
		c.SkillPath = v
	}
	if v := firstEnv("PHI_THINK_LEVEL"); v != "" {
		entry := c.defaultEntry()
		entry.Think.Mode = llm.ThinkMode(v)
		entry.Think.Enabled = (v != string(llm.Off))
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

// SetDangerouslyAllowAll persists permissions.dangerously_allow_all in config.yaml
// ("Allow All for Every Session"). Best-effort rewrite of that key.
func SetDangerouslyAllowAll(global GlobalLayout, enabled bool) error {
	path := global.ConfigFile()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	lines := []string{}
	if len(data) > 0 {
		lines = strings.Split(string(data), "\n")
	}
	val := "false"
	if enabled {
		val = "true"
	}
	inPerm := false
	found := false
	out := make([]string, 0, len(lines)+2)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		indent := countIndent(line)
		if indent == 0 && strings.HasPrefix(trimmed, "permissions:") {
			inPerm = true
			out = append(out, line)
			continue
		}
		if indent == 0 && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			if inPerm && !found {
				out = append(out, "  dangerously_allow_all: "+val)
				found = true
			}
			inPerm = false
		}
		if inPerm && indent == 1 && strings.HasPrefix(trimmed, "dangerously_allow_all:") {
			out = append(out, "  dangerously_allow_all: "+val)
			found = true
			continue
		}
		out = append(out, line)
	}
	if inPerm && !found {
		out = append(out, "  dangerously_allow_all: "+val)
		found = true
	}
	if !found {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
		out = append(out, "permissions:", "  dangerously_allow_all: "+val)
	}
	//nolint:gosec // G306: config.yaml is meant to be user-readable
	return os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o644)
}
