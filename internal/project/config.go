package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/orca"
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
	Permissions  permission.Policy
	Agents       AgentsConfig
	// orca is the OrcaRouter credential store. Entries with api: OrcaRouter and
	// no api_key take their key from here, so the PKCE path and the pasted-key
	// path reach the same inference client through the same field.
	orca *orca.Store
}

// bindOrcaStore attaches the credential store and resolves OrcaRouter keys.
func (c *Config) bindOrcaStore(store *orca.Store) {
	c.orca = store
	c.resolveOrcaKeys()
}

// resolveOrcaKeys fills api_key for OrcaRouter entries that do not carry one
// inline, using the shared credential seam. A missing credential is left empty
// here so the error surfaces with the login instruction at request time rather
// than as a config parse failure.
//
// Entries are recognized either by an explicit `api: OrcaRouter` or by an
// OrcaRouter model name. The name path matters because the environment
// override can rename the default entry (PHI_MODEL) before this runs, and
// because the preset layer routes those names to OrcaRouter regardless: an
// entry that will be sent to api.orcarouter.ai must carry the OrcaRouter
// credential, not a leftover key meant for another vendor.
func (c *Config) resolveOrcaKeys() {
	if c.orca == nil {
		return
	}
	cred, err := c.orca.Credential(context.Background())
	if err != nil {
		return
	}
	for i := range c.Models {
		if c.Models[i].APIKey != "" || !routesToOrcaRouter(c.Models[i]) {
			continue
		}
		c.Models[i].APIKey = cred.APIKey
	}
}

// routesToOrcaRouter reports whether an entry will be sent to OrcaRouter.
func routesToOrcaRouter(m llm.ModelConfig) bool {
	if m.API == llm.OrcaRouter {
		return true
	}
	if m.API != "" {
		// An explicit non-OrcaRouter provider wins over the name.
		return false
	}
	_, ok := model.Lookup(m.Name)
	if !ok {
		return false
	}
	return m.BaseURL == "" || m.BaseURL == model.OrcaRouterBaseURL
}

// OrcaCredentialStatus reports the redacted OrcaRouter credential for display.
func (c *Config) OrcaCredentialStatus() llm.CredentialStatus {
	if c.orca == nil {
		return llm.CredentialStatus{}
	}
	return c.orca.Status(context.Background())
}

// UsesOrcaRouter reports whether any configured model routes through
// OrcaRouter, so callers can decide whether the credential path is relevant.
func (c *Config) UsesOrcaRouter() bool {
	return slices.ContainsFunc(c.Models, routesToOrcaRouter)
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
	Explore string `yaml:"explore"`
	Review  string `yaml:"review"`
	Worker  string `yaml:"worker"`
}

// Model returns the default model config with the skill path applied, ready
// for agent.NewEngine.
func (c *Config) Model() llm.ModelConfig {
	m := *c.defaultEntry()
	if m.SkillPath == "" {
		m.SkillPath = c.SkillPath
	}
	return m
}

// AllModels returns every configured model with the skill path applied — the
// complete set of switchable models.
func (c *Config) AllModels() []llm.ModelConfig {
	all := make([]llm.ModelConfig, len(c.Models))
	copy(all, c.Models)
	for i := range all {
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
func loadConfig(global GlobalLayout, store *orca.Store) (*Config, error) {
	cfg, err := parseConfigFile(global.ConfigFile())
	if err != nil {
		return nil, err
	}
	// Environment overrides run first: PHI_MODEL can rename the default entry,
	// and the OrcaRouter credential resolution below must see the final name so
	// an OrcaRouter-named entry gets the OrcaRouter key.
	applyEnvOverrides(cfg)
	cfg.bindOrcaStore(store)

	if len(cfg.Models) == 0 {
		return nil, fmt.Errorf("missing models (add at least one model in %s)", global.ConfigFile())
	}
	def := cfg.defaultEntry()
	if def.Name == "" {
		return nil, fmt.Errorf("missing model name (set PHI_MODEL or models[].name in %s)", global.ConfigFile())
	}
	// An entry routed to OrcaRouter may legitimately have no api_key: the
	// credential is resolved from the shared store, and a missing one is
	// reported by the request path with the login instruction instead of a
	// parse failure.
	if def.APIKey == "" && !routesToOrcaRouter(*def) {
		return nil, fmt.Errorf("missing api_key (set PHI_API_KEY or models[].api_key in %s)", global.ConfigFile())
	}
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
	var raw fileConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	for _, m := range raw.Models {
		mc := modelEntryToConfig(m)
		if m.Default && cfg.DefaultModel == "" {
			cfg.DefaultModel = mc.Name
		}
		cfg.Models = append(cfg.Models, mc)
	}
	if raw.SkillPath != nil {
		cfg.SkillPath = *raw.SkillPath
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

func modelEntryToConfig(m modelEntry) llm.ModelConfig {
	cfg := llm.ModelConfig{Name: m.Name, APIKey: m.APIKey, BaseURL: m.BaseURL}
	// An explicit api: OrcaRouter is a first-class named provider: it fills in
	// the inference base URL so the entry cannot be pointed at api.openai.com
	// by accident, and it is what the editor and catalog filter key on.
	if m.API == llm.OrcaRouter {
		if cfg.BaseURL == "" {
			cfg.BaseURL = model.OrcaRouterBaseURL
		}
	}
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
		cfg.API = m.API
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
	if m.ThinkLevel != nil && *m.ThinkLevel != "" {
		mode := llm.ThinkMode(*m.ThinkLevel)
		cfg.Think.Mode = mode
		cfg.Think.Enabled = mode != llm.Off
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	return cfg
}

// fileConfig mirrors the YAML keys in ~/.phi/config.yaml.
type fileConfig struct {
	Models      []modelEntry  `yaml:"models"`
	SkillPath   *string       `yaml:"skill_path"`
	Permissions *permConfig   `yaml:"permissions"`
	Agents      *agentsConfig `yaml:"agents"`
}

type agentsConfig struct {
	// Enabled is a pointer so omitting the key keeps the default (on).
	Enabled *bool             `yaml:"enabled"`
	Models  *AgentsRoleModels `yaml:"models"`
}

type modelEntry struct {
	Name          string         `yaml:"name"`
	APIKey        string         `yaml:"api_key"`
	BaseURL       string         `yaml:"base_url"`
	ContextWindow *int           `yaml:"context_window"`
	ImageEnabled  *bool          `yaml:"image_enabled"`
	API           llm.RouterType `yaml:"api"`
	Default       bool           `yaml:"default"`
	ThinkEnabled  *bool          `yaml:"think_enabled"`
	ThinkLevel    *string        `yaml:"think_level"`
}

type permConfig struct {
	Mode                permission.Mode `yaml:"mode"`
	WorkspaceOnlyWrites *bool           `yaml:"workspace_only_writes"`
	AskTimeoutSec       *int            `yaml:"ask_timeout_sec"`
	DangerouslyAllowAll *bool           `yaml:"dangerously_allow_all"`
	Bash                *bashConfig     `yaml:"bash"`
}

type bashConfig struct {
	Default *string     `yaml:"default"`
	Allow   *stringList `yaml:"allow"`
	Deny    *stringList `yaml:"deny"`
}

// applyPermissions merges the file's permissions block over DefaultPolicy.
// An explicitly set list (even an empty one) replaces the default list.
func applyPermissions(p *permission.Policy, raw *permConfig) {
	if raw.Mode != "" {
		p.Mode = raw.Mode
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
		if b.Default != nil {
			p.BashDefault = parseDecision(*b.Default, p.BashDefault)
		}
		if b.Allow != nil {
			p.BashAllow = *b.Allow
		}
		if b.Deny != nil {
			p.BashDeny = *b.Deny
		}
	}
}

// stringList accepts either a single YAML scalar or a sequence, so both
// `allow: "go test ./..."` and the block list form in the README work.
type stringList []string

func (s *stringList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		*s = stringList{node.Value}
	case yaml.SequenceNode:
		items := make(stringList, 0, len(node.Content))
		for _, n := range node.Content {
			items = append(items, n.Value)
		}
		*s = items
	default:
		return errors.New("expected a string or a list of strings")
	}
	return nil
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
