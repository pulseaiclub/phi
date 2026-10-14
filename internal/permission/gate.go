package permission

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/pulseaiclub/phi/internal/util"
)

// Gate evaluates permission requests. It has no side effects; Ask is handled by the caller.
type Gate interface {
	Check(ctx context.Context, req Request) (Decision, string)
}

// StaticGate evaluates against a fixed Policy and workspace root.
type StaticGate struct {
	Policy    Policy
	Workspace string

	bashAllow []*regexp.Regexp
	bashDeny  []*regexp.Regexp
}

// NewGate compiles policy regexes and returns a Gate.
// Empty workspace uses WorkspaceRoot().
func NewGate(policy Policy, workspace string) (*StaticGate, error) {
	if workspace == "" {
		workspace = WorkspaceRoot()
	}
	g := &StaticGate{Policy: policy, Workspace: workspace}
	var err error
	g.bashAllow, err = compilePatterns(policy.BashAllow)
	if err != nil {
		return nil, fmt.Errorf("bash allow: %w", err)
	}
	g.bashDeny, err = compilePatterns(policy.BashDeny)
	if err != nil {
		return nil, fmt.Errorf("bash deny: %w", err)
	}
	return g, nil
}

func compilePatterns(patterns []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", p, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// Check evaluates req and applies mode folding (Ask→Deny for headless-strict / autopilot).
func (g *StaticGate) Check(ctx context.Context, req Request) (Decision, string) {
	_ = ctx
	dec, reason := g.evaluate(req)
	return g.foldMode(dec, reason, req)
}

func (g *StaticGate) evaluate(req Request) (Decision, string) {
	switch req.Action {
	case ActionBash:
		return g.checkBash(req)
	case ActionWrite, ActionEdit:
		return g.checkWrite(req)
	case ActionRead, ActionGrep, ActionFind, ActionLs:
		return g.checkRead(req)
	case ActionAgent:
		return Allow, ""
	default:
		return Ask, fmt.Sprintf("unknown action %q requires approval", req.Action)
	}
}

func (g *StaticGate) checkBash(req Request) (Decision, string) {
	cmd := strings.TrimSpace(req.Command)
	if cmd == "" {
		return Deny, "empty bash command denied"
	}
	for _, re := range g.bashDeny {
		if re.MatchString(cmd) {
			return Deny, "bash denied by policy: matches " + re.String()
		}
	}
	// Allowlist only applies to a single simple command. Prefix matches like
	// ^ls\b must not green-light "ls && rm -rf …".
	if bashEligibleForAllowlist(cmd) {
		for _, re := range g.bashAllow {
			if re.MatchString(cmd) {
				return Allow, ""
			}
		}
	}
	def := g.Policy.BashDefault
	if def == Allow {
		return Allow, ""
	}
	if def == Deny {
		return Deny, "bash denied by default policy"
	}
	return Ask, "bash requires approval: " + util.TruncateBytes(cmd, 120, "…")
}

func (g *StaticGate) checkWrite(req Request) (Decision, string) {
	if len(req.Paths) == 0 {
		return Deny, "write/edit without path denied"
	}
	for _, p := range req.Paths {
		if IsSensitivePath(p, g.Policy.SensitivePathDeny) {
			return Deny, "write to sensitive path denied: " + p
		}
		if g.Policy.WorkspaceOnlyWrites && !InWorkspace(p, g.Workspace) {
			return Deny, "write outside workspace denied: " + p
		}
	}
	return Allow, ""
}

func (g *StaticGate) checkRead(req Request) (Decision, string) {
	if len(req.Paths) == 0 {
		// grep/find with default "." is normalized by extract; empty = allow cwd
		return Allow, ""
	}
	for _, p := range req.Paths {
		if IsSensitivePath(p, g.Policy.SensitivePathDeny) {
			return Deny, "read of sensitive path denied: " + p
		}
		if g.Policy.WorkspaceOnlyReads && !InWorkspace(p, g.Workspace) {
			return Deny, "read outside workspace denied: " + p
		}
	}
	return Allow, ""
}

func (g *StaticGate) foldMode(dec Decision, reason string, req Request) (Decision, string) {
	mode := g.Policy.Mode
	if mode == "" {
		mode = ModeInteractive
	}

	switch mode {
	case ModeInteractive:
		return dec, reason

	case ModeReadonly:
		if isMutating(req.Action) {
			// Allow bash only if it already matched allowlist (dec==Allow for bash).
			if req.Action == ActionBash && dec == Allow {
				return Allow, reason
			}
			if dec == Allow || dec == Ask {
				return Deny, readonlyReason(req, reason)
			}
		}
		if dec == Ask {
			return Deny, askFoldReason(reason, mode)
		}
		return dec, reason

	case ModeAutopilot, ModeHeadlessStrict:
		if dec == Ask {
			return Deny, askFoldReason(reason, mode)
		}
		return dec, reason

	default:
		return dec, reason
	}
}

func isMutating(a Action) bool {
	switch a {
	case ActionWrite, ActionEdit, ActionBash:
		return true
	default:
		return false
	}
}

func readonlyReason(req Request, fallback string) string {
	if fallback != "" && !strings.Contains(fallback, "requires approval") {
		return fallback
	}
	return fmt.Sprintf("readonly mode denies %s", req.Action)
}

func askFoldReason(reason string, mode Mode) string {
	if reason == "" {
		return fmt.Sprintf("%s mode denies operations that would require approval", mode)
	}
	return fmt.Sprintf("%s mode: %s", mode, reason)
}

// AllowAll is a Gate that always allows (tests / nil-policy fallback).
type AllowAll struct{}

// Check always returns Allow.
func (AllowAll) Check(context.Context, Request) (Decision, string) {
	return Allow, ""
}
