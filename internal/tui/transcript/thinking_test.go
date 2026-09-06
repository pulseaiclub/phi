package transcript

import (
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/block"
	"github.com/pulseaiclub/phi/internal/session"
)

func TestThinkingPreferenceAppliesImmediatelyAndPreservesManualToggles(t *testing.T) {
	p := NewTranscriptPane(components.DefaultTheme(), nil, "test")
	snap := session.Snapshot{Messages: []session.Message{{
		ID: "a1", Role: session.RoleAssistant, State: session.StateStreaming,
		Content: []session.ContentBlock{{Type: session.BlockThinking, Text: "first thought"}},
	}}}
	p.LoadReplay(snap)
	p.Sync()
	require.Len(t, p.list.Entries, 1)
	thinking := p.list.Entries[0].(*block.ThinkingBlock)
	assert.False(t, thinking.Expanded)
	p.SetThinkingExpanded(true)
	assert.True(t, thinking.Expanded)
	thinking.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	assert.False(t, thinking.Expanded)
	snap.Messages[0].Content[0].Text = "more thoughts"
	p.snap = snap
	p.Sync()
	assert.False(t, thinking.Expanded, "stream updates must preserve a manual collapse")
	// Rebuilding the same branch preserves the override; a new block uses the saved default.
	p.LoadReplay(snap)
	p.Sync()
	assert.False(t, p.list.Entries[0].(*block.ThinkingBlock).Expanded)
	snap.Messages[0].State = session.StateComplete
	snap.Messages = append(snap.Messages, session.Message{
		ID: "a2", Role: session.RoleAssistant, State: session.StateStreaming,
		Content: []session.ContentBlock{{Type: session.BlockThinking, Text: "new thought"}},
	})
	p.LoadReplay(snap)
	p.Sync()
	require.Len(t, p.list.Entries, 2)
	assert.True(t, p.list.Entries[1].(*block.ThinkingBlock).Expanded)
	p.SetThinkingExpanded(false)
	for _, entry := range p.list.Entries {
		assert.False(t, entry.(*block.ThinkingBlock).Expanded)
	}
	p.LoadReplay(snap)
	p.Sync()
	for _, entry := range p.list.Entries {
		assert.False(t, entry.(*block.ThinkingBlock).Expanded)
	}
}
