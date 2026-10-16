package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// agentSpawnArgs encodes agent_spawn arguments. Paths must be marshaled rather
// than pasted into a literal: Windows paths contain backslashes, and "D:\a"
// inside a JSON string is an invalid escape.
func agentSpawnArgs(t *testing.T, workdir string) []byte {
	t.Helper()
	args, err := json.Marshal(struct {
		Prompt  string `json:"prompt"`
		WorkDir string `json:"workdir"`
	}{Prompt: "x", WorkDir: workdir})
	require.NoError(t, err)
	return args
}

func TestAgentSpawnArgsEncodeWindowsPaths(t *testing.T) {
	args := agentSpawnArgs(t, `D:\a\phi\outside`)
	require.JSONEq(t, `{"prompt":"x","workdir":"D:\\a\\phi\\outside"}`, string(args))

	req, err := Extract("agent_spawn", args)
	require.NoError(t, err)
	require.Len(t, req.Paths, 1)
	require.True(t, strings.HasSuffix(req.Paths[0], `D:\a\phi\outside`), req.Paths[0])
}

func TestInWorkspace(t *testing.T) {
	ws := "/Users/me/proj"
	require.True(t, InWorkspace("/Users/me/proj", ws), "workspace root should be inside itself")
	require.True(t, InWorkspace("/Users/me/proj/src/a.go", ws), "child should be inside")
	require.False(t, InWorkspace("/Users/me/other", ws), "sibling should be outside")
	require.False(t, InWorkspace("/Users/me/proj-evil/x", ws), "prefix-sibling should be outside")
	require.False(t, InWorkspace("/Users/me", ws), "parent should be outside")
}

func TestCheckWriteOutsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	g, err := NewGate(DefaultPolicy(), ws)
	require.NoError(t, err)
	outside := filepath.Join(os.TempDir(), "phi-perm-test-outside")
	dec, _ := g.Check(t.Context(), Request{
		Action: ActionWrite,
		Tool:   "write",
		Paths:  []string{outside},
	})
	require.Equal(t, Deny, dec)
}

func TestCheckWriteInsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	g, err := NewGate(DefaultPolicy(), ws)
	require.NoError(t, err)
	inside := filepath.Join(ws, "out.txt")
	dec, _ := g.Check(t.Context(), Request{
		Action: ActionWrite,
		Tool:   "write",
		Paths:  []string{inside},
	})
	require.Equal(t, Allow, dec)
}

func TestCheckWriteSymlinkDirEscape(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(ws, "link")))
	g, err := NewGate(DefaultPolicy(), ws)
	require.NoError(t, err)

	// CVE-2026-108599: the lexical path sits inside the workspace, but the OS
	// would resolve it outside; the gate must follow the link.
	dec, _ := g.Check(t.Context(), Request{
		Action: ActionWrite,
		Tool:   "write",
		Paths:  []string{filepath.Join(ws, "link", "escape.txt")},
	})
	require.Equal(t, Deny, dec, "write through symlinked dir must be denied")

	// A fresh file nested under a not-yet-created component of the symlink is
	// still an escape.
	dec, _ = g.Check(t.Context(), Request{
		Action: ActionWrite,
		Tool:   "write",
		Paths:  []string{filepath.Join(ws, "link", "sub", "escape.txt")},
	})
	require.Equal(t, Deny, dec)
}

func TestCheckWriteSymlinkedFileEscape(t *testing.T) {
	outside := t.TempDir()
	target := filepath.Join(outside, "secrets.txt")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o600))
	ws := t.TempDir()
	link := filepath.Join(ws, "secrets.txt")
	require.NoError(t, os.Symlink(target, link))
	g, err := NewGate(DefaultPolicy(), ws)
	require.NoError(t, err)

	dec, _ := g.Check(t.Context(), Request{
		Action: ActionWrite,
		Tool:   "edit",
		Paths:  []string{link},
	})
	require.Equal(t, Deny, dec, "edit through a symlinked file must be denied")
}

func TestCheckWriteSymlinkToSensitivePath(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	ws := t.TempDir()
	require.NoError(t, os.Symlink(filepath.Join(home, ".ssh"), filepath.Join(ws, "ssh")))
	g, err := NewGate(DefaultPolicy(), ws)
	require.NoError(t, err)

	dec, _ := g.Check(t.Context(), Request{
		Action: ActionWrite,
		Tool:   "write",
		Paths:  []string{filepath.Join(ws, "ssh", "authorized_keys")},
	})
	require.Equal(t, Deny, dec, "symlink must not launder a sensitive path")
}

func TestCheckReadSymlinkEscapeWithWorkspaceOnlyReads(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(ws, "link")))
	p := DefaultPolicy()
	p.WorkspaceOnlyReads = true
	g, err := NewGate(p, ws)
	require.NoError(t, err)

	dec, _ := g.Check(t.Context(), Request{
		Action: ActionRead,
		Tool:   "read",
		Paths:  []string{filepath.Join(ws, "link", "secret.txt")},
	})
	require.Equal(t, Deny, dec)
}

func TestCheckWriteInsideWorkspaceStillAllowed(t *testing.T) {
	// Guard against the symlink checks breaking the happy path, including a
	// workspace reached through a symlinked parent (macOS /tmp → /private/tmp).
	ws := t.TempDir()
	resolved, err := filepath.EvalSymlinks(ws)
	require.NoError(t, err)
	g, err := NewGate(DefaultPolicy(), ws)
	require.NoError(t, err)

	for _, root := range []string{ws, resolved} {
		dec, _ := g.Check(t.Context(), Request{
			Action: ActionWrite,
			Tool:   "write",
			Paths:  []string{filepath.Join(root, "out.txt")},
		})
		require.Equal(t, Allow, dec, "write inside %s should be allowed", root)
	}
}

func TestCheckAgentSpawnWorkdir(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	g, err := NewGate(DefaultPolicy(), ws)
	require.NoError(t, err)
	ctx := t.Context()

	// CVE-2026-108595: the workdir becomes the child's workspace root, so an
	// out-of-workspace workdir must not silently pass.
	req, err := Extract("agent_spawn", agentSpawnArgs(t, outside))
	require.NoError(t, err)
	dec, reason := g.Check(ctx, req)
	require.Equal(t, Ask, dec, reason)

	// In-workspace spawn stays allowed without prompting.
	req, err = Extract("agent_spawn", agentSpawnArgs(t, filepath.Join(ws, "pkg")))
	require.NoError(t, err)
	dec, reason = g.Check(ctx, req)
	require.Equal(t, Allow, dec, reason)

	// The inherited cwd is subject to the same check as an explicit workdir.
	req, err = ExtractAt("agent_spawn", []byte(`{"prompt":"x"}`), ws)
	require.NoError(t, err)
	dec, reason = g.Check(ctx, req)
	require.Equal(t, Allow, dec, reason)
}

func TestCheckAgentSpawnWorkdirDeniedInHeadlessStrict(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	p := DefaultPolicy()
	p.Mode = ModeHeadlessStrict
	g, err := NewGate(p, ws)
	require.NoError(t, err)

	req, err := Extract("agent_spawn", agentSpawnArgs(t, outside))
	require.NoError(t, err)
	dec, _ := g.Check(t.Context(), req)
	require.Equal(t, Deny, dec, "headless-strict folds the outside-workspace Ask into Deny")
}

func TestCheckReadonlyDeniesAgentSpawn(t *testing.T) {
	ws := t.TempDir()
	p := DefaultPolicy()
	p.Mode = ModeReadonly
	g, err := NewGate(p, ws)
	require.NoError(t, err)

	// Even an in-workspace spawn delegates user permissions to a process that
	// is not held to the readonly contract.
	req, err := Extract("agent_spawn", []byte(`{"prompt":"x"}`))
	require.NoError(t, err)
	dec, reason := g.Check(t.Context(), req)
	require.Equal(t, Deny, dec, reason)
}

func TestCheckAgentWaitAndCancelStayAllowed(t *testing.T) {
	g, err := NewGate(DefaultPolicy(), t.TempDir())
	require.NoError(t, err)
	ctx := t.Context()

	for _, tool := range []string{"agent_wait", "agent_cancel"} {
		req, err := Extract(tool, []byte(`{"job_id":"j1"}`))
		require.NoError(t, err)
		dec, reason := g.Check(ctx, req)
		require.Equal(t, Allow, dec, "%s: %s", tool, reason)
	}
}

func TestCheckWriteSensitiveConfig(t *testing.T) {
	ws := t.TempDir()
	g, err := NewGate(DefaultPolicy(), ws)
	require.NoError(t, err)
	home, _ := os.UserHomeDir()
	cfgPath := filepath.Join(home, ".phi", "config.yaml")
	dec, _ := g.Check(t.Context(), Request{
		Action: ActionWrite,
		Tool:   "write",
		Paths:  []string{cfgPath},
	})
	require.Equal(t, Deny, dec)
}

func TestCheckBashAllowDenyAsk(t *testing.T) {
	g, err := NewGate(DefaultPolicy(), t.TempDir())
	require.NoError(t, err)
	ctx := t.Context()

	dec, _ := g.Check(ctx, Request{Action: ActionBash, Tool: "bash", Command: "git status"})
	require.Equal(t, Allow, dec, "git status")
	dec, _ = g.Check(ctx, Request{Action: ActionBash, Tool: "bash", Command: "go test ./..."})
	require.Equal(t, Allow, dec, "go test")
	dec, _ = g.Check(ctx, Request{Action: ActionBash, Tool: "bash", Command: "sudo true"})
	require.Equal(t, Deny, dec, "sudo")
	dec, _ = g.Check(ctx, Request{Action: ActionBash, Tool: "bash", Command: "curl https://example.com"})
	require.Equal(t, Ask, dec, "curl")
}

func TestCheckBashCompoundNotAllowlisted(t *testing.T) {
	g, err := NewGate(DefaultPolicy(), t.TempDir())
	require.NoError(t, err)
	ctx := t.Context()

	// Prefix ^ls\b must NOT allow chained rm.
	cmd := `ls -la todo.list 2>/dev/null && rm -rf todo.list && echo "removed"`
	dec, _ := g.Check(ctx, Request{Action: ActionBash, Tool: "bash", Command: cmd})
	require.Equal(t, Deny, dec, "ls && rm -rf")

	// Plain ls still allowed.
	dec, _ = g.Check(ctx, Request{Action: ActionBash, Tool: "bash", Command: "ls -la todo.list"})
	require.Equal(t, Allow, dec, "ls alone")

	// rm -rf without trailing / must still deny.
	dec, _ = g.Check(ctx, Request{Action: ActionBash, Tool: "bash", Command: "rm -rf todo.list"})
	require.Equal(t, Deny, dec, "rm -rf file")

	// Pipe / redirect out of allowlist.
	dec, _ = g.Check(ctx, Request{Action: ActionBash, Tool: "bash", Command: "cat foo | sh"})
	require.NotEqual(t, Allow, dec, "pipe: must not Allow")
	dec, _ = g.Check(ctx, Request{Action: ActionBash, Tool: "bash", Command: "cat secret > /tmp/out"})
	require.NotEqual(t, Allow, dec, "redirect: must not Allow")
}

func TestModeHeadlessStrictFoldsAsk(t *testing.T) {
	p := DefaultPolicy()
	p.Mode = ModeHeadlessStrict
	g, err := NewGate(p, t.TempDir())
	require.NoError(t, err)
	dec, _ := g.Check(t.Context(), Request{
		Action:  ActionBash,
		Tool:    "bash",
		Command: "curl https://example.com",
	})
	require.Equal(t, Deny, dec)
}

func TestModeReadonlyDeniesWrite(t *testing.T) {
	ws := t.TempDir()
	p := DefaultPolicy()
	p.Mode = ModeReadonly
	g, err := NewGate(p, ws)
	require.NoError(t, err)
	dec, _ := g.Check(t.Context(), Request{
		Action: ActionWrite,
		Tool:   "write",
		Paths:  []string{filepath.Join(ws, "a.txt")},
	})
	require.Equal(t, Deny, dec)
	// allowlisted bash still ok
	dec, _ = g.Check(t.Context(), Request{
		Action:  ActionBash,
		Tool:    "bash",
		Command: "git status",
	})
	require.Equal(t, Allow, dec, "git status in readonly")
}

func TestModeAutopilotFoldsAsk(t *testing.T) {
	p := DefaultPolicy()
	p.Mode = ModeAutopilot
	g, err := NewGate(p, t.TempDir())
	require.NoError(t, err)
	dec, _ := g.Check(t.Context(), Request{
		Action:  ActionBash,
		Tool:    "bash",
		Command: "curl https://example.com",
	})
	require.Equal(t, Deny, dec)
}

func TestReadSensitiveDeny(t *testing.T) {
	g, err := NewGate(DefaultPolicy(), t.TempDir())
	require.NoError(t, err)
	home, _ := os.UserHomeDir()
	dec, _ := g.Check(t.Context(), Request{
		Action: ActionRead,
		Tool:   "read",
		Paths:  []string{filepath.Join(home, ".ssh", "id_rsa")},
	})
	require.Equal(t, Deny, dec)
}
