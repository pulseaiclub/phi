package tooldef_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

type pathInput struct {
	Path    string `json:"path"`
	Command string `json:"command"`
}

func llmDefinition(name string) llm.ToolDefinition {
	return llm.ToolDefinition{Name: name}
}

func declaredTool(paths tooldef.Declaration[pathInput]) tooldef.Tool {
	return tooldef.NewTool(tooldef.WithPermission(paths))
}

func TestPermissionBindsActionAndCommand(t *testing.T) {
	tool := declaredTool(tooldef.Declaration[pathInput]{
		Kind:    permission.KindExec,
		Command: func(in pathInput) string { return in.Command },
	})
	req, err := tool.Permission(json.RawMessage(`{"command":"git status"}`), "")
	require.NoError(t, err)
	assert.Equal(t, permission.KindExec, req.Kind)
	assert.Equal(t, "git status", req.Command)
	assert.Empty(t, req.Paths)
}

func TestPermissionResolvesPathsAgainstCwd(t *testing.T) {
	root := t.TempDir()
	tool := declaredTool(tooldef.Declaration[pathInput]{
		Kind:  permission.KindWrite,
		Paths: func(in pathInput) ([]string, error) { return []string{in.Path}, nil },
	})
	req, err := tool.Permission(json.RawMessage(`{"path":"out.txt"}`), root)
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(root, "out.txt")}, req.Paths)
}

func TestPermissionKeepsInternalURLsVerbatim(t *testing.T) {
	tool := declaredTool(tooldef.Declaration[pathInput]{
		Kind:  permission.KindRead,
		Paths: func(in pathInput) ([]string, error) { return []string{in.Path}, nil },
	})
	req, err := tool.Permission(json.RawMessage(`{"path":"mcp://fake/tools/echo"}`), t.TempDir())
	require.NoError(t, err)
	require.Len(t, req.Paths, 1)
	assert.Equal(t, "mcp://fake/tools/echo", req.Paths[0], "internal URLs must not be cwd-joined")
}

func TestRegistryPermissionRequestSetsToolName(t *testing.T) {
	reg := tooldef.NewRegistry([]tooldef.Tool{
		tooldef.NewTool(
			tooldef.WithDefinition(llmDefinition("read")),
			tooldef.WithPermission(tooldef.Declaration[pathInput]{
				Kind:  permission.KindRead,
				Paths: func(in pathInput) ([]string, error) { return []string{in.Path}, nil },
			}),
		),
	})
	req, err := reg.PermissionRequest("read", json.RawMessage(`{"path":"a.go"}`), "")
	require.NoError(t, err)
	assert.Equal(t, "read", req.Tool)
	assert.Equal(t, permission.KindRead, req.Kind)
}

// An undeclared tool — an extension tool, or a name that never registered —
// must fall back to KindUnknown so the gate asks instead of trusting the name.
func TestRegistryPermissionRequestUndeclaredToolIsUnknown(t *testing.T) {
	reg := tooldef.NewRegistry([]tooldef.Tool{
		tooldef.NewTool(tooldef.WithDefinition(llmDefinition("ext_tool"))),
	})
	for _, name := range []string{"ext_tool", "never_registered"} {
		t.Run(name, func(t *testing.T) {
			req, err := reg.PermissionRequest(name, json.RawMessage(`{}`), "")
			require.NoError(t, err)
			assert.Equal(t, permission.KindUnknown, req.Kind)
			assert.Equal(t, name, req.Tool)
			assert.Equal(t, name, permission.Summarize(req))
		})
	}
}

func TestRegistryPermissionRequestWrapsExtractionErrors(t *testing.T) {
	reg := tooldef.NewRegistry([]tooldef.Tool{
		tooldef.NewTool(
			tooldef.WithDefinition(llmDefinition("write")),
			tooldef.WithPermission(tooldef.Declaration[pathInput]{
				Kind: permission.KindWrite,
				Paths: func(pathInput) ([]string, error) {
					return nil, assert.AnError
				},
			}),
		),
	})
	_, err := reg.PermissionRequest("write", json.RawMessage(`{"path":"a.go"}`), "")
	require.ErrorIs(t, err, assert.AnError)
	assert.Contains(t, err.Error(), "write", "the rejection must name the tool")
}

func TestRegistryPermissionRequestBadArgs(t *testing.T) {
	reg := tooldef.NewRegistry([]tooldef.Tool{
		tooldef.NewTool(
			tooldef.WithDefinition(llmDefinition("bash")),
			tooldef.WithPermission(tooldef.Declaration[pathInput]{
				Kind: permission.KindExec,
			}),
		),
	})
	_, err := reg.PermissionRequest("bash", json.RawMessage(`{"command":`), "")
	require.Error(t, err)
}
