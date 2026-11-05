package editor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/app"
	"github.com/pulseaiclub/phi/internal/extension/loader"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func TestExtensionRewindReplaysAndRefillsPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PHI_MODEL", "test-model")
	t.Setenv("PHI_API_KEY", "test-key")
	t.Setenv("PHI_BASE_URL", "http://127.0.0.1:9")
	t.Setenv(loader.EnvExtensions, "off")
	cwd := t.TempDir()
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	bus := controller.NewBus(nil)
	ctrl, err := controller.NewController(bus, proj, cwd)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)
	s, err := agent.NewSession(agent.WithCwd(cwd), agent.WithSessionDir(ctrl.SessionDir()), agent.WithPersist(true))
	require.NoError(t, err)
	require.NoError(t, s.AddUser("original prompt"))
	user := s.LastID()
	require.NoError(t, s.Append(llm.Message{Role: llm.RoleAssistant, Content: "answer"}))
	_, err = ctrl.Resume(s.ID())
	require.NoError(t, err)
	e := NewEditor(app.NewApp(nil), bus, ctrl, nil, components.DefaultTheme(), cwd, "test-model", nil, 0, nil)
	e.transcript.LoadReplay(ctrl.ReplaySnapshot())
	e.submitter.SetCommandBusy(func() bool { return true })
	reply := make(chan pxb.HostResult, 1)
	arg, err := json.Marshal(ext.RewindRequest{SessionID: s.ID(), EntryID: user})
	require.NoError(t, err)
	e.Update(
		controller.ExtHostRequestMsg{
			Context: t.Context(),
			Reply:   reply,
			Request: pxb.HostRequest{Method: "session_rewind", Arg: string(arg)},
		},
	)
	assert.True(t, (<-reply).OK, "the extension's own command must not block its rewind")
	assert.Equal(t, "original prompt", e.composer.Chat.Value)
	assert.Empty(t, e.transcript.Snapshot().Messages)
	loaded, err := session.OpenSession(ctrl.SessionFile())
	require.NoError(t, err)
	assert.Empty(t, loaded.UserTurns())
}

func TestExtensionRewindBusyAndExpiredRequests(t *testing.T) {
	e := newTestEditor(t)
	reply := make(chan pxb.HostResult, 1)
	request := pxb.HostRequest{Method: "session_rewind", Arg: "{}"}
	e.footer.Activity().Apply(controller.ActivityWaiting)
	e.Update(controller.ExtHostRequestMsg{Context: t.Context(), Reply: reply, Request: request})
	assert.Contains(t, (<-reply).Error, "reply")
	e.footer.Activity().Apply(controller.ActivityIdle)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	e.Update(controller.ExtHostRequestMsg{Context: ctx, Reply: reply, Request: request})
	assert.Empty(t, reply, "expired requests must never mutate the session")
}
