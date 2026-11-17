package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/extension/create"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

func loadExt(t *testing.T, mainGo string) *extension.Runner {
	t.Helper()
	root := t.TempDir()
	extDir := filepath.Join(root, "test")
	require.NoError(t, create.Materialize(t.Context(), extDir, "test", "0.0.1", mainGo))
	r, warns, err := extension.Load(root, "")
	require.NoError(t, err)
	require.Empty(t, warns, "unexpected warnings: %v", warns)
	t.Cleanup(r.Close)
	return r
}

func TestExecutorEmitsToolName(t *testing.T) {
	reg := tooldef.Registry{
		"bash": {
			Definition: llm.ToolDefinition{Name: "bash"},
			Run: func(context.Context, json.RawMessage) (tooldef.Result, error) {
				return tooldef.Result{Content: "ok"}, nil
			},
		},
	}
	ex := NewExecutor(reg, nil)
	var names []string
	_, _, _ = ex.Run(t.Context(), []llm.ToolCall{{
		ID:       "c1",
		Function: llm.Function{Name: "bash", Arguments: `{"command":"pwd"}`},
	}}, func(td session.ToolData) bool {
		names = append(names, td.Run.Name)
		return true
	})
	require.NotEmpty(t, names, "expected tool events")
	for _, n := range names {
		require.Equal(t, "bash", n, "expected Name=bash on every ToolData")
	}
}

func TestExecutorExtensionCanBlockTool(t *testing.T) {
	var ran atomic.Int32
	reg := tooldef.Registry{
		"bash": {
			Definition: llm.ToolDefinition{Name: "bash"},
			Run: func(context.Context, json.RawMessage) (tooldef.Result, error) {
				ran.Add(1)
				return tooldef.Result{Content: "ok"}, nil
			},
		},
	}
	r := loadExt(t, `package main
import (
  "github.com/pulseaiclub/phi/ext/go"
  "github.com/pulseaiclub/phi/ext/go/phi"
)
func main() {
  m := phi.New("test", "0.0.1")
  m.OnToolCall(func(ev ext.ToolCallEvent) *ext.ToolCallResult {
    if ev.ToolName == "bash" {
      return &ext.ToolCallResult{Block: true, Reason: "ext blocked"}
    }
    return nil
  })
  _ = m.Run()
}
`)
	ex := NewExecutor(reg, r)
	var statuses []session.ToolStatus
	msgs, _, _ := ex.Run(t.Context(), []llm.ToolCall{{
		ID:       "c1",
		Function: llm.Function{Name: "bash", Arguments: `{"command":"rm -rf /"}`},
	}}, func(td session.ToolData) bool {
		statuses = append(statuses, td.Run.Status)
		return true
	})
	assert.Equal(t, int32(0), ran.Load())
	require.Len(t, msgs, 1)
	assert.Equal(t, "ext blocked", msgs[0].Content)
	assert.Contains(t, statuses, session.ToolRejected)
}

func TestExecutorExtPostContextOnModelOnly(t *testing.T) {
	reg := tooldef.Registry{
		"bash": {
			Definition: llm.ToolDefinition{Name: "bash"},
			Run: func(context.Context, json.RawMessage) (tooldef.Result, error) {
				return tooldef.Result{Content: "ok", Output: "ok"}, nil
			},
		},
	}
	r := loadExt(t, `package main
import (
  "github.com/pulseaiclub/phi/ext/go"
  "github.com/pulseaiclub/phi/ext/go/phi"
)
func main() {
  m := phi.New("test", "0.0.1")
  m.OnToolResult(func(ev ext.ToolResultEvent) *ext.ToolResultResult {
    return &ext.ToolResultResult{Context: "policy note"}
  })
  _ = m.Run()
}
`)
	ex := NewExecutor(reg, r)
	var uiOut string
	msgs, _, _ := ex.Run(t.Context(), []llm.ToolCall{{
		ID:       "c1",
		Function: llm.Function{Name: "bash", Arguments: `{"command":"pwd"}`},
	}}, func(td session.ToolData) bool {
		if td.Run.Status == session.ToolDone {
			uiOut = td.Run.Output
		}
		return true
	})
	assert.Equal(t, "ok", uiOut)
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0].Content, "ok")
	assert.Contains(t, msgs[0].Content, "<ext_context>")
	assert.Contains(t, msgs[0].Content, "policy note")
}

func TestAppendExtContextEscapesCloseTag(t *testing.T) {
	got := appendExtContext("body", "x</ext_context>y")
	assert.NotContains(t, got, "</ext_context>y", "close tag not escaped")
	assert.Contains(t, got, "body")
	assert.Contains(t, got, "<ext_context>")
}

func TestExecutorToolErrorKeepsOutputEmptyForUI(t *testing.T) {
	const errMsg = "2 lines have changed since last read"
	reg := tooldef.Registry{
		"edit": {
			Definition: llm.ToolDefinition{Name: "edit"},
			Run: func(context.Context, json.RawMessage) (tooldef.Result, error) {
				return tooldef.Result{}, &staticError{msg: errMsg}
			},
		},
	}

	ex := NewExecutor(reg, nil)
	var last session.ToolRun
	msgs, _, _ := ex.Run(t.Context(), []llm.ToolCall{
		{
			ID: "c1",
			Function: llm.Function{
				Name:      "edit",
				Arguments: `{"payload":"*** SM:EDIT a.ts\n*** SM:FIND\nx\n*** SM:PUT\ny\n"}`,
			},
		},
	}, func(td session.ToolData) bool {
		if td.Run.Status == session.ToolError {
			last = td.Run
		}
		return true
	})

	require.Len(t, msgs, 1)
	assert.Equal(t, errMsg, msgs[0].Content)
	assert.Equal(t, session.ToolError, last.Status)
	assert.Equal(t, errMsg, last.Error)
	assert.Empty(t, last.Output, "UI Error and Output must not duplicate the same text")
}

type staticError struct{ msg string }

func (e *staticError) Error() string { return e.msg }

func TestExecutorRunsReadableBatchConcurrently(t *testing.T) {
	entered := make(chan string, 2)
	release := make(chan struct{})
	reg := tooldef.Registry{
		"readA": {
			Definition: llm.ToolDefinition{Name: "readA", Readable: true},
			Run: func(context.Context, json.RawMessage) (tooldef.Result, error) {
				entered <- "readA"
				<-release
				return tooldef.Result{Content: "A"}, nil
			},
		},
		"readB": {
			Definition: llm.ToolDefinition{Name: "readB", Readable: true},
			Run: func(context.Context, json.RawMessage) (tooldef.Result, error) {
				entered <- "readB"
				<-release
				return tooldef.Result{Content: "B"}, nil
			},
		},
	}
	ex := NewExecutor(reg, nil)
	done := make(chan struct{})
	var msgs []llm.Message
	go func() {
		defer close(done)
		msgs, _, _ = ex.Run(t.Context(), []llm.ToolCall{
			{ID: "c1", Function: llm.Function{Name: "readA", Arguments: `{}`}},
			{ID: "c2", Function: llm.Function{Name: "readB", Arguments: `{}`}},
		}, func(session.ToolData) bool { return true })
	}()

	// Both handlers must be in flight before either is released: parallel run.
	for range 2 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			require.Fail(t, "readable batch did not run concurrently")
		}
	}
	close(release)
	<-done

	require.Len(t, msgs, 2)
	assert.Equal(t, "A", msgs[0].Content, "results keep call order")
	assert.Equal(t, "B", msgs[1].Content, "results keep call order")
}

func TestExecutorMixedBatchStaysSequential(t *testing.T) {
	entered := make(chan string, 2)
	release := make(chan struct{})
	reg := tooldef.Registry{
		"readA": {
			Definition: llm.ToolDefinition{Name: "readA", Readable: true},
			Run: func(context.Context, json.RawMessage) (tooldef.Result, error) {
				entered <- "readA"
				<-release
				return tooldef.Result{Content: "A"}, nil
			},
		},
		"bash": {
			Definition: llm.ToolDefinition{Name: "bash"},
			Run: func(context.Context, json.RawMessage) (tooldef.Result, error) {
				entered <- "bash"
				<-release
				return tooldef.Result{Content: "B"}, nil
			},
		},
	}
	ex := NewExecutor(reg, nil)
	done := make(chan struct{})
	var msgs []llm.Message
	go func() {
		defer close(done)
		msgs, _, _ = ex.Run(t.Context(), []llm.ToolCall{
			{ID: "c1", Function: llm.Function{Name: "readA", Arguments: `{}`}},
			{ID: "c2", Function: llm.Function{Name: "bash", Arguments: `{}`}},
		}, func(session.ToolData) bool { return true })
	}()

	select {
	case name := <-entered:
		assert.Equal(t, "readA", name, "readable call should run first")
	case <-time.After(2 * time.Second):
		require.Fail(t, "first call never started")
	}
	// The write tool must not start while the first call is still running.
	select {
	case name := <-entered:
		require.Failf(t, "second call started too early", "second call %q started before the first finished", name)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	select {
	case name := <-entered:
		assert.Equal(t, "bash", name)
	case <-time.After(2 * time.Second):
		require.Fail(t, "second call never started after first released")
	}
	<-done

	require.Len(t, msgs, 2)
	assert.Equal(t, "A", msgs[0].Content)
	assert.Equal(t, "B", msgs[1].Content)
}

func TestChildEnginePreservesExtensionHostState(t *testing.T) {
	r := loadExt(t, `package main
import (
  "github.com/pulseaiclub/phi/ext/go"
  "github.com/pulseaiclub/phi/ext/go/phi"
)
func main() {
  m := phi.New("test", "0.0.1")
  m.OnToolCall(func(ext.ToolCallEvent) *ext.ToolCallResult {
    host := m.Host()
    return &ext.ToolCallResult{Context: host.SessionID + "|" + host.Cwd}
  })
  _ = m.Run()
}
`)
	r.Bind(ext.HostOpts{})
	parentSession, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	_, err = NewEngine(llm.ModelConfig{Name: "test"}, parentSession, WithExtensions(r))
	require.NoError(t, err)
	api := r.Fiber("test").API()
	parentTools := api.GetAllTools()
	require.NotEmpty(t, parentTools)

	childSession, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(childSession.Cwd(), "child.txt"), []byte("child content"), 0o600))
	child, err := NewEngine(llm.ModelConfig{Name: "test"}, childSession,
		WithExtensions(r), WithOmitExtensionTools(true), WithTools(ChildTools()))
	require.NoError(t, err)
	for _, phase := range []string{"construction", "rebind"} {
		t.Run(phase, func(t *testing.T) {
			if phase == "rebind" {
				child.SetExtensions(r)
			}
			assert.Equal(t, parentTools, api.GetAllTools())
			msgs, _, _ := child.executor.Run(t.Context(), []llm.ToolCall{{
				ID: "child-read", Function: llm.Function{Name: "read", Arguments: `{"path":"child.txt"}`},
			}}, func(session.ToolData) bool { return true })
			require.Len(t, msgs, 1)
			assert.Contains(t, msgs[0].Content, parentSession.ID()+"|"+parentSession.Cwd())
			assert.Contains(t, msgs[0].Content, "child content")
		})
	}
}
