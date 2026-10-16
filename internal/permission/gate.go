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

	// workspaceReal is the symlink-resolved Workspace. Workspace checks run
	// on the resolved spelling: the containment question is where the OS
	// would actually touch the file, so a symlink inside the workspace
	// pointing out of it cannot launder the target (CWE-59).
	workspaceReal string
}

// NewGate compiles policy regexes and returns a Gate.
// Empty workspace uses WorkspaceRoot().
func NewGate(policy Policy, workspace string) (*StaticGate, error) {
	if workspace == "" {
		workspace = WorkspaceRoot()
	}
	resolved, err := realPath(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	g := &StaticGate{Policy: policy, Workspace: workspace, workspaceReal: resolved}
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
	case ActionAgentSpawn:
		return g.checkAgentSpawn(req)
	case ActionAgentControl:
		return Allow, ""
	default:
		action := string(req.Action)
		if req.Action == ActionUnknown {
			action = req.Tool
		}
		return Ask, fmt.Sprintf("unknown action %q requires approval", action)
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
	resolve := requestPathResolver(len(req.Paths))
	for _, p := range req.Paths {
		// An internal URL (mcp://…) is an external call, not a file write:
		// workspace-only policy must not hard-deny it, so it asks instead.
		if IsInternalURL(p) {
			return Ask, "external tool call requires approval: " + p
		}
		real, reason := g.resolveAllowedPath(p, resolve)
		if reason != "" {
			return Deny, reason
		}
		if g.Policy.WorkspaceOnlyWrites && !InWorkspace(real, g.workspaceReal) {
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
	resolve := requestPathResolver(len(req.Paths))
	for _, p := range req.Paths {
		// MCP browsing is a read-tier lookup; only a workspace-only policy
		// escalates it, and then to Ask (never a silent deny).
		if IsInternalURL(p) {
			if g.Policy.WorkspaceOnlyReads {
				return Ask, "external resource read requires approval: " + p
			}
			continue
		}
		real, reason := g.resolveAllowedPath(p, resolve)
		if reason != "" {
			return Deny, reason
		}
		if g.Policy.WorkspaceOnlyReads && !InWorkspace(real, g.workspaceReal) {
			return Deny, "read outside workspace denied: " + p
		}
	}
	return Allow, ""
}

// Multi-path calls reuse successful resolutions only until this Check returns.
// A single-path call avoids allocating a cache; later calls always see fresh links.
func requestPathResolver(pathCount int) func(string) (string, error) {
	if pathCount <= 1 {
		return realPath
	}
	resolved := make(map[string]string)
	return func(path string) (string, error) {
		if real, ok := resolved[path]; ok {
			return real, nil
		}
		real, err := realPath(path)
		if err == nil {
			resolved[path] = real
		}
		return real, err
	}
}

// resolveAllowedPath checks both aliases and canonical sensitive prefixes.
func (g *StaticGate) resolveAllowedPath(path string, resolve func(string) (string, error)) (string, string) {
	real, err := resolve(path)
	if err != nil {
		return "", fmt.Sprintf("cannot resolve path %q: %v", path, err)
	}
	if IsSensitivePath(path, g.Policy.SensitivePathDeny) || IsSensitivePath(real, g.Policy.SensitivePathDeny) {
		return "", "access to sensitive path denied: " + path
	}
	for _, prefix := range g.Policy.SensitivePathDeny {
		resolved, err := resolve(prefix)
		if err != nil {
			return "", fmt.Sprintf("cannot resolve sensitive path %q: %v", prefix, err)
		}
		if IsSensitivePath(real, []string{resolved}) {
			return "", "access to sensitive path denied: " + path
		}
	}
	return real, ""
}

// Readonly sessions cannot delegate execution.
func (g *StaticGate) checkAgentSpawn(req Request) (Decision, string) {
	if g.Policy.Mode == ModeReadonly {
		return Deny, "readonly mode denies agent_spawn"
	}
	if len(req.Paths) != 1 || IsInternalURL(req.Paths[0]) {
		return Deny, "agent_spawn requires one filesystem workdir"
	}
	real, reason := g.resolveAllowedPath(req.Paths[0], realPath)
	if reason != "" {
		return Deny, reason
	}
	if !InWorkspace(real, g.workspaceReal) {
		return Ask, "agent_spawn workdir outside workspace requires approval: " + req.Paths[0]
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
