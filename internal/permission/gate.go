package permission

import (
	"context"
	"fmt"
	"strings"
)

// Gate evaluates permission requests. It has no side effects; Ask is handled by the caller.
type Gate interface {
	Check(ctx context.Context, req Request) (Decision, string)
}

// staticGate evaluates requests against a fixed rule set and workspace root.
// It owns no mode: folding decisions by mode is modeGate's job.
type staticGate struct {
	Workspace string

	rules ruleSet

	// workspaceReal is the symlink-resolved Workspace. Workspace checks run
	// on the resolved spelling: the containment question is where the OS
	// would actually touch the file, so a symlink inside the workspace
	// pointing out of it cannot launder the target (CWE-59).
	workspaceReal string
}

// NewGate compiles the policy into rules and wraps the gate in the session
// mode. Empty workspace uses WorkspaceRoot().
func NewGate(policy Policy, workspace string) (Gate, error) {
	if workspace == "" {
		workspace = WorkspaceRoot()
	}
	resolved, err := realPath(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	rules, err := buildRules(policy)
	if err != nil {
		return nil, err
	}
	mode := policy.Mode
	if mode == "" {
		mode = ModeInteractive
	}
	return &modeGate{
		mode:  mode,
		inner: &staticGate{Workspace: workspace, workspaceReal: resolved, rules: rules},
	}, nil
}

// Check evaluates req against the rule set. Mode folding happens outside, in
// modeGate.
func (g *staticGate) Check(ctx context.Context, req Request) (Decision, string) {
	_ = ctx
	if reason, denied := validate(req); denied {
		return Deny, reason
	}
	set, denyReason := g.subjects(req)
	if denyReason != "" {
		return Deny, denyReason
	}
	return g.rules.evaluate(req, set.subjects, set.env)
}

// subjectSet is the subjects of one request plus the per-call resolution cache.
type subjectSet struct {
	subjects []subject
	env      *checkEnv
}

// subjects splits a request into the things rules look at: the command, or one
// subject per path. URLs stay verbatim; files resolve once, up front, so every
// rule compares the same spelling. A path that cannot be resolved denies the
// whole call: an unjudgeable path must not fall through to allow.
func (g *staticGate) subjects(req Request) (subjectSet, string) {
	env := newCheckEnv(g.workspaceReal, len(req.Paths))
	if req.Kind == KindExec {
		// Commands are matched and quoted trimmed: the policy regexes were
		// always anchored against the trimmed spelling.
		return subjectSet{
			env:      env,
			subjects: []subject{{kind: subjectCommand, text: strings.TrimSpace(req.Command)}},
		}, ""
	}
	if len(req.Paths) == 0 {
		return subjectSet{env: env, subjects: []subject{{kind: subjectNone}}}, ""
	}
	out := make([]subject, 0, len(req.Paths))
	for _, path := range req.Paths {
		if IsInternalURL(path) {
			out = append(out, subject{kind: subjectURL, text: path})
			continue
		}
		real, err := env.resolve(path)
		if err != nil {
			return subjectSet{}, fmt.Sprintf("cannot resolve path %q: %v", path, err)
		}
		out = append(out, subject{kind: subjectPath, text: path, real: real})
	}
	return subjectSet{env: env, subjects: out}, ""
}

// AllowAll is a Gate that always allows (tests / nil-policy fallback).
type AllowAll struct{}

// Check always returns Allow.
func (AllowAll) Check(context.Context, Request) (Decision, string) {
	return Allow, ""
}
