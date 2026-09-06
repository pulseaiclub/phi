package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// SetThinkingExpanded edits the YAML tree so comments and unrelated settings survive.
func SetThinkingExpanded(global GlobalLayout, expanded bool) error {
	path := global.ConfigFile()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read TUI settings %s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse TUI settings %s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	// Reject malformed values and duplicate keys before touching the existing file.
	var cfg fileConfig
	if err := doc.Decode(&cfg); err != nil {
		return fmt.Errorf("parse TUI settings %s: %w", path, err)
	}
	root := doc.Content[0]
	if root.Tag == "!!null" {
		root.Kind, root.Tag, root.Value = yaml.MappingNode, "!!map", ""
	}
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("TUI settings %s: config must be a YAML mapping", path)
	}
	tui := configField(root, "tui")
	if tui.Kind == 0 || tui.Tag == "!!null" {
		tui.Kind, tui.Tag, tui.Value = yaml.MappingNode, "!!map", ""
	}
	if tui.Kind != yaml.MappingNode {
		return fmt.Errorf("TUI settings %s: tui must be a YAML mapping", path)
	}
	field := configField(tui, "thinking_expanded")
	field.Kind, field.Tag, field.Value = yaml.ScalarNode, "!!bool", strconv.FormatBool(expanded)
	field.Content, field.Alias = nil, nil
	updated, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("encode TUI settings: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create TUI config directory: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("stage TUI settings: %w", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	defer f.Close()
	if info, err := os.Stat(path); err == nil {
		if err := f.Chmod(info.Mode().Perm()); err != nil {
			return fmt.Errorf("preserve config permissions: %w", err)
		}
	}
	if _, err := f.Write(updated); err != nil {
		return fmt.Errorf("write TUI settings: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close TUI settings: %w", err)
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("save TUI settings %s: %w", path, err)
	}
	return nil
}

func configField(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	value := &yaml.Node{}
	mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
	return value
}
