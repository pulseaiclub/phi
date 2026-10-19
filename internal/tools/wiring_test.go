package tools

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

// Every built-in tool must declare its permission action; the gate has no name
// table to fall back on, so a missing declaration silently degrades every call
// of that tool to "ask".
func TestDefaultToolsDeclarePermissionAction(t *testing.T) {
	args := map[string]string{
		"bash":  `{"command":"x"}`,
		"read":  `{"path":"a.go"}`,
		"write": `{"path":"a.go","content":"x"}`,
		"edit":  `{"payload":"*** SM:EDIT a.go\n*** SM:FIND\nx\n*** SM:PUT\ny\n"}`,
		"grep":  `{"pattern":"x"}`,
		"find":  `{"pattern":"*.go"}`,
		"ls":    `{"path":"."}`,
	}
	want := map[string]permission.Kind{
		"bash":  permission.KindExec,
		"read":  permission.KindRead,
		"write": permission.KindWrite,
		"edit":  permission.KindWrite,
		"grep":  permission.KindRead,
		"find":  permission.KindRead,
		"ls":    permission.KindRead,
	}
	reg := tooldef.NewRegistry(DefaultTools(nil))
	require.Len(t, reg, len(want), "built-in tool set changed; update this table")
	for name, action := range want {
		t.Run(name, func(t *testing.T) {
			require.NotNil(t, reg[name].Permission, "%s must declare a permission action", name)
			req, err := reg.PermissionRequest(name, json.RawMessage(args[name]), "")
			require.NoError(t, err)
			assert.Equal(t, action, req.Kind)
			assert.Equal(t, name, req.Tool)
		})
	}
}

func TestBuiltinToolPermissionPaths(t *testing.T) {
	root := t.TempDir()
	reg := tooldef.NewRegistry(DefaultTools(nil))
	for _, tc := range []struct {
		name  string
		args  string
		paths []string
	}{
		{"read", `{"path":"a.go"}`, []string{filepath.Join(root, "a.go")}},
		{"write", `{"path":"sub/a.go","content":"x"}`, []string{filepath.Join(root, "sub", "a.go")}},
		// grep/find default to the cwd the session runs in.
		{"grep", `{"pattern":"x"}`, []string{root}},
		{"find", `{"pattern":"*.go"}`, []string{root}},
		{"ls", `{"path":"src"}`, []string{filepath.Join(root, "src")}},
		// ls also accepts a bare string path.
		{"ls", `"src"`, []string{filepath.Join(root, "src")}},
		// Internal URLs name remote resources and must not be cwd-joined.
		{"read", `{"path":"mcp://fake/tools/echo"}`, []string{"mcp://fake/tools/echo"}},
		{"write", `{"path":"mcp://fake/tools/echo","content":"{}"}`, []string{"mcp://fake/tools/echo"}},
		{"ls", `{"path":"mcp://"}`, []string{"mcp://"}},
	} {
		t.Run(tc.name+tc.args, func(t *testing.T) {
			req, err := reg.PermissionRequest(tc.name, json.RawMessage(tc.args), root)
			require.NoError(t, err)
			assert.Equal(t, tc.paths, req.Paths)
		})
	}
}

func TestBashPermissionTrimsCommand(t *testing.T) {
	reg := tooldef.NewRegistry(DefaultTools(nil))
	req, err := reg.PermissionRequest("bash", json.RawMessage(`{"command":"  git status\n"}`), "")
	require.NoError(t, err)
	assert.Equal(t, "git status", req.Command)
	assert.Empty(t, req.Paths)
}

// A multi-file edit payload must expose every target, or the gate would let a
// write outside the workspace ride along with an allowed first target.
func TestEditPermissionListsEveryTarget(t *testing.T) {
	root := t.TempDir()
	reg := tooldef.NewRegistry(DefaultTools(nil))
	req, err := reg.PermissionRequest("edit", json.RawMessage(
		`{"payload":"*** SM:EDIT a.go\n*** SM:FIND\nx\n*** SM:PUT\ny\n*** SM:EDIT b.go\n*** SM:FIND\nx\n*** SM:PUT\ny\n"}`,
	), root)
	require.NoError(t, err)
	assert.Equal(t, permission.KindWrite, req.Kind)
	require.Len(t, req.Paths, 2)
	assert.Equal(t, filepath.Join(root, "a.go"), req.Paths[0])
	assert.Equal(t, filepath.Join(root, "b.go"), req.Paths[1])
}

func TestEditPermissionRejectsPayloadWithoutTarget(t *testing.T) {
	reg := tooldef.NewRegistry(DefaultTools(nil))
	_, err := reg.PermissionRequest("edit", json.RawMessage(
		`{"payload":"*** SM:FIND\nx\n*** SM:PUT\ny\n"}`,
	), "")
	require.Error(t, err)
}
