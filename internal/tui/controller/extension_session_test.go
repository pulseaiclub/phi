package controller

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/session"
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
	assert.Empty(t, s.BuildContext())
	assert.False(t, c.retryReady)
	assert.Empty(t, c.lastPrompt.Text)
}

func TestRewindPreservesCurrentModelAcrossResume(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{"PHI_MODEL", "PHI_API_KEY", "PHI_BASE_URL"} {
		t.Setenv(key, "")
	}
	t.Setenv("PHI_EXTENSIONS", "off")
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".phi"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".phi", "config.yaml"), []byte(`models:
  - name: model-a
    default: true
    api: OpenAI
    api_key: test
    base_url: http://127.0.0.1:9
  - name: model-b
    api: OpenAI
    api_key: test
    base_url: http://127.0.0.1:9
`), 0o600))
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	c, err := NewController(NewBus(nil), proj, cwd)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	s, err := agent.NewSession(
		agent.WithCwd(cwd),
		agent.WithSessionDir(c.SessionDir()),
		agent.WithPersist(true),
		agent.WithModel("model-a"),
	)
	require.NoError(t, err)
	require.NoError(t, s.AddUser("first"))
	target := s.LastID()
	require.NoError(t, s.Append(llm.Message{Role: llm.RoleAssistant, Content: "answer"}))
	_, err = c.Resume(s.ID())
	require.NoError(t, err)
	require.NoError(t, c.SetModel("model-b"))
	_, err = c.RewindSession(ext.RewindRequest{SessionID: s.ID(), EntryID: target})
	require.NoError(t, err)
	saved, err := session.OpenSession(c.SessionFile())
	require.NoError(t, err)
	assert.Equal(t, "model-b", c.ModelName())
	assert.Equal(t, c.ModelName(), saved.ModelName())
	assert.Empty(t, saved.UserTurns())
	list, err := session.ListSessions(c.SessionDir())
	require.NoError(t, err)
	for _, item := range list {
		if item.ID == s.ID() {
			assert.Empty(t, item.Preview)
		}
	}
	_, err = c.Resume(s.ID())
	require.NoError(t, err)
	assert.Equal(t, "model-b", c.ModelName())
}
