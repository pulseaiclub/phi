package permission

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDecisionTable pins the gate's answer across kinds, policies, and modes.
// Every row was recorded from the hardcoded checks the rule engine replaced, so
// a failure here means a call that used to be judged one way is now judged
// another — review it as a policy change, not a refactor.
func TestDecisionTable(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	ssh := filepath.Join(home, ".ssh", "id_rsa")
	cfg := filepath.Join(home, ".phi", "config.yaml")

	defaults := DefaultPolicy()
	strict := DefaultPolicy()
	strict.WorkspaceOnlyReads = true
	openWrites := DefaultPolicy()
	openWrites.WorkspaceOnlyWrites = false
	readonly := DefaultPolicy()
	readonly.Mode = ModeReadonly
	autopilot := DefaultPolicy()
	autopilot.Mode = ModeAutopilot
	headless := DefaultPolicy()
	headless.Mode = ModeHeadlessStrict
	explore := ChildPolicy(ModeReadonly)
	denyExec := DefaultPolicy()
	denyExec.BashDefault = Deny
	wholeAllowlist := DefaultPolicy()
	wholeAllowlist.BashAllow = []string{`^rm`}
	lsAllowlist := DefaultPolicy()
	lsAllowlist.BashAllow = []string{`^ls\b`}

	exec := func(command string) Request {
		return Request{Kind: KindExec, Tool: "bash", Command: command}
	}
	read := func(path string) Request {
		return Request{Kind: KindRead, Tool: "read", Paths: []string{path}}
	}
	writeTo := func(path string) Request {
		return Request{Kind: KindWrite, Tool: "write", Paths: []string{path}}
	}
	delegate := func(paths ...string) Request {
		return Request{Kind: KindDelegate, Tool: "agent_spawn", Paths: paths}
	}
	control := func(tool string) Request { return Request{Kind: KindControl, Tool: tool} }
	unknown := func(tool string) Request { return Request{Kind: KindUnknown, Tool: tool} }

	rmDeny := `bash denied by policy: matches \brm\s+-[a-zA-Z]*[rf][a-zA-Z]*\b`
	rows := []struct {
		name     string
		policy   Policy
		req      Request
		decision Decision
		reason   string
	}{
		{"exec allowlist", defaults, exec("git status"), Allow, ""},
		{"exec padded allowlist", defaults, exec("  git status\n"), Allow, ""},
		{"exec chained allowlist", defaults, exec("ls -la x && rm -rf x"), Deny, rmDeny},
		{"exec pipe to sh", defaults, exec("cat foo | sh"), Deny, `bash denied by policy: matches \|\s*(ba)?sh\b`},
		{
			"exec redirect",
			defaults,
			exec("cat secret > /tmp/out"),
			Ask,
			"bash requires approval: cat secret > /tmp/out",
		},
		{"exec devnull redirect", defaults, exec("ls -la x 2>/dev/null"), Allow, ""},
		{"exec sudo", defaults, exec("sudo true"), Deny, `bash denied by policy: matches \bsudo\b`},
		{"exec rm -rf", defaults, exec("rm -rf todo.list"), Deny, rmDeny},
		{
			"exec off-list", defaults, exec("curl https://example.com"), Ask,
			"bash requires approval: curl https://example.com",
		},
		{"exec empty", defaults, exec("  "), Deny, "empty bash command denied"},
		{"exec deny by default", denyExec, exec("curl https://example.com"), Deny, "bash denied by default policy"},
		{"exec allowlist cannot outrank deny", wholeAllowlist, exec("rm -rf x"), Deny, rmDeny},
		{
			"exec allowlist ignores compound command", lsAllowlist, exec("ls && curl example.com"), Ask,
			"bash requires approval: ls && curl example.com",
		},
		{"exec child allow", explore, exec("curl https://example.com"), Allow, ""},
		{"exec child chained", explore, exec("curl x && ls"), Allow, ""},
		{"exec child deny", explore, exec("rm -rf x"), Deny, rmDeny},
		{
			"exec autopilot off-list", autopilot, exec("curl https://example.com"), Deny,
			"autopilot mode: bash requires approval: curl https://example.com",
		},
		{
			"exec headless off-list", headless, exec("curl https://example.com"), Deny,
			"headless-strict mode: bash requires approval: curl https://example.com",
		},
		{"exec headless allowlist", headless, exec("git status"), Allow, ""},
		{"exec readonly allowlist", readonly, exec("git status"), Allow, ""},
		{"exec readonly off-list", readonly, exec("curl https://example.com"), Deny, "readonly mode denies bash"},

		{"read inside", defaults, read(filepath.Join(ws, "a.go")), Allow, ""},
		{"read outside", defaults, read(filepath.Join(outside, "a.go")), Allow, ""},
		{
			"read outside strict", strict, read(filepath.Join(outside, "a.go")), Deny,
			"read outside workspace denied: " + filepath.Join(outside, "a.go"),
		},
		{"read inside strict", strict, read(filepath.Join(ws, "a.go")), Allow, ""},
		{"read sensitive", defaults, read(ssh), Deny, "access to sensitive path denied: " + ssh},
		{"read mcp url", defaults, read("mcp://fake/tools/echo"), Allow, ""},
		{
			"read mcp url strict", strict, read("mcp://fake/tools/echo"), Ask,
			"external resource read requires approval: mcp://fake/tools/echo",
		},
		{"read no path", defaults, Request{Kind: KindRead, Tool: "grep"}, Allow, ""},

		{"write inside", defaults, writeTo(filepath.Join(ws, "out.txt")), Allow, ""},
		{
			"write outside", defaults, writeTo(filepath.Join(outside, "out.txt")), Deny,
			"write outside workspace denied: " + filepath.Join(outside, "out.txt"),
		},
		{"write outside unrestricted", openWrites, writeTo(filepath.Join(outside, "out.txt")), Allow, ""},
		{"write sensitive", defaults, writeTo(cfg), Deny, "access to sensitive path denied: " + cfg},
		{
			"write mcp url", defaults, writeTo("mcp://fake/tools/echo"), Ask,
			"external tool call requires approval: mcp://fake/tools/echo",
		},
		{"write no path", defaults, Request{Kind: KindWrite, Tool: "write"}, Deny, "write/edit without path denied"},
		{"write readonly", readonly, writeTo(filepath.Join(ws, "out.txt")), Deny, "readonly mode denies write"},
		{"write mixed paths", defaults, Request{Kind: KindWrite, Tool: "write", Paths: []string{
			filepath.Join(ws, "a.txt"), filepath.Join(outside, "b.txt"),
		}}, Deny, "write outside workspace denied: " + filepath.Join(outside, "b.txt")},
		{"edit readonly", readonly, Request{
			Kind: KindWrite, Tool: "edit",
			Paths: []string{filepath.Join(ws, "a.go")},
		}, Deny, "readonly mode denies edit"},

		{"delegate inside", defaults, delegate(ws), Allow, ""},
		{
			"delegate outside", defaults, delegate(outside), Ask,
			"agent_spawn workdir outside workspace requires approval: " + outside,
		},
		{
			"delegate outside headless", headless, delegate(outside), Deny,
			"headless-strict mode: agent_spawn workdir outside workspace requires approval: " + outside,
		},
		{"delegate outside readonly", readonly, delegate(outside), Deny, "readonly mode denies agent_spawn"},
		{"delegate inside readonly", readonly, delegate(ws), Deny, "readonly mode denies agent_spawn"},
		{
			"delegate url", defaults, delegate("mcp://fake/tools/echo"), Deny,
			"agent_spawn requires one filesystem workdir",
		},
		{"delegate two paths", defaults, delegate(ws, ws), Deny, "agent_spawn requires one filesystem workdir"},
		{"delegate no path", defaults, delegate(), Deny, "agent_spawn requires one filesystem workdir"},
		{
			"delegate sensitive", defaults, delegate(filepath.Join(home, ".ssh")), Deny,
			"access to sensitive path denied: " + filepath.Join(home, ".ssh"),
		},

		{"control", defaults, control("agent_control"), Allow, ""},
		{"control readonly", readonly, control("agent_control"), Allow, ""},

		{"unknown", defaults, unknown("custom_tool"), Ask, `unknown action "custom_tool" requires approval`},
		{
			"unknown headless", headless, unknown("custom_tool"), Deny,
			`headless-strict mode: unknown action "custom_tool" requires approval`,
		},
		{
			"unknown readonly", readonly, unknown("custom_tool"), Deny,
			`readonly mode: unknown action "custom_tool" requires approval`,
		},
	}

	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			gate, err := NewGate(tc.policy, ws)
			require.NoError(t, err)
			decision, reason := gate.Check(t.Context(), tc.req)
			assert.Equal(t, tc.decision, decision)
			assert.Equal(t, tc.reason, reason)
		})
	}
}
