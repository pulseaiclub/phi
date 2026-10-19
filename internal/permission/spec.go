package permission

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// RuleSpec is a rule as a config file writes it: every selector and the
// decision are strings until Compile validates them. Config carries specs, the
// gate runs Rules. Keeping the two apart means a bad entry fails at load with a
// message about that entry, instead of becoming a rule that quietly never fires.
type RuleSpec struct {
	Tool     string   `yaml:"tool,omitempty"`
	Kind     string   `yaml:"kind,omitempty"`
	Resource string   `yaml:"resource,omitempty"`
	Command  string   `yaml:"command,omitempty"`
	Path     []string `yaml:"path,omitempty"`
	Scope    string   `yaml:"scope,omitempty"`
	Decision string   `yaml:"decision"`
	Reason   string   `yaml:"reason,omitempty"`
}

// Summary is a one-line rendering for humans ("read mcp__* → ask"). It is
// deliberately lossy: it names the selectors in the order a rule is usually
// written, and is not meant to be parsed back.
func (s RuleSpec) Summary() string {
	selectors := make([]string, 0, 4)
	for _, part := range []string{
		strings.TrimSpace(s.Kind),
		strings.TrimSpace(s.Tool),
		strings.TrimSpace(s.Resource),
	} {
		if part != "" {
			selectors = append(selectors, part)
		}
	}
	if command := strings.TrimSpace(s.Command); command != "" {
		selectors = append(selectors, "cmd:"+command)
	}
	selectors = append(selectors, s.Path...)
	if scope := strings.TrimSpace(s.Scope); scope != "" {
		selectors = append(selectors, scope)
	}
	if len(selectors) == 0 {
		selectors = []string{"every call"}
	}
	decision := strings.TrimSpace(s.Decision)
	if decision == "" {
		decision = "?"
	}
	return strings.Join(selectors, " ") + " → " + decision
}

// CompileRules compiles every spec, numbering the failure so a load error points
// at the offending entry rather than the whole block.
func CompileRules(specs []RuleSpec) ([]Rule, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	rules := make([]Rule, 0, len(specs))
	for i, spec := range specs {
		rule, err := spec.Compile()
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", i+1, err)
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// Compile validates a spec. Each check rejects a rule that would parse fine and
// then never match: a permission rule that silently does nothing is
// indistinguishable from one that was never written.
func (s RuleSpec) Compile() (Rule, error) {
	decision, err := parseRuleDecision(s.Decision)
	if err != nil {
		return Rule{}, err
	}
	rule := Rule{Tool: strings.TrimSpace(s.Tool), Decision: decision, Reason: s.Reason}
	if rule.Kind, err = parseRuleKind(s.Kind); err != nil {
		return Rule{}, err
	}
	if rule.Resource, err = parseRuleResource(s.Resource); err != nil {
		return Rule{}, err
	}
	if rule.Scope, err = parseRuleScope(s.Scope); err != nil {
		return Rule{}, err
	}
	if pattern := strings.TrimSpace(s.Command); pattern != "" {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return Rule{}, fmt.Errorf("command: invalid pattern %q: %w", pattern, err)
		}
		rule.Command = re
	}
	for _, path := range s.Path {
		path = strings.TrimSpace(path)
		if path == "" {
			return Rule{}, errors.New("path: empty entry")
		}
		if !filepath.IsAbs(path) {
			return Rule{}, fmt.Errorf("path %q must be absolute (config expands a leading ~ on load)", path)
		}
		rule.Sensitive = append(rule.Sensitive, path)
	}
	if err := rule.checkCombination(); err != nil {
		return Rule{}, err
	}
	return rule, nil
}

// checkCombination rejects selectors that cannot select the same subject: a
// command only ever appears on an exec call, and an exec call carries no path.
func (r Rule) checkCombination() error {
	pathSelector := len(r.Sensitive) > 0 || r.Scope != ScopeAny
	if r.Command != nil {
		if pathSelector || r.Resource != ResourceAny {
			return errors.New("command selects an exec call, which carries no path: drop path, scope, and resource")
		}
		if r.Kind != "" && r.Kind != KindExec {
			return fmt.Errorf("command cannot select kind %q: only exec calls carry a command", r.Kind)
		}
		return nil
	}
	if r.Kind == KindExec && (pathSelector || r.Resource != ResourceAny) {
		return errors.New("exec calls carry no path or URL: drop path, scope, and resource")
	}
	if r.Resource == ResourceURL && r.Scope != ScopeAny {
		return errors.New("scope applies to filesystem paths, not to resource: url")
	}
	if r.Tool == "" && r.Kind == "" && r.Resource == ResourceAny && !pathSelector {
		return errors.New("rule selects nothing: set at least one of tool, kind, resource, path, or scope")
	}
	return nil
}

func parseRuleDecision(raw string) (Decision, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "allow":
		return Allow, nil
	case "ask":
		return Ask, nil
	case "deny":
		return Deny, nil
	case "":
		return Allow, errors.New(`decision: missing (want "allow", "ask", or "deny")`)
	default:
		return Allow, fmt.Errorf(`decision: unknown %q (want "allow", "ask", or "deny")`, raw)
	}
}

func parseRuleKind(raw string) (Kind, error) {
	kind := Kind(strings.ToLower(strings.TrimSpace(raw)))
	switch kind {
	case "", KindExec, KindRead, KindWrite, KindDelegate, KindControl, KindUnknown:
		return kind, nil
	default:
		return "", fmt.Errorf(
			`kind: unknown %q (want exec, read, write, delegate, control, or unknown)`, raw,
		)
	}
}

func parseRuleResource(raw string) (Resource, error) {
	resource := Resource(strings.ToLower(strings.TrimSpace(raw)))
	switch resource {
	case ResourceAny, ResourcePath, ResourceURL:
		return resource, nil
	default:
		return ResourceAny, fmt.Errorf(`resource: unknown %q (want path or url)`, raw)
	}
}

func parseRuleScope(raw string) (Scope, error) {
	scope := Scope(strings.ToLower(strings.TrimSpace(raw)))
	switch scope {
	case ScopeAny, ScopeOutsideWorkspace:
		return scope, nil
	default:
		return ScopeAny, fmt.Errorf(`scope: unknown %q (want outside-workspace)`, raw)
	}
}
