package transcript_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/block"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/transcript"
)

func TestMapperSyncDirtyOnlyChangedRows(t *testing.T) {
	m := transcript.NewMapper(components.DefaultTheme(), nil, nil)
	snap := session.Snapshot{
		Messages: []session.Message{
			{
				ID:    "u1",
				Role:  session.RoleUser,
				State: session.StateComplete,
				Text:  "hello",
			},
			{
				ID:    "a1",
				Role:  session.RoleAssistant,
				State: session.StateStreaming,
				Content: []session.ContentBlock{
					{Type: session.BlockText, Text: "hi"},
				},
			},
		},
	}
	entries, ids, dirty := m.Sync(nil, nil, snap)
	require.Len(t, dirty, 2, "initial dirty")

	// Stream more assistant text — only assistant row should be dirty.
	snap.Messages[1].Content[0].Text = "hi there"
	entries, ids, dirty = m.Sync(entries, ids, snap)
	require.Equal(t, []int{1}, dirty, "stream dirty")
	ab, ok := entries[1].(*block.AssistantBlock)
	require.True(t, ok)
	require.Equal(t, "hi there", ab.Text)

	// No-op sync — nothing dirty.
	entries, ids, dirty = m.Sync(entries, ids, snap)
	require.Empty(t, dirty, "noop dirty")

	// Append a new user message — only the new index dirty.
	snap.Messages = append(snap.Messages, session.Message{
		ID:    "u2",
		Role:  session.RoleUser,
		State: session.StateComplete,
		Text:  "more",
	})
	_, _, dirty = m.Sync(entries, ids, snap)
	require.Equal(t, []int{2}, dirty, "append dirty")
}

func TestApplyProgressReportsChange(t *testing.T) {
	s := transcript.NewSubagentStore()
	p := job.Progress{
		JobID:           "job1",
		ParentToolUseID: "parent1",
		ToolUseID:       "c1",
		Name:            "read",
		Status:          "in-progress",
		Detail:          "a.go",
	}
	require.True(t, s.ApplyProgress(p), "first apply should change")
	require.False(t, s.ApplyProgress(p), "identical apply should not change")
	p.Status = "done"
	require.True(t, s.ApplyProgress(p), "status change should dirty")
}

func TestMapperToolExpandedFromRun(t *testing.T) {
	m := transcript.NewMapper(components.DefaultTheme(), nil, nil)
	snap := session.Snapshot{
		Messages: []session.Message{{
			ID: "a1", Role: session.RoleAssistant, State: session.StateComplete,
			Content: []session.ContentBlock{{Type: session.BlockToolUse, ID: "t1", Name: "plan"}},
		}},
		Tools: map[string]session.ToolRun{
			"t1": {
				ToolUseID: "t1", Name: "plan", Status: session.ToolDone,
				Detail: "plan", Output: "# steps\n1. a", Expanded: true,
			},
		},
	}
	entries, _, _ := m.Sync(nil, nil, snap)
	require.Len(t, entries, 1)
	tb, ok := entries[0].(*block.ToolBlock)
	require.True(t, ok)
	assert.True(t, tb.Expanded)
	assert.Equal(t, "# steps\n1. a", tb.Output)
}
