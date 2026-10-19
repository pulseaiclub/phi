package permission

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pathSubjects is the shape staticGate.subjects produces for resolved files.
func pathSubjects(paths ...string) []subject {
	out := make([]subject, 0, len(paths))
	for _, path := range paths {
		out = append(out, subject{kind: subjectPath, text: path, real: path})
	}
	return out
}

func execSubjects(command string) []subject {
	return []subject{{kind: subjectCommand, text: command}}
}

func TestEvaluateDenyOutranksAllow(t *testing.T) {
	// The allow is narrower (tool-scoped) than the deny (kind-wide) and still
	// must lose: an allow rule may never widen a policy.
	rs := ruleSet{
		deny:  []Rule{{Kind: KindWrite, Decision: Deny, Reason: "no writes"}},
		allow: []Rule{{Tool: "write", Kind: KindWrite, Decision: Allow}},
	}
	req := Request{Kind: KindWrite, Tool: "write", Paths: []string{"/tmp/a"}}

	dec, reason := rs.evaluate(req, pathSubjects("/tmp/a"), newCheckEnv("/tmp", 1))
	assert.Equal(t, Deny, dec)
	assert.Equal(t, "no writes", reason)
}

func TestEvaluateAskOutranksAllow(t *testing.T) {
	rs := ruleSet{
		allow: []Rule{{Kind: KindWrite, Decision: Allow}},
		ask:   []Rule{{Kind: KindWrite, Decision: Ask, Reason: "confirm %s"}},
	}
	req := Request{Kind: KindWrite, Tool: "write", Paths: []string{"/tmp/a"}}

	dec, reason := rs.evaluate(req, pathSubjects("/tmp/a"), newCheckEnv("/tmp", 1))
	assert.Equal(t, Ask, dec)
	assert.Equal(t, "confirm /tmp/a", reason)
}

func TestAllowRuleIgnoresCompoundCommand(t *testing.T) {
	rs := ruleSet{
		allow:    []Rule{{Kind: KindExec, Command: regexp.MustCompile(`^ls\b`), Decision: Allow}},
		defaults: map[Kind]fallback{KindExec: {decision: Ask, reason: "approve %s"}},
	}
	req := Request{Kind: KindExec, Tool: "bash", Command: "ls && curl example.com"}

	dec, _ := rs.evaluate(req, execSubjects(req.Command), newCheckEnv("/tmp", 1))
	assert.Equal(t, Ask, dec, "an allowlisted prefix must not license the rest of the line")
}

func TestRequestDefaultAppliesOnlyWhenNothingMatched(t *testing.T) {
	rs := ruleSet{
		allow:    []Rule{{Kind: KindExec, Command: regexp.MustCompile(`^ls\b`), Decision: Allow}},
		defaults: map[Kind]fallback{KindExec: {decision: Deny, reason: "denied by default"}},
	}
	env := newCheckEnv("/tmp", 1)

	dec, reason := rs.evaluate(Request{Kind: KindExec, Tool: "bash", Command: "ls"}, execSubjects("ls"), env)
	assert.Equal(t, Allow, dec)
	assert.Empty(t, reason)

	dec, reason = rs.evaluate(Request{Kind: KindExec, Tool: "bash", Command: "curl x"}, execSubjects("curl x"), env)
	assert.Equal(t, Deny, dec)
	assert.Equal(t, "denied by default", reason)
}

func TestUnknownKindFallsBackToAsk(t *testing.T) {
	rs := ruleSet{defaults: defaultDecisions(DefaultPolicy())}
	req := Request{Kind: KindUnknown, Tool: "custom_tool"}

	dec, reason := rs.evaluate(req, []subject{{kind: subjectNone}}, newCheckEnv("/tmp", 0))
	assert.Equal(t, Ask, dec)
	assert.Equal(t, `unknown action "custom_tool" requires approval`, reason)
}

func TestScopeJudgedOnResolvedPath(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	rs := ruleSet{deny: []Rule{{
		Kind:     KindWrite,
		Scope:    ScopeOutsideWorkspace,
		Decision: Deny,
		Reason:   "outside: %s",
	}}}
	req := Request{Kind: KindWrite, Tool: "write", Paths: []string{filepath.Join(workspace, "link")}}
	// The path as written sits inside the workspace; its resolution does not.
	subjects := []subject{{
		kind: subjectPath,
		text: req.Paths[0],
		real: filepath.Join(outside, "real.txt"),
	}}

	dec, _ := rs.evaluate(req, subjects, newCheckEnv(workspace, 1))
	assert.Equal(t, Deny, dec)
}

func TestRuleSelectors(t *testing.T) {
	env := newCheckEnv("/tmp", 1)
	req := Request{Kind: KindRead, Tool: "mcp__fake__echo", Paths: []string{"mcp://fake/tools/echo"}}
	subjects := []subject{{kind: subjectURL, text: "mcp://fake/tools/echo"}}

	cases := []struct {
		name string
		rule Rule
		want bool
	}{
		{"tool glob", Rule{Tool: "mcp__*"}, true},
		{"tool mismatch", Rule{Tool: "read"}, false},
		{"kind match", Rule{Kind: KindRead}, true},
		{"kind mismatch", Rule{Kind: KindWrite}, false},
		{"url resource", Rule{Resource: ResourceURL}, true},
		{"path resource", Rule{Resource: ResourcePath}, false},
		{"scope with url", Rule{Scope: ScopeOutsideWorkspace}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, errText := tc.rule.selects(req, subjects[0], env)
			assert.Empty(t, errText)
			assert.Equal(t, tc.want, ok)
		})
	}
}

func TestExpandReasonTruncatesLongCommand(t *testing.T) {
	long := strings.Repeat("a", 500)
	got := expandReason("approve %s", subject{kind: subjectCommand, text: long})
	assert.Less(t, len(got), 200)
	assert.True(t, strings.HasSuffix(got, "…"))
	require.Contains(t, got, "approve ")
}
