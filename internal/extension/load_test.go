package extension_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/pulseaiclub/phi/internal/extension/core"
	"github.com/pulseaiclub/phi/internal/extension/loader"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/extension/create"
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
	require.NoError(t, create.Materialize(t.Context(), extDir, "guard", "0.0.1", src))

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
	require.NoError(t, create.Materialize(t.Context(), extDir, "greet", "0.0.1", src))
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

func TestRegisterToolTimeoutSecPropagates(t *testing.T) {
	root := t.TempDir()
	extDir := filepath.Join(root, "slow")
	src := `package main

import (
	"context"
	"encoding/json"

	"github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/phi"
)

func main() {
	m := phi.New("slow", "0.0.1")
	m.RegisterTool(ext.Tool{
		Name:        "slow",
		Description: "Takes a while",
		TimeoutSec:  180,
		Parameters:  map[string]any{"type": "object"},
		Execute: func(ctx context.Context, args json.RawMessage) (ext.ToolResult, error) {
			return ext.ToolResult{Content: "ok"}, nil
		},
	})
	_ = m.Run()
}
`
	require.NoError(t, create.Materialize(t.Context(), extDir, "slow", "0.0.1", src))
	m, err := loader.ReadManifest(extDir)
	require.NoError(t, err)
	proc, err := core.StartProc(t.Context(), m, extDir, t.TempDir(), root, "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = proc.Close() })
	require.Len(t, proc.Tools(), 1)
	assert.Equal(t, uint32(180), proc.Tools()[0].TimeoutSec)
	assert.False(t, proc.Tools()[0].HasDetail)
}

func TestRegisterToolHasDetailPropagates(t *testing.T) {
	root := t.TempDir()
	extDir := filepath.Join(root, "detail")
	src := `package main

import (
	"context"
	"encoding/json"

	"github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/phi"
)

func main() {
	m := phi.New("detail", "0.0.1")
	m.RegisterTool(ext.Tool{
		Name:        "ping",
		Description: "Ping",
		Parameters:  map[string]any{"type": "object"},
		DetailFromArgs: func(input json.RawMessage) string {
			return "ping-detail"
		},
		Execute: func(ctx context.Context, args json.RawMessage) (ext.ToolResult, error) {
			return ext.ToolResult{Content: "pong"}, nil
		},
	})
	_ = m.Run()
}
`
	require.NoError(t, create.Materialize(t.Context(), extDir, "detail", "0.0.1", src))
	m, err := loader.ReadManifest(extDir)
	require.NoError(t, err)
	proc, err := core.StartProc(t.Context(), m, extDir, t.TempDir(), root, "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = proc.Close() })
	require.Len(t, proc.Tools(), 1)
	assert.True(t, proc.Tools()[0].HasDetail)

	detail, err := proc.CallToolDetail(t.Context(), "ping", json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.Equal(t, "ping-detail", detail)
}

func TestRegisterToolReadablePropagates(t *testing.T) {
	root := t.TempDir()
	extDir := filepath.Join(root, "readable")
	src := `package main

import (
	"context"
	"encoding/json"

	"github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/phi"
)

func main() {
	m := phi.New("readable", "0.0.1")
	m.RegisterTool(ext.Tool{
		Name:        "read",
		Description: "Read a file",
		Parameters:  map[string]any{"type": "object"},
		Readable:    true,
		Execute: func(ctx context.Context, args json.RawMessage) (ext.ToolResult, error) {
			return ext.ToolResult{Content: "ok"}, nil
		},
	})
	_ = m.Run()
}
`
	require.NoError(t, create.Materialize(t.Context(), extDir, "readable", "0.0.1", src))
	m, err := loader.ReadManifest(extDir)
	require.NoError(t, err)
	proc, err := core.StartProc(t.Context(), m, extDir, t.TempDir(), root, "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = proc.Close() })
	require.Len(t, proc.Tools(), 1)
	assert.True(t, proc.Tools()[0].Readable)
}

// TestCommandPickerRoundTrip walks ShowPicker through the real wire: the
// extension asks over PXB, the host answers from the UI, and the command's
// Submit reports what the handler saw.
func TestCommandPickerRoundTrip(t *testing.T) {
	root := t.TempDir()
	extDir := filepath.Join(root, "pick")
	src := `package main

import (
	"github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/phi"
)

func pick(m *phi.ExtensionAPI, req ext.PickerRequest) {
	reply := m.ShowPicker(req)
	if !reply.OK {
		m.Submit("dismissed")
		return
	}
	m.Submit("picked " + reply.ID)
}

func main() {
	m := phi.New("pick", "0.0.1")
	m.RegisterCommand("pick", ext.Command{
		Description: "Pick a row",
		Handler: func(args string, ctx *ext.Context) error {
			pick(m, ext.PickerRequest{
				Title: "Models",
				Items: []ext.PickerItem{{ID: "opus", Label: "opus", Detail: "smart"}},
			})
			return nil
		},
	})
	m.RegisterCommand("pick-empty", ext.Command{
		Description: "Pick from nothing",
		Handler: func(args string, ctx *ext.Context) error {
			pick(m, ext.PickerRequest{Title: "Models"})
			return nil
		},
	})
	_ = m.Run()
}
`
	require.NoError(t, create.Materialize(t.Context(), extDir, "pick", "0.0.1", src))
	r, warns, err := extension.Load(root, "")
	require.NoError(t, err)
	require.Empty(t, warns, "%v", warns)
	t.Cleanup(r.Close)

	accept := true
	var asked []ext.PickerRequest
	r.Bind(ext.HostOpts{HasUI: true, UI: extension.BusUI{
		PickerFn: func(req ext.PickerRequest) ext.PickerReply {
			asked = append(asked, req)
			if !accept {
				return ext.PickerReply{}
			}
			return ext.PickerReply{OK: true, ID: req.Items[0].ID}
		},
	}})

	out, err := r.RunCommand("pick", "")
	require.NoError(t, err)
	assert.Equal(t, "picked opus", out.Submit)
	require.Len(t, asked, 1)
	assert.Equal(t, "Models", asked[0].Title)
	assert.Equal(t, "smart", asked[0].Items[0].Detail)

	accept = false
	out, err = r.RunCommand("pick", "")
	require.NoError(t, err)
	assert.Equal(t, "dismissed", out.Submit)

	out, err = r.RunCommand("pick-empty", "")
	require.NoError(t, err)
	assert.Equal(t, "dismissed", out.Submit)
	assert.Len(t, asked, 2, "an empty picker never reaches the UI")
}

func TestRegisterCommandNeedsArgsPropagates(t *testing.T) {
	root := t.TempDir()
	extDir := filepath.Join(root, "plan")
	src := `package main

import (
	"github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/phi"
)

func main() {
	m := phi.New("plan", "0.0.1")
	m.RegisterCommand("plan", ext.Command{
		Description: "Enter/exit plan mode — /plan on|off|status",
		NeedsArgs:   true,
		Handler: func(args string, ctx *ext.Context) error {
			return nil
		},
	})
	m.RegisterCommand("hello", ext.Command{
		Description: "Say hi",
		Handler: func(args string, ctx *ext.Context) error {
			return nil
		},
	})
	_ = m.Run()
}
`
	require.NoError(t, create.Materialize(t.Context(), extDir, "plan", "0.0.1", src))
	m, err := loader.ReadManifest(extDir)
	require.NoError(t, err)
	proc, err := core.StartProc(t.Context(), m, extDir, t.TempDir(), root, "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = proc.Close() })
	cmds := proc.Commands()
	require.Len(t, cmds, 2)
	byName := map[string]pxb.RegisterCommand{}
	for _, c := range cmds {
		byName[c.Name] = c
	}
	assert.True(t, byName["plan"].NeedsArgs)
	assert.False(t, byName["hello"].NeedsArgs)

	api := ext.NewAPI()
	proc.BuildAPI(api)
	entries := api.CommandEntries()
	entryBy := map[string]ext.CommandEntry{}
	for _, e := range entries {
		entryBy[e.Name] = e
	}
	assert.True(t, entryBy["plan"].NeedsArgs)
	assert.False(t, entryBy["hello"].NeedsArgs)
}
