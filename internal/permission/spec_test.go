package permission

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuleSpecCompile(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret")

	cases := []struct {
		name    string
		spec    RuleSpec
		wantErr string
		check   func(t *testing.T, rule Rule)
	}{
		{
			name: "full",
			spec: RuleSpec{
				Tool: "mcp__*", Kind: "read", Resource: "url",
				Decision: "ask", Reason: "external lookup",
			},
			check: func(t *testing.T, rule Rule) {
				assert.Equal(t, "mcp__*", rule.Tool)
				assert.Equal(t, KindRead, rule.Kind)
				assert.Equal(t, ResourceURL, rule.Resource)
				assert.Equal(t, Ask, rule.Decision)
				assert.Equal(t, "external lookup", rule.Reason)
			},
		},
		{
			name: "command",
			spec: RuleSpec{Kind: "exec", Command: `^git status$`, Decision: "allow"},
			check: func(t *testing.T, rule Rule) {
				require.NotNil(t, rule.Command)
				assert.True(t, rule.Command.MatchString("git status"))
			},
		},
		{
			name: "path",
			spec: RuleSpec{Kind: "read", Path: []string{secret}, Decision: "deny"},
			check: func(t *testing.T, rule Rule) {
				assert.Equal(t, []string{secret}, rule.Sensitive)
			},
		},
		{name: "missing decision", spec: RuleSpec{Kind: "read"}, wantErr: "decision: missing"},
		{name: "bad decision", spec: RuleSpec{Kind: "read", Decision: "maybe"}, wantErr: `decision: unknown "maybe"`},
		{name: "bad kind", spec: RuleSpec{Kind: "reads", Decision: "deny"}, wantErr: `kind: unknown "reads"`},
		{
			name:    "bad resource",
			spec:    RuleSpec{Resource: "socket", Decision: "deny"},
			wantErr: `resource: unknown "socket"`,
		},
		{name: "bad scope", spec: RuleSpec{Scope: "inside", Decision: "deny"}, wantErr: `scope: unknown "inside"`},
		{
			name:    "bad command pattern",
			spec:    RuleSpec{Command: `[`, Decision: "deny"},
			wantErr: `command: invalid pattern "["`,
		},
		{
			name:    "relative path",
			spec:    RuleSpec{Path: []string{".ssh"}, Decision: "deny"},
			wantErr: `path ".ssh" must be absolute`,
		},
		{
			name:    "command with path",
			spec:    RuleSpec{Command: "^ls$", Path: []string{secret}, Decision: "deny"},
			wantErr: "command selects an exec call",
		},
		{
			name:    "command with non-exec kind",
			spec:    RuleSpec{Kind: "read", Command: "^ls$", Decision: "deny"},
			wantErr: `command cannot select kind "read"`,
		},
		{
			name:    "exec with path",
			spec:    RuleSpec{Kind: "exec", Path: []string{secret}, Decision: "deny"},
			wantErr: "exec calls carry no path or URL",
		},
		{
			name:    "url with scope",
			spec:    RuleSpec{Resource: "url", Scope: "outside-workspace", Decision: "deny"},
			wantErr: "scope applies to filesystem paths",
		},
		{
			name:    "no selector",
			spec:    RuleSpec{Decision: "allow"},
			wantErr: "rule selects nothing",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule, err := tc.spec.Compile()
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			tc.check(t, rule)
		})
	}
}

func TestCompileRulesNumbersFailures(t *testing.T) {
	_, err := CompileRules([]RuleSpec{
		{Kind: "read", Decision: "allow"},
		{Kind: "write", Decision: "sometimes"},
	})
	require.ErrorContains(t, err, "rule 2:")
	require.ErrorContains(t, err, `decision: unknown "sometimes"`)
}

// TestConfiguredRulesApply proves the config surface reaches the gate: a rule
// loaded from a file changes a decision, and the tier order still holds.
func TestConfiguredRulesApply(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")

	policy := DefaultPolicy()
	rules, err := CompileRules([]RuleSpec{
		// MCP browsing asks, without touching workspace_only_reads.
		{Kind: "read", Resource: "url", Decision: "ask", Reason: "external lookup: %s"},
		// An extra sensitive path, denied for reads as well as writes.
		{Path: []string{secret}, Decision: "deny"},
		// A narrowing rule cannot resurrect the built-in workspace deny.
		{Kind: "write", Scope: "outside-workspace", Decision: "allow"},
		// Nor can it soften a built-in ask.
		{Kind: "delegate", Scope: "outside-workspace", Decision: "allow"},
		// A config allowlist entry is still subject to the compound guard.
		{Kind: "exec", Command: `^ls\b`, Decision: "allow"},
		{Kind: "unknown", Decision: "deny"},
	})
	require.NoError(t, err)
	policy.Rules = rules

	gate, err := NewGate(policy, workspace)
	require.NoError(t, err)

	cases := []struct {
		name string
		req  Request
		want Decision
	}{
		{"mcp read asks", Request{Kind: KindRead, Tool: "ls", Paths: []string{"mcp://fake/tools/echo"}}, Ask},
		{"extra sensitive path", Request{Kind: KindRead, Tool: "read", Paths: []string{secret}}, Deny},
		{"built-in deny holds", Request{
			Kind: KindWrite, Tool: "write",
			Paths: []string{filepath.Join(outside, "b.txt")},
		}, Deny},
		{"built-in ask holds", Request{Kind: KindDelegate, Tool: "agent_spawn", Paths: []string{outside}}, Ask},
		{"config allowlist", Request{Kind: KindExec, Tool: "bash", Command: "ls -la"}, Allow},
		{"config allowlist compound", Request{Kind: KindExec, Tool: "bash", Command: "ls && curl x"}, Ask},
		{"undeclared tool denied", Request{Kind: KindUnknown, Tool: "extension_tool"}, Deny},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec, reason := gate.Check(t.Context(), tc.req)
			assert.Equal(t, tc.want, dec, reason)
		})
	}
}

func TestRuleSpecSummary(t *testing.T) {
	cases := []struct {
		spec RuleSpec
		want string
	}{
		{RuleSpec{Kind: "read", Resource: "url", Decision: "ask"}, "read url → ask"},
		{RuleSpec{Tool: "mcp__*", Decision: "deny"}, "mcp__* → deny"},
		{RuleSpec{Kind: "exec", Command: "^git push", Decision: "ask"}, "exec cmd:^git push → ask"},
		{RuleSpec{Path: []string{"~/.ssh"}, Decision: "deny"}, "~/.ssh → deny"},
		{RuleSpec{Kind: "write", Scope: "outside-workspace", Decision: "deny"}, "write outside-workspace → deny"},
		{RuleSpec{Resource: "url"}, "url → ?"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, tc.spec.Summary())
	}
}
