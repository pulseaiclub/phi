package project

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
)

// ConfigDoc preserves absent keys when the config editor saves ~/.phi/config.yaml.
type ConfigDoc struct {
	Models      []ModelDoc `yaml:"models"`
	SkillPath   *string    `yaml:"skill_path,omitempty"`
	MaxRounds   *int       `yaml:"max_rounds,omitempty"`
	Permissions *PermDoc   `yaml:"permissions,omitempty"`
	Agents      *AgentsDoc `yaml:"agents,omitempty"`
}

// ModelDoc is one models[] entry.
type ModelDoc struct {
	Name   string `yaml:"name"`
	Preset string `yaml:"preset,omitempty"`
	// APIKey and BaseURL are omitted when empty: the loader treats an absent
	// key exactly like an empty one, and a saved file reads cleaner without
	// `base_url: ""` noise on every preset-backed model.
	APIKey        string `yaml:"api_key,omitempty"`
	BaseURL       string `yaml:"base_url,omitempty"`
	ContextWindow *int   `yaml:"context_window,omitempty"`
	// ImageEnabled is a pointer so json/yaml round-trips keep "key absent"
	// distinct from an explicit false.
	ImageEnabled      *bool                 `yaml:"image_enabled,omitempty"`
	API               string                `yaml:"api,omitempty"` // OpenAI | OpenAIResponses | Anthropic | Gemini
	ThinkEnabled      *bool                 `yaml:"think_enabled,omitempty"`
	ThinkLevel        string                `yaml:"think_level,omitempty"`
	Default           bool                  `yaml:"default,omitempty"`
	RequestParameters llm.RequestParameters `yaml:"request_parameters,omitempty"`
}

// PermDoc is the permissions block.
type PermDoc struct {
	Mode                string   `yaml:"mode,omitempty"`
	WorkspaceOnlyWrites *bool    `yaml:"workspace_only_writes,omitempty"`
	AskTimeoutSec       *int     `yaml:"ask_timeout_sec,omitempty"`
	DangerouslyAllowAll *bool    `yaml:"dangerously_allow_all,omitempty"`
	Bash                *BashDoc `yaml:"bash,omitempty"`
	// Rules are evaluated in strictest-first tiers: a configured deny outranks
	// every ask, and an ask outranks every allow. See doc/permissions.md.
	Rules []permission.RuleSpec `yaml:"rules,omitempty"`
}

// BashDoc is the permissions.bash block.
type BashDoc struct {
	Default string      `yaml:"default,omitempty"`
	Allow   *StringList `yaml:"allow,omitempty"`
	Deny    *StringList `yaml:"deny,omitempty"`
}

// AgentsDoc is the agents block.
type AgentsDoc struct {
	// Enabled is a pointer so omitting the key keeps the default (on) when only
	// agents.models is set.
	Enabled *bool             `yaml:"enabled,omitempty"`
	Models  *AgentsRoleModels `yaml:"models,omitempty"`
}

// StringList accepts either a single YAML scalar or a sequence, so both
// `allow: "go test ./..."` and the block list form in the README work.
type StringList []string

func (s *StringList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		*s = StringList{node.Value}
	case yaml.SequenceNode:
		items := make(StringList, 0, len(node.Content))
		for _, n := range node.Content {
			items = append(items, n.Value)
		}
		*s = items
	default:
		return errors.New("expected a string or a list of strings")
	}
	return nil
}

// ReadConfigDoc loads path into an editable document. A missing file yields an
// empty document so the editor can bootstrap a config.
func ReadConfigDoc(path string) (*ConfigDoc, error) {
	doc := &ConfigDoc{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return doc, nil
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return doc, nil
}

// Validate reports whether doc is safe to write. It does not mutate doc: a
// document with no explicit default is valid, since the loader falls back to
// the first model.
func (d *ConfigDoc) Validate() error {
	if len(d.Models) == 0 {
		return errors.New("at least one model is required")
	}
	seen := make(map[string]struct{}, len(d.Models))
	defaults := 0
	for i, m := range d.Models {
		name := strings.TrimSpace(m.Name)
		if name == "" {
			return fmt.Errorf("model %d has no name", i+1)
		}
		if _, dup := seen[name]; dup {
			return fmt.Errorf("duplicate model name %q", name)
		}
		seen[name] = struct{}{}
		if _, err := ResolveModelDoc(m); err != nil {
			return fmt.Errorf("model %q: %w", name, err)
		}
		if m.Default {
			defaults++
			if strings.TrimSpace(m.APIKey) == "" {
				return fmt.Errorf("default model %q is missing api_key", name)
			}
		}
	}
	if defaults > 1 {
		return errors.New("only one model may be marked default")
	}
	// With no explicit default the loader starts with the first entry, so that
	// entry is the one that has to be usable.
	if defaults == 0 && strings.TrimSpace(d.Models[0].APIKey) == "" {
		return fmt.Errorf("model %q is used by default and is missing api_key", strings.TrimSpace(d.Models[0].Name))
	}
	return nil
}

// Save writes doc to path as YAML, keeping the previous file as path+".bak" so
// a bad edit never costs the user their only copy.
func (d *ConfigDoc) Save(path string) error {
	data, err := yaml.Marshal(d)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if cur, err := os.ReadFile(path); err == nil {
		//nolint:gosec // G306: config backup stays user-readable
		if err := os.WriteFile(path+".bak", cur, 0o644); err != nil {
			return fmt.Errorf("backup config: %w", err)
		}
	}
	//nolint:gosec // G306: config.yaml is meant to be user-readable
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}
