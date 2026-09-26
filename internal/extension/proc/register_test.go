package proc

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/extension/exttest"
	"github.com/pulseaiclub/phi/internal/extension/manifest"
)

// startTestExt materializes mainGo as an extension binary and starts it.
func startTestExt(t *testing.T, root, name, mainGo string) *Proc {
	t.Helper()
	extDir := filepath.Join(root, name)
	require.NoError(t, exttest.Materialize(t.Context(), extDir, name, "0.0.1", mainGo))
	m, err := manifest.ReadManifest(extDir)
	require.NoError(t, err)
	p, err := StartProc(t.Context(), m, extDir, t.TempDir(), root, "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func TestRegisterToolTimeoutSecPropagates(t *testing.T) {
	root := t.TempDir()
	p := startTestExt(t, root, "slow", `package main

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
`)
	require.Len(t, p.Tools(), 1)
	assert.Equal(t, uint32(180), p.Tools()[0].TimeoutSec)
	assert.False(t, p.Tools()[0].HasDetail)
}

func TestRegisterToolHasDetailPropagates(t *testing.T) {
	root := t.TempDir()
	p := startTestExt(t, root, "detail", `package main

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
`)
	require.Len(t, p.Tools(), 1)
	assert.True(t, p.Tools()[0].HasDetail)

	detail, err := p.CallToolDetail(t.Context(), "ping", json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.Equal(t, "ping-detail", detail)
}

func TestRegisterToolReadablePropagates(t *testing.T) {
	root := t.TempDir()
	p := startTestExt(t, root, "readable", `package main

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
`)
	require.Len(t, p.Tools(), 1)
	assert.True(t, p.Tools()[0].Readable)
}

func TestRegisterCommandNeedsArgsPropagates(t *testing.T) {
	root := t.TempDir()
	p := startTestExt(t, root, "plan", `package main

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
`)
	cmds := p.Commands()
	require.Len(t, cmds, 2)
	byName := map[string]pxb.RegisterCommand{}
	for _, c := range cmds {
		byName[c.Name] = c
	}
	assert.True(t, byName["plan"].NeedsArgs)
	assert.False(t, byName["hello"].NeedsArgs)

	api := ext.NewAPI()
	p.BuildAPI(api)
	entries := api.CommandEntries()
	entryBy := map[string]ext.CommandEntry{}
	for _, e := range entries {
		entryBy[e.Name] = e
	}
	assert.True(t, entryBy["plan"].NeedsArgs)
	assert.False(t, entryBy["hello"].NeedsArgs)
}
