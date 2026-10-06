package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func TestSessionCommands_ShowOpensPicker(t *testing.T) {
	dir := t.TempDir()
	m, err := session.NewSessionManager(dir, session.WithSessionDir(dir), session.WithShouldFlush(true))
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleUser, Content: "hello world"})
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleAssistant, Content: "ok"})
	require.NoError(t, err)

	var got []session.SessionMeta
	var current string
	bus := controller.NewBus(nil)
	s := &SessionCommands{
		Bus:        bus,
		SessionDir: func() string { return dir },
		SessionID:  func() string { return m.ID() },
		OpenPicker: func(items []session.SessionMeta, currentID string) {
			got = items
			current = currentID
		},
	}
	s.Show()
	require.Len(t, got, 1)
	assert.Equal(t, m.ID(), got[0].ID)
	assert.Equal(t, m.ID(), current)
	assert.Equal(t, "hello world", got[0].Preview)
}

func TestSessionCommands_ShowEmptyToasts(t *testing.T) {
	bus := controller.NewBus(nil)
	s := &SessionCommands{
		Bus:        bus,
		SessionDir: func() string { return t.TempDir() },
		OpenPicker: func([]session.SessionMeta, string) {
			require.Fail(t, "picker should not open")
		},
	}
	s.Show()
	assert.Contains(t, drainToast(t, bus), "No sessions")
}

func TestSessionCommands_AcceptBlocksWhenBusy(t *testing.T) {
	bus := controller.NewBus(nil)
	s := &SessionCommands{
		Bus:          bus,
		StreamActive: func() bool { return true },
	}
	s.Accept("abc")
	assert.Contains(t, drainToast(t, bus), "Cannot resume")
}

func TestSessionCommands_NewSessionBlocksWhenBusy(t *testing.T) {
	bus := controller.NewBus(nil)
	s := &SessionCommands{
		Bus:          bus,
		StreamActive: func() bool { return true },
	}
	s.NewSession()
	assert.Contains(t, drainToast(t, bus), "Cannot start a new session")
}
