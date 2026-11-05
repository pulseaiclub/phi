package agent

import (
	"context"
	"errors"
	"iter"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

func TestLoopStopsDuringCompaction(t *testing.T) {
	for _, stopAtStart := range []bool{true, false} {
		name := "failed completion"
		if stopAtStart {
			name = "start"
		}
		t.Run(name, func(t *testing.T) {
			sess, err := NewSession(WithCwd(t.TempDir()))
			require.NoError(t, err)
			require.NoError(t, sess.Append(
				llm.Message{Role: llm.RoleUser, Content: compactionSeed},
				llm.Message{Role: llm.RoleAssistant, Content: compactionSeed},
				llm.Message{Role: llm.RoleUser, Content: compactionSeed},
			))
			engine, err := NewEngine(llm.ModelConfig{Name: "stub", ContextWindow: 40_000}, sess,
				WithTools([]tooldef.Tool{}))
			require.NoError(t, err)
			var compacts int
			engine.client = fakeCompactClient{compact: func(context.Context) error {
				compacts++
				return errors.New("summary failed")
			}}
			var events int
			for event, err := range engine.Loop(t.Context(), "go", LoopOpts{}) {
				require.NoError(t, err)
				events++
				if stopAtStart {
					require.IsType(t, session.CompactionStarted{}, event)
					break
				}
				if complete, ok := event.(session.CompactionComplete); ok {
					require.True(t, complete.Failed)
					break
				}
			}
			if stopAtStart {
				require.Equal(t, 1, events)
				require.Zero(t, compacts)
			} else {
				require.Equal(t, 2, events)
				require.Equal(t, 1, compacts)
			}
			requireNoCompactionEntry(t, sess)
		})
	}
}

func TestLoopStopsToolBatchWithoutRunningTools(t *testing.T) {
	for _, readable := range []bool{false, true} {
		name := "sequential"
		if readable {
			name = "concurrent"
		}
		t.Run(name, func(t *testing.T) {
			sess, err := NewSession(WithCwd(t.TempDir()))
			require.NoError(t, err)
			var runs atomic.Int32
			tool := countingTool(&runs)
			tool.Definition.Readable = readable
			engine, err := NewEngine(llm.ModelConfig{Name: "stub"}, sess,
				WithTools([]tooldef.Tool{tool}), WithGate(permission.AllowAll{}))
			require.NoError(t, err)
			client := &stubChatClient{streams: []func(context.Context, []llm.Message) iter.Seq2[llm.StreamEvent, error]{
				stubMessageStream(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
					{ID: "first", Type: "function", Function: llm.Function{Name: "count", Arguments: `{}`}},
					{ID: "second", Type: "function", Function: llm.Function{Name: "count", Arguments: `{}`}},
				}}),
			}}
			engine.client = client
			var toolEvents int
			for event, err := range engine.Loop(t.Context(), "go", LoopOpts{}) {
				require.NoError(t, err)
				if _, ok := event.(session.ToolData); ok {
					toolEvents++
					break
				}
			}
			require.Equal(t, 1, toolEvents)
			require.Zero(t, runs.Load())
			require.Len(t, client.calls, 1)
			require.NoError(t, t.Context().Err(), "stopping a consumer must not cancel its parent")
		})
	}
}
