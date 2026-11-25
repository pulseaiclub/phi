package transcript_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/block"
	"github.com/pulseaiclub/phi/internal/components/status"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
	"github.com/pulseaiclub/phi/internal/tui/transcript"
)

func TestMapperAgentBlockSummaryAndChildren(t *testing.T) {
	store := transcript.NewSubagentStore()
	store.ApplyProgress(job.Progress{
		JobID:           "job_1",
		ParentToolUseID: "call_agent",
		ToolUseID:       "c1",
		Name:            "read",
		Status:          "done",
		Detail:          "x.go",
	})

	m := transcript.NewMapper(components.DefaultTheme(), nil, nil)
	m.Children = store.Children
	m.ChildrenByJob = store.ChildrenByJob

	snap := session.Snapshot{
		Messages: []session.Message{{
			ID:    "m1",
			Role:  session.RoleAssistant,
			State: session.StateComplete,
			Content: []session.ContentBlock{{
				Type:     session.BlockToolUse,
				ID:       "call_agent",
				Name:     "agent_spawn",
				Input:    `{"prompt":"p"}`,
				Complete: true,
			}},
		}},
		Tools: map[string]session.ToolRun{
			"call_agent": {
				ToolUseID: "call_agent",
				Name:      "agent_spawn",
				Status:    session.ToolDone,
				Detail:    "completed",
				Output: `{
  "job_id": "job_1",
  "status": "completed",
  "summary": "## Findings\n\n- ok"
}`,
			},
		},
	}

	entries, ids, dirty := m.Sync(nil, nil, snap)
	require.Len(t, entries, 1)
	require.Len(t, ids, 1)
	require.Equal(t, []int{0}, dirty)
	ab, ok := entries[0].(*block.AgentBlock)
	require.True(t, ok, "expected *block.AgentBlock, got %T", entries[0])
	assert.NotEmpty(t, ab.Summary)
	assert.Contains(t, ab.Summary, "Findings")
	require.Len(t, ab.Children, 1)
	require.Equal(t, "read", ab.Children[0].Name)
	require.Equal(t, status.ToolDone, ab.Status)
	assert.True(t, ab.HideName, "spawn title must not repeat the tool name")
	surf := ab.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 40}})
	txt := components.SurfaceText(surf)
	assert.NotContains(t, txt, `"job_id"`, "raw json leaked")
	assert.NotContains(t, txt, "agent_spawn", "tool name is noise next to the label")
}

func TestMapperAgentWaitSummaryOnly(t *testing.T) {
	store := transcript.NewSubagentStore()
	store.ApplyProgress(job.Progress{
		JobID:           "job_w",
		ParentToolUseID: "call_spawn",
		ToolUseID:       "c1",
		Name:            "grep",
		Status:          "done",
		Detail:          "Tree",
	})

	m := transcript.NewMapper(components.DefaultTheme(), nil, nil)
	m.Children = store.Children
	m.ChildrenByJob = store.ChildrenByJob

	// In-progress wait: title only, no duplicated child tree.
	snap := session.Snapshot{
		Messages: []session.Message{{
			ID:    "m1",
			Role:  session.RoleAssistant,
			State: session.StateComplete,
			Content: []session.ContentBlock{{
				Type:     session.BlockToolUse,
				ID:       "call_wait",
				Name:     "agent_wait",
				Input:    `{"job_id":"job_w"}`,
				Complete: true,
			}},
		}},
		Tools: map[string]session.ToolRun{
			"call_wait": {
				ToolUseID: "call_wait",
				Name:      "agent_wait",
				Status:    session.ToolInProgress,
				Detail:    "job_w",
			},
		},
	}

	entries, _, _ := m.Sync(nil, nil, snap)
	ab, ok := entries[0].(*block.AgentBlock)
	require.True(t, ok, "expected *block.AgentBlock, got %T", entries[0])
	require.Empty(t, ab.Children, "wait must not show spawn children")

	// Done wait: markdown summary only.
	snap.Tools["call_wait"] = session.ToolRun{
		ToolUseID: "call_wait",
		Name:      "agent_wait",
		Status:    session.ToolDone,
		Detail:    "completed",
		Output: `{
  "job_id": "job_w",
  "status": "completed",
  "summary": "## Done\n\n- ok"
}`,
	}
	entries, _, dirty := m.Sync(entries, []string{"call_wait"}, snap)
	require.Equal(t, []int{0}, dirty, "dirty after summary change")
	ab, ok = entries[0].(*block.AgentBlock)
	require.True(t, ok, "expected *block.AgentBlock, got %T", entries[0])
	require.Empty(t, ab.Children, "wait children still set")
	assert.Contains(t, ab.Summary, "Done")
	require.True(t, ab.Expanded, "expected expand when summary present")
}

// A wait row carries only a job id in its args: the title has to show the job's
// label, not the id, while the wait is still blocking.
func TestMapperAgentWaitRowShowsJobLabel(t *testing.T) {
	store := transcript.NewSubagentStore()
	store.ApplyResult("call_spawn", agenttool.ParseAgentResult(`{
		"job_id":"job_w","status":"running","label":"explore: find auth"
	}`))

	m := transcript.NewMapper(components.DefaultTheme(), nil, nil)
	m.Children = store.Children
	m.ChildrenByJob = store.ChildrenByJob
	m.LabelByJob = store.Label

	snap := session.Snapshot{
		Messages: []session.Message{{
			ID:    "m1",
			Role:  session.RoleAssistant,
			State: session.StateComplete,
			Content: []session.ContentBlock{{
				Type:     session.BlockToolUse,
				ID:       "call_wait",
				Name:     "agent_wait",
				Input:    `{"job_id":"job_w"}`,
				Complete: true,
			}},
		}},
		Tools: map[string]session.ToolRun{
			"call_wait": {
				ToolUseID: "call_wait",
				Name:      "agent_wait",
				Status:    session.ToolInProgress,
				Detail:    "job_w",
			},
		},
	}

	entries, _, _ := m.Sync(nil, nil, snap)
	ab, ok := entries[0].(*block.AgentBlock)
	require.True(t, ok, "expected *block.AgentBlock, got %T", entries[0])
	assert.Equal(t, "explore: find auth", ab.Detail)
	assert.False(t, ab.HideName, "wait rows keep the tool name to stay distinct")

	// Unknown job: the raw detail survives instead of rendering an empty title.
	snap.Tools["call_wait"] = session.ToolRun{
		ToolUseID: "call_wait",
		Name:      "agent_wait",
		Status:    session.ToolInProgress,
		Detail:    "job_unknown",
	}
	entries, _, _ = m.Sync(entries, []string{"call_wait"}, snap)
	ab, ok = entries[0].(*block.AgentBlock)
	require.True(t, ok, "expected *block.AgentBlock, got %T", entries[0])
	assert.Equal(t, "job_unknown", ab.Detail)
}

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
