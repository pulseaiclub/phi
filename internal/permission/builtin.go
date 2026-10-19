package permission

import (
	"fmt"
	"regexp"
)

// buildRules turns the configured policy into rules. This is the only place
// that knows which switch means what; once built, the gate is a matcher.
func buildRules(p Policy) (ruleSet, error) {
	deny, err := bashDenyRules(p)
	if err != nil {
		return ruleSet{}, err
	}
	allow, err := bashAllowRules(p)
	if err != nil {
		return ruleSet{}, err
	}
	rs := ruleSet{deny: deny, allow: allow, defaults: defaultDecisions(p)}
	if len(p.SensitivePathDeny) > 0 {
		rs.deny = append(rs.deny, Rule{
			Sensitive: p.SensitivePathDeny,
			Decision:  Deny,
			Reason:    "access to sensitive path denied: %s",
		})
	}
	if p.WorkspaceOnlyWrites {
		rs.deny = append(rs.deny, Rule{
			Kind:     KindWrite,
			Scope:    ScopeOutsideWorkspace,
			Decision: Deny,
			Reason:   "write outside workspace denied: %s",
		})
	}
	if p.WorkspaceOnlyReads {
		rs.deny = append(rs.deny, Rule{
			Kind:     KindRead,
			Scope:    ScopeOutsideWorkspace,
			Decision: Deny,
			Reason:   "read outside workspace denied: %s",
		})
	}
	rs.ask = append(rs.ask, externalCallRules(p)...)
	rs.addRules(p.Rules)
	return rs, nil
}

// addRules files configured rules into their tiers, after the built-ins. An
// unknown decision value lands in deny: a rule the gate cannot classify must
// not end up permitting anything.
func (rs *ruleSet) addRules(rules []Rule) {
	for _, rule := range rules {
		switch rule.Decision {
		case Allow:
			rs.allow = append(rs.allow, rule)
		case Ask:
			rs.ask = append(rs.ask, rule)
		default:
			rs.deny = append(rs.deny, rule)
		}
	}
}

// bashDenyRules compiles the hard denials in their configured order. They run
// before any allow rule, so a permissive allowlist cannot resurrect them.
func bashDenyRules(p Policy) ([]Rule, error) {
	rules := make([]Rule, 0, len(p.BashDeny))
	for _, pattern := range p.BashDeny {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("bash deny: invalid pattern %q: %w", pattern, err)
		}
		rules = append(rules, Rule{
			Kind:     KindExec,
			Command:  re,
			Decision: Deny,
			Reason:   "bash denied by policy: matches " + pattern,
		})
	}
	return rules, nil
}

// bashAllowRules compiles the allowlist. Rule.selects drops these on compound
// commands, so an entry only ever green-lights one simple command.
func bashAllowRules(p Policy) ([]Rule, error) {
	rules := make([]Rule, 0, len(p.BashAllow))
	for _, pattern := range p.BashAllow {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("bash allow: invalid pattern %q: %w", pattern, err)
		}
		rules = append(rules, Rule{Kind: KindExec, Command: re, Decision: Allow})
	}
	return rules, nil
}

// externalCallRules prompt instead of deciding: what a remote resource does is
// not observable here. A remote call is an external action rather than a file
// operation, so the workspace-only denies above must not swallow it first.
func externalCallRules(p Policy) []Rule {
	rules := []Rule{{
		Kind:     KindWrite,
		Resource: ResourceURL,
		Decision: Ask,
		Reason:   "external tool call requires approval: %s",
	}}
	if p.WorkspaceOnlyReads {
		rules = append(rules, Rule{
			Kind:     KindRead,
			Resource: ResourceURL,
			Decision: Ask,
			Reason:   "external resource read requires approval: %s",
		})
	}
	// The inherited workdir becomes the child's workspace root, and the child
	// process is not covered by this gate, so a spawn outside it asks.
	return append(rules, Rule{
		Kind:     KindDelegate,
		Scope:    ScopeOutsideWorkspace,
		Decision: Ask,
		Reason:   "agent_spawn workdir outside workspace requires approval: %s",
	})
}

// defaultDecisions answers requests no rule matched. The execute default comes
// from the policy (children default-allow); everything the gate only reads from
// is allowed, and kinds the policy never heard of ask.
func defaultDecisions(p Policy) map[Kind]fallback {
	return map[Kind]fallback{
		KindExec:     bashFallback(p.BashDefault),
		KindRead:     {decision: Allow},
		KindWrite:    {decision: Allow},
		KindDelegate: {decision: Allow},
		KindControl:  {decision: Allow},
	}
}

func bashFallback(decision Decision) fallback {
	switch decision {
	case Allow:
		return fallback{decision: Allow}
	case Deny:
		return fallback{decision: Deny, reason: "bash denied by default policy"}
	default:
		return fallback{decision: Ask, reason: "bash requires approval: %s"}
	}
}
