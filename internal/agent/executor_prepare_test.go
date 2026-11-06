package agent

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/session"
)

func TestExecutorPrepareUsesFinalArgsAndDistinctExecutionIDs(t *testing.T) {
	r := loadExt(t, `package main
import (
  "encoding/json"
  "github.com/pulseaiclub/phi/ext/go"
  "github.com/pulseaiclub/phi/ext/go/phi"
)
func main() {
  m := phi.New("test", "0.0.1")
  m.OnToolCall(func(ev ext.ToolCallEvent) *ext.ToolCallResult {
    var in struct{ Path string }
    _ = json.Unmarshal(ev.Input, &in)
    if in.Path != "a" { return nil }
    body, _ := json.Marshal(map[string]string{"path":"b", "content":"x"})
    return &ext.ToolCallResult{Input:body}
  })
  m.OnToolPrepare(func(ev ext.ToolCallEvent) *ext.ToolCallResult {
    var in struct{ Path string }
    _ = json.Unmarshal(ev.Input, &in)
    if in.Path == "deny" { return &ext.ToolCallResult{Block:true, Reason:"prepare refused"} }
    if in.Path == "rewrite" { return &ext.ToolCallResult{Input:[]byte("{}")} }
    return &ext.ToolCallResult{Context:ev.ExecutionID}
  })
  _ = m.Run()
}
`)
	s, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	gate := &recordingGate{}
	e, err := NewEngine(llm.ModelConfig{Name: "test"}, s, WithExtensions(r), WithGate(gate))
	require.NoError(t, err)
	run := func(path string) string {
		t.Helper()
		input, err := json.Marshal(map[string]string{"path": path, "content": "x"})
		require.NoError(t, err)
		msgs, _, _ := e.executor.Run(
			t.Context(),
			[]llm.ToolCall{{ID: "write", Function: llm.Function{Name: "write", Arguments: string(input)}}},
			func(session.ToolData) bool { return true },
		)
		require.Len(t, msgs, 1)
		return msgs[0].Content
	}
	first, second := run("a"), run("a")
	assert.Contains(t, first, "<ext_context>")
	assert.NotEqual(t, first, second, "repeated provider IDs must have different host execution IDs")
	assert.Equal(t, []string{filepath.Join(s.Cwd(), "b")}, gate.last.Paths)
	assert.NoFileExists(t, filepath.Join(s.Cwd(), "a"))
	assert.FileExists(t, filepath.Join(s.Cwd(), "b"))
	gate.last = permission.Request{}
	assert.Contains(t, run("deny"), "prepare refused")
	assert.Contains(t, run("rewrite"), "cannot rewrite")
	assert.Equal(t, permission.Request{}, gate.last, "prepare vetoes must stop before Gate/Ask")
	assert.NoFileExists(t, filepath.Join(s.Cwd(), "deny"))
	assert.NoFileExists(t, filepath.Join(s.Cwd(), "rewrite"))
}
