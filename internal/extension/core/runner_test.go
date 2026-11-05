package core

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

func TestToolFromDefReadable(t *testing.T) {
	got := toolFromDef(ext.Tool{Name: "read", Readable: true})
	assert.True(t, got.Definition.Readable)
	assert.Equal(t, "read", got.Definition.Name)
	assert.False(t, toolFromDef(ext.Tool{Name: "write"}).Definition.Readable)
}

func TestPrepareRunsAfterEveryRewrite(t *testing.T) {
	r := NewRunner("")
	first, observe := handBuiltFiber("a-observe")
	last, rewrite := handBuiltFiber("z-rewrite")
	var phases []string
	observe.On(ext.EventToolPrepare, func(ev ext.ToolCallEvent, _ *ext.Context) *ext.ToolCallResult {
		phases = append(phases, "prepare")
		assert.JSONEq(t, `"b"`, string(ev.Input))
		assert.Equal(t, "execution", ev.ExecutionID)
		return nil
	})
	rewrite.On(ext.EventToolCall, func(ext.ToolCallEvent, *ext.Context) *ext.ToolCallResult {
		phases = append(phases, "rewrite")
		return &ext.ToolCallResult{Input: json.RawMessage(`"b"`)}
	})
	r.AddFiber(first)
	r.AddFiber(last)
	declared := tooldef.NewTool(tooldef.WithPermission(tooldef.Declaration[string]{
		Kind:  permission.KindWrite,
		Paths: func(path string) ([]string, error) { return []string{path}, nil },
	}))
	cwd := t.TempDir()
	observe.On(ext.EventToolPrepare, func(ev ext.ToolCallEvent, _ *ext.Context) *ext.ToolCallResult {
		assert.Equal(t, []string{filepath.Join(cwd, "b")}, ev.Paths)
		return nil
	})
	input, blocked, _, _ := r.PreTool(
		tooldef.WithCwd(t.Context(), cwd),
		"write",
		"write",
		json.RawMessage(`"a"`),
		"execution",
		declared.Permission,
	)
	assert.False(t, blocked)
	assert.JSONEq(t, `"b"`, string(input))
	assert.Equal(t, []string{"rewrite", "prepare"}, phases)
}

func TestPrepareRejectsArgumentChangesAndTransportFailures(t *testing.T) {
	r := NewRunner("")
	f, api := handBuiltFiber("prepare")
	api.On(ext.EventToolPrepare, func(ext.ToolCallEvent, *ext.Context) *ext.ToolCallResult {
		return &ext.ToolCallResult{Input: json.RawMessage(`"changed"`)}
	})
	r.AddFiber(f)
	input, blocked, reason, _ := r.PreTool(t.Context(), "write", "write", json.RawMessage(`"original"`), "execution")
	require.True(t, blocked)
	assert.Contains(t, reason, "cannot rewrite")
	assert.JSONEq(t, `"original"`, string(input))
	r = NewRunner("")
	f, api = handBuiltFiber("closed")
	p := &Proc{intercept: map[uint16]struct{}{pxb.EvToolPrepare: {}}}
	p.closed.Store(true)
	p.registerInterceptShims(api)
	r.AddFiber(f)
	_, blocked, reason, _ = r.PreTool(t.Context(), "write", "write", nil, "execution")
	assert.True(t, blocked)
	assert.Contains(t, reason, "tool_prepare failed")
}
