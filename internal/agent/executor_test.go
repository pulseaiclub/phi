package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools"
)

func loadExt(t *testing.T, mainGo string) *extension.Runner {
	t.Helper()
	root := t.TempDir()
	extDir := filepath.Join(root, "test")
	require.NoError(t, extension.Materialize(t.Context(), extDir, "test", "0.0.1", mainGo))
	r, warns, err := extension.Load(root, "")
	require.NoError(t, err)
	require.Empty(t, warns, "unexpected warnings: %v", warns)
	t.Cleanup(r.Close)
	return r
}

type fixedGate struct {
	dec    permission.Decision
	reason string
}

func (g fixedGate) Check(context.Context, permission.Request) (permission.Decision, string) {
	return g.dec, g.reason
}

func TestExecutorDenyDoesNotRunHandler(t *testing.T) {
	var ran atomic.Int32
	reg := tools.Registry{
		"bash": {
			Definition: llm.ToolDefinition{Name: "bash"},
			Run: func(context.Context, json.RawMessage) (tools.Result, error) {
				ran.Add(1)
				return tools.Result{Content: "ok"}, nil
			},
		},
	}
	ex := NewExecutor(reg, fixedGate{dec: permission.Deny, reason: "denied by test"}, nil, nil)
	var statuses []session.ToolStatus
	msgs, _, _ := ex.Run(t.Context(), []llm.ToolCall{{
		ID:       "c1",
		Function: llm.Function{Name: "bash", Arguments: `{"command":"echo hi"}`},
	}}, func(td session.ToolData) bool {
		statuses = append(statuses, td.Run.Status)
		return true
	})
	require.Equal(t, int32(0), ran.Load(), "handler should not run on deny")
	require.Len(t, msgs, 1)
	require.Equal(t, "denied by test", msgs[0].Content)
	require.Contains(t, statuses, session.ToolRejected)
}

func TestExecutorAskFalseRejects(t *testing.T) {
	var ran atomic.Int32
	reg := tools.Registry{
		"bash": {
			Definition: llm.ToolDefinition{Name: "bash"},
			Run: func(context.Context, json.RawMessage) (tools.Result, error) {
				ran.Add(1)
				return tools.Result{Content: "ok"}, nil
			},
		},
	}
	ask := func(context.Context, permission.Request, string) (permission.AskResult, error) {
		return permission.AskResult{Approved: false}, nil
	}
	ex := NewExecutor(reg, fixedGate{dec: permission.Ask, reason: "needs approval"}, ask, nil)
	msgs, _, _ := ex.Run(t.Context(), []llm.ToolCall{{
		ID:       "c1",
		Function: llm.Function{Name: "bash", Arguments: `{"command":"curl x"}`},
	}}, func(session.ToolData) bool { return true })
	require.Equal(t, int32(0), ran.Load(), "handler should not run when ask denied")
	require.Len(t, msgs, 1)
	require.NotEmpty(t, msgs[0].Content, "expected rejection message")
}

func TestExecutorEmitsToolName(t *testing.T) {
	reg := tools.Registry{
		"bash": {
			Definition: llm.ToolDefinition{Name: "bash"},
			Run: func(context.Context, json.RawMessage) (tools.Result, error) {
				return tools.Result{Content: "ok"}, nil
			},
		},
	}
	ex := NewExecutor(reg, permission.AllowAll{}, nil, nil)
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

func TestExecutorAskNilRejectsHeadless(t *testing.T) {
	// Headless mode wires Ask=nil: an Ask decision must reject without
	// running the handler (Ask≡Deny), even if the gate did not fold it.
	var ran atomic.Int32
	reg := tools.Registry{
		"bash": {
			Definition: llm.ToolDefinition{Name: "bash"},
			Run: func(context.Context, json.RawMessage) (tools.Result, error) {
				ran.Add(1)
				return tools.Result{Content: "ok"}, nil
			},
		},
	}
	ex := NewExecutor(reg, fixedGate{dec: permission.Ask, reason: "needs approval"}, nil, nil)
	var statuses []session.ToolStatus
	msgs, _, _ := ex.Run(t.Context(), []llm.ToolCall{{
		ID:       "c1",
		Function: llm.Function{Name: "bash", Arguments: `{"command":"rm -rf /tmp/x"}`},
	}}, func(td session.ToolData) bool {
		statuses = append(statuses, td.Run.Status)
		return true
	})
	require.Equal(t, int32(0), ran.Load(), "handler should not run when ask handler is nil (headless)")
	require.Len(t, msgs, 1)
	require.NotEmpty(t, msgs[0].Content, "expected rejection message")
	require.Contains(t, statuses, session.ToolRejected)
}

func TestExecutorAskTrueRuns(t *testing.T) {
	var ran atomic.Int32
	reg := tools.Registry{
		"bash": {
			Definition: llm.ToolDefinition{Name: "bash"},
			Run: func(context.Context, json.RawMessage) (tools.Result, error) {
				ran.Add(1)
				return tools.Result{Content: "ran"}, nil
			},
		},
	}
	ask := func(context.Context, permission.Request, string) (permission.AskResult, error) {
		return permission.AskResult{Approved: true}, nil
	}
	ex := NewExecutor(reg, fixedGate{dec: permission.Ask, reason: "needs approval"}, ask, nil)
	msgs, _, _ := ex.Run(t.Context(), []llm.ToolCall{{
		ID:       "c1",
		Function: llm.Function{Name: "bash", Arguments: `{"command":"curl x"}`},
	}}, func(session.ToolData) bool { return true })
	require.Equal(t, int32(1), ran.Load(), "handler should run when ask approved")
	require.Len(t, msgs, 1)
	require.Equal(t, "ran", msgs[0].Content)
}

func TestExecutorAskFeedbackMessage(t *testing.T) {
	reg := tools.Registry{
		"bash": {
			Definition: llm.ToolDefinition{Name: "bash"},
			Run: func(context.Context, json.RawMessage) (tools.Result, error) {
				return tools.Result{Content: "ok"}, nil
			},
		},
	}
	ask := func(context.Context, permission.Request, string) (permission.AskResult, error) {
		return permission.AskResult{Approved: false, Feedback: "use go test instead"}, nil
	}
	ex := NewExecutor(reg, fixedGate{dec: permission.Ask, reason: "ask me"}, ask, nil)
	msgs, _, _ := ex.Run(t.Context(), []llm.ToolCall{{
		ID:       "c1",
		Function: llm.Function{Name: "bash", Arguments: `{"command":"curl x"}`},
	}}, func(session.ToolData) bool { return true })
	require.Len(t, msgs, 1)
	require.Contains(t, msgs[0].Content, "use go test instead")
}

func TestExecutorNilAskOnAskDenies(t *testing.T) {
	reg := tools.Registry{
		"bash": {
			Definition: llm.ToolDefinition{Name: "bash"},
			Run: func(context.Context, json.RawMessage) (tools.Result, error) {
				return tools.Result{Content: "ok"}, nil
			},
		},
	}
	ex := NewExecutor(reg, fixedGate{dec: permission.Ask, reason: "ask me"}, nil, nil)
	msgs, _, _ := ex.Run(t.Context(), []llm.ToolCall{{
		ID:       "c1",
		Function: llm.Function{Name: "bash", Arguments: `{"command":"curl x"}`},
	}}, func(session.ToolData) bool { return true })
	require.Len(t, msgs, 1)
	require.NotEmpty(t, msgs[0].Content, "expected deny message")
}

func TestExecutorExtDenySkipsGateAsk(t *testing.T) {
	var ran atomic.Int32
	var askCalled atomic.Int32
	reg := tools.Registry{
		"bash": {
			Definition: llm.ToolDefinition{Name: "bash"},
			Run: func(context.Context, json.RawMessage) (tools.Result, error) {
				ran.Add(1)
				return tools.Result{Content: "ok"}, nil
			},
		},
	}
	ask := func(context.Context, permission.Request, string) (permission.AskResult, error) {
		askCalled.Add(1)
		return permission.AskResult{Approved: true}, nil
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
	ex := NewExecutor(reg, fixedGate{dec: permission.Ask, reason: "needs approval"}, ask, r)
	var statuses []session.ToolStatus
	msgs, _, _ := ex.Run(t.Context(), []llm.ToolCall{{
		ID:       "c1",
		Function: llm.Function{Name: "bash", Arguments: `{"command":"rm -rf /"}`},
	}}, func(td session.ToolData) bool {
		statuses = append(statuses, td.Run.Status)
		return true
	})
	assert.Equal(t, int32(0), ran.Load())
	assert.Equal(t, int32(0), askCalled.Load())
	require.Len(t, msgs, 1)
	assert.Equal(t, "ext blocked", msgs[0].Content)
	assert.Contains(t, statuses, session.ToolRejected)
}

func TestExecutorExtModifySeenByGateAndRun(t *testing.T) {
	var sawArgs string
	reg := tools.Registry{
		"bash": {
			Definition: llm.ToolDefinition{Name: "bash"},
			DetailFromArgs: func(input json.RawMessage) string {
				var in struct {
					Command string `json:"command"`
				}
				_ = json.Unmarshal(input, &in)
				return in.Command
			},
			Run: func(_ context.Context, input json.RawMessage) (tools.Result, error) {
				sawArgs = string(input)
				return tools.Result{Content: "ran", Output: "ran"}, nil
			},
		},
	}
	gate := &recordingGate{}
	r := loadExt(t, `package main
import (
  "encoding/json"
  "github.com/pulseaiclub/phi/ext/go"
  "github.com/pulseaiclub/phi/ext/go/phi"
)
func main() {
  m := phi.New("test", "0.0.1")
  m.OnToolCall(func(ev ext.ToolCallEvent) *ext.ToolCallResult {
    return &ext.ToolCallResult{Input: json.RawMessage(`+"`"+`{"command":"echo safe"}`+"`"+`)}
  })
  _ = m.Run()
}
`)
	ex := NewExecutor(reg, gate, nil, r)
	var detail string
	msgs, _, _ := ex.Run(t.Context(), []llm.ToolCall{{
		ID:       "c1",
		Function: llm.Function{Name: "bash", Arguments: `{"command":"rm -rf /"}`},
	}}, func(td session.ToolData) bool {
		if td.Run.Status == session.ToolDone {
			detail = td.Run.Detail
		}
		return true
	})
	assert.Equal(t, "echo safe", gate.last.Command)
	assert.Contains(t, sawArgs, "echo safe")
	assert.Equal(t, "echo safe", detail)
	require.Len(t, msgs, 1)
	assert.Equal(t, "ran", msgs[0].Content)
}

func TestExecutorExtPostContextOnModelOnly(t *testing.T) {
	reg := tools.Registry{
		"bash": {
			Definition: llm.ToolDefinition{Name: "bash"},
			Run: func(context.Context, json.RawMessage) (tools.Result, error) {
				return tools.Result{Content: "ok", Output: "ok"}, nil
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
	ex := NewExecutor(reg, permission.AllowAll{}, nil, r)
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

type recordingGate struct {
	last permission.Request
}

func (g *recordingGate) Check(_ context.Context, req permission.Request) (permission.Decision, string) {
	g.last = req
	return permission.Allow, ""
}

func TestExecutorToolErrorKeepsOutputEmptyForUI(t *testing.T) {
	const errMsg = "2 lines have changed since last read"
	reg := tools.Registry{
		"edit": {
			Definition: llm.ToolDefinition{Name: "edit"},
			Run: func(context.Context, json.RawMessage) (tools.Result, error) {
				return tools.Result{}, &staticError{msg: errMsg}
			},
		},
	}

	ex := NewExecutor(reg, permission.AllowAll{}, nil, nil)
	var last session.ToolRun
	msgs, _, _ := ex.Run(t.Context(), []llm.ToolCall{{
		ID:       "c1",
		Function: llm.Function{Name: "edit", Arguments: `{}`},
	}}, func(td session.ToolData) bool {
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
	reg := tools.Registry{
		"readA": {
			Definition: llm.ToolDefinition{Name: "readA", Readable: true},
			Run: func(context.Context, json.RawMessage) (tools.Result, error) {
				entered <- "readA"
				<-release
				return tools.Result{Content: "A"}, nil
			},
		},
		"readB": {
			Definition: llm.ToolDefinition{Name: "readB", Readable: true},
			Run: func(context.Context, json.RawMessage) (tools.Result, error) {
				entered <- "readB"
				<-release
				return tools.Result{Content: "B"}, nil
			},
		},
	}
	ex := NewExecutor(reg, permission.AllowAll{}, nil, nil)
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
	reg := tools.Registry{
		"readA": {
			Definition: llm.ToolDefinition{Name: "readA", Readable: true},
			Run: func(context.Context, json.RawMessage) (tools.Result, error) {
				entered <- "readA"
				<-release
				return tools.Result{Content: "A"}, nil
			},
		},
		"bash": {
			Definition: llm.ToolDefinition{Name: "bash"},
			Run: func(context.Context, json.RawMessage) (tools.Result, error) {
				entered <- "bash"
				<-release
				return tools.Result{Content: "B"}, nil
			},
		},
	}
	ex := NewExecutor(reg, permission.AllowAll{}, nil, nil)
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
