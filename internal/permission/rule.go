package permission

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/pulseaiclub/phi/internal/util"
)

// Kind names the policy category a tool call belongs to. Rules select on it;
// the concrete tool name stays available for messages and for tool-scoped
// rules, but never has to be known to the gate.
type Kind string

// Kind values. A tool declares one; the gate reasons only about categories.
const (
	KindUnknown  Kind = "unknown"
	KindExec     Kind = "exec"     // runs a shell command
	KindRead     Kind = "read"     // reads files, directories, or remote resources
	KindWrite    Kind = "write"    // creates or modifies files or remote resources
	KindDelegate Kind = "delegate" // hands execution to a sub-agent in a workspace
	KindControl  Kind = "control"  // waits for or cancels existing work
)

// Resource distinguishes filesystem paths from internal URLs, which name
// remote resources and must never be workspace-checked.
type Resource string

// Resource values. Empty matches either kind of subject.
const (
	ResourceAny  Resource = ""
	ResourcePath Resource = "path"
	ResourceURL  Resource = "url"
)

// Scope constrains a rule to paths on one side of the workspace boundary.
type Scope string

// Scope values. Empty matches any path.
const (
	ScopeAny              Scope = ""
	ScopeOutsideWorkspace Scope = "outside-workspace"
)

// Rule is one policy statement: selectors (all optional, AND-ed) decide which
// subjects it covers, Decision says what happens to them. Rules are data so a
// config file can add one without a new Go function per resource type.
type Rule struct {
	Tool      string // exact name or glob; empty matches every tool
	Kind      Kind   // empty matches every kind
	Resource  Resource
	Command   *regexp.Regexp // exec subjects only
	Sensitive []string       // filesystem prefixes, checked raw and symlink-resolved
	Scope     Scope
	Decision  Decision
	// Reason is shown to the model; %s expands to the subject (command or
	// path). Empty means "no explanation".
	Reason string
}

// ruleSet holds rules tiered by outcome. Order inside a tier is the order the
// rules were declared; tiers are strictest-first, so a deny beats an ask and an
// ask beats an allow. That ordering is the safety property: no allow entry can
// ever talk the gate out of a deny or out of a prompt.
type ruleSet struct {
	deny  []Rule
	allow []Rule
	ask   []Rule
	// defaults decide requests no rule matched. A kind with no entry is
	// unknown to the policy and asks.
	defaults map[Kind]fallback
}

type fallback struct {
	decision Decision
	reason   string
}

// subjectKind is what a rule actually looks at: a command, a filesystem path
// (with its symlink-resolved spelling), an internal URL, or nothing at all.
type subjectKind int

const (
	subjectNone subjectKind = iota
	subjectCommand
	subjectPath
	subjectURL
)

type subject struct {
	kind subjectKind
	text string // command text, path as given, or URL verbatim
	real string // resolved path; empty for anything else
}

// checkEnv is per-call state: resolving a sensitive prefix costs a stat walk,
// and one call usually tests several paths against the same prefixes.
type checkEnv struct {
	workspace string
	resolve   func(string) (string, error)
	prefixes  map[string]prefixResolution
}

type prefixResolution struct {
	real string
	err  string
}

func newCheckEnv(workspace string, pathCount int) *checkEnv {
	return &checkEnv{
		workspace: workspace,
		resolve:   requestPathResolver(pathCount),
		prefixes:  make(map[string]prefixResolution, 4),
	}
}

func (e *checkEnv) resolvePrefix(prefix string) (string, string) {
	if got, ok := e.prefixes[prefix]; ok {
		return got.real, got.err
	}
	real, err := e.resolve(prefix)
	got := prefixResolution{real: real}
	if err != nil {
		got.err = err.Error()
	}
	e.prefixes[prefix] = got
	return got.real, got.err
}

// selects reports whether rule covers the subject. A non-empty error means the
// rule could not be decided safely (an unresolvable sensitive prefix) and must
// deny.
func (r Rule) selects(req Request, subj subject, env *checkEnv) (bool, string) {
	if r.Tool != "" && !globMatch(r.Tool, req.Tool) {
		return false, ""
	}
	if r.Kind != "" && r.Kind != req.Kind {
		return false, ""
	}
	if r.Resource != ResourceAny {
		want := subjectPath
		if r.Resource == ResourceURL {
			want = subjectURL
		}
		if subj.kind != want {
			return false, ""
		}
	}
	if r.Command != nil {
		if subj.kind != subjectCommand || !r.Command.MatchString(subj.text) {
			return false, ""
		}
		// An allow must never green-light a chained command: `^ls\b` is not a
		// license for `ls && rm -rf …`.
		if r.Decision == Allow && !bashEligibleForAllowlist(subj.text) {
			return false, ""
		}
	}
	if len(r.Sensitive) > 0 {
		if subj.kind != subjectPath {
			return false, ""
		}
		if IsSensitivePath(subj.text, r.Sensitive) || IsSensitivePath(subj.real, r.Sensitive) {
			return true, ""
		}
		// A prefix directory may itself be a symlink; compare the resolved
		// spellings too so that cannot launder the target.
		for _, prefix := range r.Sensitive {
			resolved, errText := env.resolvePrefix(prefix)
			if errText != "" {
				return false, fmt.Sprintf("cannot resolve sensitive path %q: %s", prefix, errText)
			}
			if IsSensitivePath(subj.real, []string{resolved}) {
				return true, ""
			}
		}
		return false, ""
	}
	if r.Scope == ScopeOutsideWorkspace {
		if subj.kind != subjectPath {
			return false, ""
		}
		if InWorkspace(subj.real, env.workspace) {
			return false, ""
		}
	}
	return true, ""
}

// evaluate applies the tiered rules to every subject of req, then falls back to
// the kind default. The first deny or ask wins, in subject order, so one bad
// path in a multi-path call decides the whole call.
func (rs ruleSet) evaluate(req Request, subjects []subject, env *checkEnv) (Decision, string) {
	explicitlyAllowed := false
	for _, subj := range subjects {
		dec, reason, matched, denyErr := rs.match(req, subj, env)
		if denyErr != "" {
			return Deny, denyErr
		}
		if !matched {
			continue
		}
		if dec != Allow {
			return dec, reason
		}
		explicitlyAllowed = true
	}
	if explicitlyAllowed {
		return Allow, ""
	}
	return rs.fallbackFor(req, subjects)
}

func (rs ruleSet) match(req Request, subj subject, env *checkEnv) (Decision, string, bool, string) {
	for _, tier := range [][]Rule{rs.deny, rs.ask, rs.allow} {
		for _, rule := range tier {
			ok, denyErr := rule.selects(req, subj, env)
			if denyErr != "" {
				return Deny, "", true, denyErr
			}
			if ok {
				return rule.Decision, expandReason(rule.Reason, subj), true, ""
			}
		}
	}
	return Allow, "", false, ""
}

func (rs ruleSet) fallbackFor(req Request, subjects []subject) (Decision, string) {
	fb, ok := rs.defaults[req.Kind]
	if !ok {
		return Ask, unknownActionReason(req.Tool)
	}
	var subj subject
	if len(subjects) > 0 {
		subj = subjects[0]
	}
	return fb.decision, expandReason(fb.reason, subj)
}

func unknownActionReason(tool string) string {
	return fmt.Sprintf("unknown action %q requires approval", tool)
}

// expandReason fills %s with the subject. Commands are truncated: the model
// wrote them, and a 40-line heredoc makes an unusable prompt.
func expandReason(reason string, subj subject) string {
	if reason == "" || !strings.Contains(reason, "%s") {
		return reason
	}
	text := subj.text
	if subj.kind == subjectCommand {
		text = util.TruncateBytes(text, 120, "…")
	}
	return strings.Replace(reason, "%s", text, 1)
}

// validate rejects requests the policy cannot judge at all. These are
// malformed calls, not policy outcomes, so they deny before any rule runs.
func validate(req Request) (string, bool) {
	switch req.Kind {
	case KindExec:
		if strings.TrimSpace(req.Command) == "" {
			return "empty bash command denied", true
		}
	case KindWrite:
		if len(req.Paths) == 0 {
			return "write/edit without path denied", true
		}
	case KindDelegate:
		if len(req.Paths) != 1 || IsInternalURL(req.Paths[0]) {
			return "agent_spawn requires one filesystem workdir", true
		}
	}
	return "", false
}

// globMatch supports the `mcp__*` style tool selectors. A pattern without
// meta characters is a plain equality test.
func globMatch(pattern, name string) bool {
	if !strings.ContainsAny(pattern, "*?[") {
		return pattern == name
	}
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}
