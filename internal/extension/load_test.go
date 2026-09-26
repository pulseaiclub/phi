package extension_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/extension/exttest"
)

func TestLoadAndPreToolBlock(t *testing.T) {
	root := t.TempDir()
	extDir := filepath.Join(root, "guard")
	src := `package main

import (
	"encoding/json"
	"strings"

	"github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/phi"
)

func main() {
	m := phi.New("guard", "0.0.1")
	m.OnToolCall(func(ev ext.ToolCallEvent) *ext.ToolCallResult {
		if ev.ToolName != "bash" {
			return nil
		}
		var in struct {
			Command string ` + "`json:\"command\"`" + `
		}
		_ = json.Unmarshal(ev.Input, &in)
		if strings.Contains(in.Command, "phi-deny") {
			return &ext.ToolCallResult{Block: true, Reason: "blocked by extension"}
		}
		return nil
	})
	_ = m.Run()
}
`
	require.NoError(t, exttest.Materialize(t.Context(), extDir, "guard", "0.0.1", src))

	r, warns, err := extension.Load(root, "")
	require.NoError(t, err)
	require.Empty(t, warns, "%v", warns)
	t.Cleanup(r.Close)
	require.Len(t, r.Loaded(), 1)

	input := json.RawMessage(`{"command":"echo phi-deny"}`)
	_, blocked, reason, _ := r.PreTool(t.Context(), "bash", "1", input)
	assert.True(t, blocked)
	assert.Equal(t, "blocked by extension", reason)

	_, blocked, _, _ = r.PreTool(t.Context(), "bash", "2", json.RawMessage(`{"command":"echo ok"}`))
	assert.False(t, blocked)
}

func TestRegisterTool(t *testing.T) {
	root := t.TempDir()
	extDir := filepath.Join(root, "greet")
	src := `package main

import (
	"context"
	"encoding/json"

	"github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/phi"
)

func main() {
	m := phi.New("greet", "0.0.1")
	m.RegisterTool(ext.Tool{
		Name:        "greet",
		Description: "Greet someone",
		TimeoutSec:  120,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
			"required": []any{"name"},
		},
		DetailFromArgs: func(input json.RawMessage) string {
			var in struct {
				Name string ` + "`json:\"name\"`" + `
			}
			_ = json.Unmarshal(input, &in)
			return in.Name
		},
		Execute: func(ctx context.Context, args json.RawMessage) (ext.ToolResult, error) {
			var in struct {
				Name string ` + "`json:\"name\"`" + `
			}
			_ = json.Unmarshal(args, &in)
			return ext.ToolResult{Content: "Hello, " + in.Name + "!"}, nil
		},
	})
	_ = m.Run()
}
`
	require.NoError(t, exttest.Materialize(t.Context(), extDir, "greet", "0.0.1", src))
	r, warns, err := extension.Load(root, "")
	require.NoError(t, err)
	require.Empty(t, warns, "%v", warns)
	t.Cleanup(r.Close)
	tools := r.ExtensionTools()
	require.Len(t, tools, 1)
	assert.Equal(t, "greet", tools[0].Definition.Name)
	require.NotNil(t, tools[0].DetailFromArgs)
	assert.Equal(t, "phi", tools[0].DetailFromArgs(json.RawMessage(`{"name":"phi"}`)))

	res, err := tools[0].Run(t.Context(), json.RawMessage(`{"name":"phi"}`))
	require.NoError(t, err)
	assert.Equal(t, "Hello, phi!", res.Content)
}
