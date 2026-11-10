package session

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

func TestApplyStreamingUpdates(t *testing.T) {
	var s Snapshot
	s = Apply(s, UserAppend{ID: "u1", Text: "hello"})
	require.Len(t, s.Messages, 1)
	require.Equal(t, RoleUser, s.Messages[0].Role)

	s = Apply(s, AssistantMessageUpdate{Message: Message{
		ID: "a1", State: StateStreaming,
		Content: []ContentBlock{{Type: BlockText, Text: "Hi"}},
	}})
	require.Len(t, s.Messages, 2)
	require.True(t, IsStreaming(s))

	s = Apply(s, AssistantMessageUpdate{Message: Message{
		ID: "a1", State: StateStreaming,
		Content: []ContentBlock{{Type: BlockText, Text: "Hi there"}},
	}})
	require.Len(t, s.Messages, 2)
	require.Equal(t, "Hi there", s.Messages[1].FlatText())

	s = Apply(s, AssistantMessageUpdate{Message: Message{
		ID: "a1", State: StateComplete,
		Content: []ContentBlock{{Type: BlockText, Text: "Hi there!"}},
	}})
	require.False(t, IsStreaming(s))
	require.Equal(t, StateComplete, s.Messages[1].State)
}

func TestCancelStreaming(t *testing.T) {
	s := Snapshot{
		Messages: []Message{
			{ID: "u", Role: RoleUser, Text: "x"},
			{
				ID: "a", Role: RoleAssistant, State: StateStreaming,
				Content: []ContentBlock{{Type: BlockText, Text: "partial"}},
			},
		},
		Tools: map[string]ToolRun{
			"t1": {ToolUseID: "t1", Status: ToolInProgress},
		},
	}
	s = Apply(s, CancelStreaming{})
	require.Equal(t, StateCancelled, s.Messages[1].State)
	require.Equal(t, ToolCancelled, s.Tools["t1"].Status)
}

func TestToolDataAndSecondTurn(t *testing.T) {
	var s Snapshot
	s = Apply(s, UserAppend{Text: "go"})
	s = Apply(s, AssistantMessageUpdate{Message: Message{
		ID: "a1", State: StateComplete, StopReason: StopToolUse,
		Content: []ContentBlock{
			{Type: BlockText, Text: "calling"},
			{Type: BlockToolUse, ID: "t1", Name: "Read", Input: "a.go", Complete: true},
		},
	}})
	require.Equal(t, ToolInProgress, s.Tools["t1"].Status)
	require.Equal(t, "Read", s.Tools["t1"].Name)
	s = Apply(s, ToolData{Run: ToolRun{ToolUseID: "t1", Status: ToolDone, Output: "ok"}})
	require.Equal(t, ToolDone, s.Tools["t1"].Status)
	require.Equal(t, "ok", s.Tools["t1"].Output)
	require.Equal(t, "Read", s.Tools["t1"].Name, "Name preserved across ToolData")
	s = Apply(s, AssistantMessageUpdate{Message: Message{
		ID: "a2", State: StateStreaming,
		Content: []ContentBlock{{Type: BlockText, Text: "done"}},
	}})
	require.Len(t, s.Messages, 3)
	require.Equal(t, "a2", s.Messages[2].ID)
}

func TestProjectOrder(t *testing.T) {
	s := Snapshot{
		Messages: []Message{
			{ID: "u1", Role: RoleUser, Text: "hi"},
			{
				ID: "a1", Role: RoleAssistant, State: StateComplete,
				Content: []ContentBlock{
					{Type: BlockThinking, Text: "plan"},
					{Type: BlockText, Text: "hello"},
					{Type: BlockToolUse, ID: "t1", Name: "Bash", Input: "ls"},
				},
			},
		},
		Tools: map[string]ToolRun{
			"t1": {ToolUseID: "t1", Status: ToolDone, Output: "a\n", Detail: "ls"},
		},
	}
	items := Project(s)
	require.Len(t, items, 4)
	require.Equal(t, ItemUser, items[0].Kind)
	require.Equal(t, ItemThinking, items[1].Kind)
	require.Equal(t, ItemAssistant, items[2].Kind)
	require.Equal(t, ItemTool, items[3].Kind)
	require.Equal(t, ToolDone, items[3].ToolRun.Status)
	require.Equal(t, "a\n", items[3].ToolRun.Output)
	require.Equal(t, "Bash", items[3].ToolRun.Name)
}

func TestCompactionEvents(t *testing.T) {
	var s Snapshot
	s = Apply(s, UserAppend{Text: "hi"})
	s = Apply(s, CompactionStarted{})
	require.True(t, s.Compacting)
	require.True(t, IsStreaming(s))
	s = Apply(s, CompactionComplete{ID: "c1"})
	require.False(t, s.Compacting)
	require.Len(t, s.Messages, 2)
	require.Equal(t, RoleCompaction, s.Messages[1].Role)
	items := Project(s)
	require.GreaterOrEqual(t, len(items), 2)
	require.Equal(t, ItemCompaction, items[len(items)-1].Kind)

	s = Apply(s, CompactionStarted{})
	s = Apply(s, CompactionComplete{ID: "c2", Failed: true})
	require.False(t, s.Compacting)
	nMarkers := 0
	for _, m := range s.Messages {
		if m.Role == RoleCompaction {
			nMarkers++
		}
	}
	require.Equal(t, 1, nMarkers)
}

// The pre-cut context size rides on the marker so the transcript can show it.
func TestCompactionMarkerCarriesTokensBefore(t *testing.T) {
	var s Snapshot
	s = Apply(s, CompactionComplete{ID: "c1", TokensBefore: 15000})

	require.Len(t, s.Messages, 1)
	require.Equal(t, 15000, s.Messages[0].TokensBefore)

	items := Project(s)
	require.Len(t, items, 1)
	require.Equal(t, ItemCompaction, items[0].Kind)
	require.Equal(t, 15000, items[0].TokensBefore)
}

// The summary rides on the marker too: it is the only record of what the cut
// dropped, so the transcript needs it to show anything at all.
func TestCompactionMarkerCarriesSummary(t *testing.T) {
	var s Snapshot
	s = Apply(s, CompactionComplete{ID: "c1", TokensBefore: 15000, Summary: "## Goal\nship it"})

	require.Len(t, s.Messages, 1)
	require.Equal(t, "## Goal\nship it", s.Messages[0].Summary)

	items := Project(s)
	require.Len(t, items, 1)
	require.Equal(t, "## Goal\nship it", items[0].Summary)
}

func TestLocalBash(t *testing.T) {
	var s Snapshot
	s = Apply(s, LocalBashStart{ID: "b1", Command: "echo hi"})
	require.Len(t, s.Messages, 1)
	require.Equal(t, RoleLocalBash, s.Messages[0].Role)
	require.True(t, s.Tools["b1"].Local)
	require.Equal(t, ToolInProgress, s.Tools["b1"].Status)
	require.False(t, IsStreaming(s))
	require.False(t, HasRunningTools(s))

	items := Project(s)
	require.Len(t, items, 1)
	require.Equal(t, ItemTool, items[0].Kind)
	require.Equal(t, "bash", items[0].ToolRun.Name)

	s = Apply(s, ToolData{Run: ToolRun{
		ToolUseID: "b1",
		Status:    ToolDone,
		Output:    "hi\n",
		ExitCode:  0,
		Local:     true,
	}})
	require.Equal(t, ToolDone, s.Tools["b1"].Status)
	require.True(t, s.Tools["b1"].Local)

	// CancelStreaming must leave local bash alone.
	s = Apply(s, LocalBashStart{ID: "b2", Command: "sleep 9"})
	s = Apply(s, CancelStreaming{})
	require.Equal(t, ToolInProgress, s.Tools["b2"].Status)
}

func TestApplyUserAppendImages(t *testing.T) {
	var s Snapshot
	s = Apply(s, UserAppend{
		Text:   "Images: a.png",
		Images: []llm.Image{{Data: "QUJD", MimeType: "image/png"}},
	})
	require.Len(t, s.Messages, 1)
	require.Len(t, s.Messages[0].Images, 1)
	require.Equal(t, "image/png", s.Messages[0].Images[0].MimeType)
}
