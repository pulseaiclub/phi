package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/core"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func TestSessionCommands_ShowOpensPicker(t *testing.T) {
	dir := t.TempDir()
	m, err := session.NewSessionManager(dir, session.WithSessionDir(dir), session.WithShouldFlush(true))
	require.NoError(t, err)
	_, err = m.Append(core.Message{Role: core.RoleUser, Content: "hello world"})
	require.NoError(t, err)
	_, err = m.Append(core.Message{Role: core.RoleAssistant, Content: "ok"})
	require.NoError(t, err)

	bus := controller.NewBus(nil)
	comp := &stubBuiltinComposer{}
	s := &SessionCommands{
		Bus:        bus,
		SessionDir: func() string { return dir },
		SessionID:  func() string { return m.ID() },
		composer:   comp,
	}
	s.Show()
	require.Len(t, comp.sessionItems, 1)
	assert.Equal(t, m.ID(), comp.sessionItems[0].ID)
	require.NotNil(t, comp.sessionAccept, "accept must reach the command handlers")
	assert.Equal(t, "hello world", comp.sessionItems[0].Preview)
}

func TestSessionCommands_ShowEmptyToasts(t *testing.T) {
	bus := controller.NewBus(nil)
	comp := &stubBuiltinComposer{}
	s := &SessionCommands{
		Bus:        bus,
		SessionDir: func() string { return t.TempDir() },
		composer:   comp,
	}
	s.Show()
	assert.Contains(t, drainToast(t, bus), "No sessions")
	assert.Nil(t, comp.sessionAccept, "empty listing must not arm an accept path")
}

func TestSessionCommands_AcceptBlocksWhenBusy(t *testing.T) {
	bus := controller.NewBus(nil)
	s := &SessionCommands{Bus: bus, ui: &stubHost{stream: true}}
	s.Accept("abc")
	assert.Contains(t, drainToast(t, bus), "Cannot resume")
}

func TestSessionCommands_NewSessionBlocksWhenBusy(t *testing.T) {
	bus := controller.NewBus(nil)
	s := &SessionCommands{Bus: bus, ui: &stubHost{stream: true}}
	s.NewSession()
	assert.Contains(t, drainToast(t, bus), "Cannot start a new session")
}

func TestSessionCommands_ForkBlocksWhenBusy(t *testing.T) {
	bus := controller.NewBus(nil)
	s := &SessionCommands{Bus: bus, ui: &stubHost{stream: true}}
	s.Fork(nil)
	assert.Contains(t, drainToast(t, bus), "Cannot fork while a reply or command is running")
}

func TestSessionCommands_ForkRejectsArguments(t *testing.T) {
	bus := controller.NewBus(nil)
	s := &SessionCommands{Bus: bus, ui: &stubHost{}}
	s.Fork([]string{"later"})
	assert.Contains(t, drainToast(t, bus), "Usage: /fork")

	reg := NewCommandRegistry()
	s.Register(reg)
	assert.True(t, reg.DispatchSlash("/fork now", NewContext(bus, nil)))
	assert.Contains(t, drainToast(t, bus), "Usage: /fork", "arguments must not fall through to a prompt")
}

func TestSessionCommands_DeleteRemovesAndRefreshes(t *testing.T) {
	dir := t.TempDir()
	// Managers only persist once an assistant message exists (hasAssistantMsg
	// gates flushing), so each fixture needs both roles to hit the disk.
	current, err := session.NewSessionManager(dir, session.WithSessionDir(dir), session.WithShouldFlush(true))
	require.NoError(t, err)
	_, err = current.Append(core.Message{Role: core.RoleUser, Content: "keep me"})
	require.NoError(t, err)
	_, err = current.Append(core.Message{Role: core.RoleAssistant, Content: "ok"})
	require.NoError(t, err)
	other, err := session.NewSessionManager(dir, session.WithSessionDir(dir), session.WithShouldFlush(true))
	require.NoError(t, err)
	_, err = other.Append(core.Message{Role: core.RoleUser, Content: "delete me"})
	require.NoError(t, err)
	_, err = other.Append(core.Message{Role: core.RoleAssistant, Content: "ok"})
	require.NoError(t, err)

	bus := controller.NewBus(nil)
	comp := &stubBuiltinComposer{}
	s := &SessionCommands{
		Bus:        bus,
		SessionDir: func() string { return dir },
		SessionID:  func() string { return current.ID() },
		composer:   comp,
	}
	s.Delete(other.ID())

	assert.Contains(t, drainToast(t, bus), "Deleted")
	require.Len(t, comp.refreshedSessions, 1)
	assert.Equal(t, current.ID(), comp.refreshedSessions[0].ID, "deleted session is gone from the refreshed list")
	list, err := session.ListSessions(dir)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, current.ID(), list[0].ID)
}

func TestSessionCommands_DeleteBlocksWhenBusy(t *testing.T) {
	bus := controller.NewBus(nil)
	s := &SessionCommands{
		Bus:        bus,
		SessionDir: func() string { return t.TempDir() },
		ui:         &stubHost{stream: true},
	}
	s.Delete("abc")
	assert.Contains(t, drainToast(t, bus), "Cannot delete")
}

func TestSessionCommands_DeleteUnknownIdToastsAndKeepsList(t *testing.T) {
	dir := t.TempDir()
	m, err := session.NewSessionManager(dir, session.WithSessionDir(dir), session.WithShouldFlush(true))
	require.NoError(t, err)
	_, err = m.Append(core.Message{Role: core.RoleUser, Content: "hi"})
	require.NoError(t, err)
	_, err = m.Append(core.Message{Role: core.RoleAssistant, Content: "ok"})
	require.NoError(t, err)

	bus := controller.NewBus(nil)
	comp := &stubBuiltinComposer{}
	s := &SessionCommands{
		Bus:        bus,
		SessionDir: func() string { return dir },
		SessionID:  func() string { return m.ID() },
		composer:   comp,
	}
	s.Delete("nosuchsession")
	assert.Contains(t, drainToast(t, bus), "not found")
	assert.Nil(t, comp.refreshedSessions, "failed delete must not touch the picker")
}

func TestSessionCommands_CompactRejectsArguments(t *testing.T) {
	bus := controller.NewBus(nil)
	s := &SessionCommands{Bus: bus, ui: &stubHost{}}
	s.Compact([]string{"now"})
	assert.Contains(t, drainToast(t, bus), "Usage: /compact")
}

func TestSessionCommands_CompactBlocksWhenBusy(t *testing.T) {
	bus := controller.NewBus(nil)
	s := &SessionCommands{Bus: bus, ui: &stubHost{stream: true}}
	s.Compact(nil)
	assert.Contains(t, drainToast(t, bus), "Cannot compact")
}
