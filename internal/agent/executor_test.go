package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

func TestExecutorEmitsToolName(t *testing.T) {
	reg := tooldef.Registry{
		"bash": {
			Definition: llm.ToolDefinition{Name: "bash"},
			Run: func(context.Context, json.RawMessage) (tooldef.Result, error) {
				return tooldef.Result{Content: "ok"}, nil
			},
		},
	}
	ex := NewExecutor(reg)
	var names []string
	ex.Run(t.Context(), []llm.ToolCall{{
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

	ex := NewExecutor(reg)
	var last session.ToolRun
	msgs := ex.Run(t.Context(), []llm.ToolCall{{
		ID: "c1",
		Function: llm.Function{
			Name:      "edit",
			Arguments: `{"payload":"*** SM:EDIT a.ts\n*** SM:FIND\nx\n*** SM:PUT\ny\n"}`,
		},
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
	ex := NewExecutor(reg)
	done := make(chan struct{})
	var msgs []llm.Message
	go func() {
		defer close(done)
		msgs = ex.Run(t.Context(), []llm.ToolCall{
			{ID: "c1", Function: llm.Function{Name: "readA", Arguments: `{}`}},
			{ID: "c2", Function: llm.Function{Name: "readB", Arguments: `{}`}},
		}, func(session.ToolData) bool { return true })
	}()

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
	ex := NewExecutor(reg)
	done := make(chan struct{})
	var msgs []llm.Message
	go func() {
		defer close(done)
		msgs = ex.Run(t.Context(), []llm.ToolCall{
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

func TestChildToolReadsRelativeToJobWorkDir(t *testing.T) {
	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "child.txt"), []byte("child content"), 0o600))
	meta := job.Meta{Dir: t.TempDir(), WorkDir: workDir}
	sess, err := newJobSession(meta)
	require.NoError(t, err)
	child, err := (EngineRunner{}).newEngine(meta, SpecForRole(job.RoleExplore), sess)
	require.NoError(t, err)
	msgs := child.executor.Run(t.Context(), []llm.ToolCall{{
		ID: "child-read", Function: llm.Function{Name: "read", Arguments: `{"path":"child.txt"}`},
	}}, func(session.ToolData) bool { return true })
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0].Content, "child content")
}
