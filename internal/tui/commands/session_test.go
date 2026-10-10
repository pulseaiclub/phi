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

func TestSessionCommands_DeleteRemovesAndRefreshes(t *testing.T) {
	dir := t.TempDir()
	// Managers only persist once an assistant message exists (hasAssistantMsg
	// gates flushing), so each fixture needs both roles to hit the disk.
	current, err := session.NewSessionManager(dir, session.WithSessionDir(dir), session.WithShouldFlush(true))
	require.NoError(t, err)
	_, err = current.Append(llm.Message{Role: llm.RoleUser, Content: "keep me"})
	require.NoError(t, err)
	_, err = current.Append(llm.Message{Role: llm.RoleAssistant, Content: "ok"})
	require.NoError(t, err)
	other, err := session.NewSessionManager(dir, session.WithSessionDir(dir), session.WithShouldFlush(true))
	require.NoError(t, err)
	_, err = other.Append(llm.Message{Role: llm.RoleUser, Content: "delete me"})
	require.NoError(t, err)
	_, err = other.Append(llm.Message{Role: llm.RoleAssistant, Content: "ok"})
	require.NoError(t, err)

	bus := controller.NewBus(nil)
	var refreshed []session.SessionMeta
	var refreshCurrent string
	s := &SessionCommands{
		Bus:        bus,
		SessionDir: func() string { return dir },
		SessionID:  func() string { return current.ID() },
		RefreshPicker: func(items []session.SessionMeta, currentID string) {
			refreshed = items
			refreshCurrent = currentID
		},
	}
	s.Delete(other.ID())

	assert.Contains(t, drainToast(t, bus), "Deleted")
	require.Len(t, refreshed, 1)
	assert.Equal(t, current.ID(), refreshed[0].ID, "deleted session is gone from the refreshed list")
	assert.Equal(t, current.ID(), refreshCurrent)
	list, err := session.ListSessions(dir)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, current.ID(), list[0].ID)
}

func TestSessionCommands_DeleteBlocksWhenBusy(t *testing.T) {
	bus := controller.NewBus(nil)
	s := &SessionCommands{
		Bus:          bus,
		SessionDir:   func() string { return t.TempDir() },
		StreamActive: func() bool { return true },
	}
	s.Delete("abc")
	assert.Contains(t, drainToast(t, bus), "Cannot delete")
}

func TestSessionCommands_DeleteUnknownIdToastsAndKeepsList(t *testing.T) {
	dir := t.TempDir()
	m, err := session.NewSessionManager(dir, session.WithSessionDir(dir), session.WithShouldFlush(true))
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleUser, Content: "hi"})
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleAssistant, Content: "ok"})
	require.NoError(t, err)

	bus := controller.NewBus(nil)
	var refreshed bool
	s := &SessionCommands{
		Bus:           bus,
		SessionDir:    func() string { return dir },
		SessionID:     func() string { return m.ID() },
		RefreshPicker: func([]session.SessionMeta, string) { refreshed = true },
	}
	s.Delete("nosuchsession")
	assert.Contains(t, drainToast(t, bus), "not found")
	assert.False(t, refreshed, "failed delete must not touch the picker")
}
