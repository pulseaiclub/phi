package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/llm"
)

func TestControllerRewindValidatesBeforeMoving(t *testing.T) {
	s, err := agent.NewSession(agent.WithCwd(t.TempDir()))
	require.NoError(t, err)
	require.NoError(t, s.AddUser("prompt"))
	user := s.LastID()
	require.NoError(t, s.Append(llm.Message{Role: llm.RoleAssistant, Content: "reply"}))
	e, err := agent.NewEngine(llm.ModelConfig{Name: "test"}, s)
	require.NoError(t, err)
	c := &EngineController{engine: e, retryReady: true, lastPrompt: LastPrompt{Text: "stale"}}
	turns := c.SessionTurns()
	require.Len(t, turns, 1)
	assert.Equal(t, user, turns[0].EntryID)
	req := ext.RewindRequest{SessionID: s.ID(), EntryID: user, ValidateOnly: true}
	_, err = c.RewindSession(req)
	require.NoError(t, err)
	require.Len(t, s.BuildContext(), 2)
	req.SessionID = "different"
	_, err = c.RewindSession(req)
	require.ErrorContains(t, err, "session changed")
	req.SessionID, req.EntryID = s.ID(), "missing"
	_, err = c.RewindSession(req)
	require.ErrorContains(t, err, "current branch")
	req.EntryID, req.ValidateOnly = user, false
	text, err := c.RewindSession(req)
	require.NoError(t, err)
	assert.Equal(t, "prompt", text)
	assert.False(t, c.retryReady)
	assert.Empty(t, c.lastPrompt.Text)
}
