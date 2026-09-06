package editor

import (
	"testing"
	"time"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/app"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func newTreeEditor(t *testing.T) (*Editor, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PHI_MODEL", "test-model")
	t.Setenv("PHI_API_KEY", "key")
	t.Setenv("PHI_BASE_URL", "http://127.0.0.1:9")
	t.Setenv("PHI_EXTENSIONS", "off")
	cwd := t.TempDir()
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	bus := controller.NewBus(nil)
	ctrl, err := controller.NewController(bus, proj, cwd)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)
	m, err := session.NewSessionManager(cwd, session.WithSessionDir(ctrl.SessionDir()), session.WithShouldFlush(true))
	require.NoError(t, err)
	u, err := m.Append(llm.Message{Role: llm.RoleUser, Content: "first question"})
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleAssistant, Content: "answer", Usage: llm.Usage{TotalTokens: 31}})
	require.NoError(t, err)
	_, err = ctrl.Resume(m.ID())
	require.NoError(t, err)
	e := NewEditor(
		app.NewApp(nil),
		bus,
		ctrl,
		nil,
		components.DefaultTheme(),
		cwd,
		"test-model",
		"",
		10000,
		[]string{"test-model"},
	)
	e.transcript.LoadReplay(ctrl.ReplaySnapshot())
	e.transcript.Sync()
	return e, u
}

func TestTreeInlineNavigationPreservesDraftAndSessionIdentity(t *testing.T) {
	for _, draft := range []string{"", "unfinished draft"} {
		t.Run(draft, func(t *testing.T) {
			e, u := newTreeEditor(t)
			identity := e.ctrl.SessionID()
			e.composer.SetInput(draft)
			e.Update(controller.TreeOpenMsg{})
			require.True(t, e.composer.Tree.Open)
			surface := e.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 30}})
			require.GreaterOrEqual(t, len(surface.Children), 3)
			assert.Same(t, &e.composer.Tree, surface.Children[1].Surface.Widget)
			for _, row := range e.composer.Tree.Rows() {
				if row.Item.ID == u {
					e.composer.Tree.OnAccept(row.Item)
				}
			}
			assert.Equal(t, "summary", e.composer.Tree.Mode)
			e.composer.Tree.OnChoice(false, "")
			require.Eventually(
				t,
				func() bool { e.drainBus(); return !e.ctrl.TreeBusy() },
				3*time.Second,
				time.Millisecond,
			)
			assert.Equal(t, identity, e.ctrl.SessionID())
			assert.Empty(t, e.ctrl.TreeSnapshot().LeafID)
			assert.Empty(t, e.transcript.Snapshot().Messages)
			assert.False(t, e.composer.Tree.Open)
			if draft == "" {
				assert.Equal(t, "first question", e.composer.Chat.Value)
			} else {
				assert.Equal(t, draft, e.composer.Chat.Value)
			}
		})
	}
}

func TestTreeSlashDispatchAndEscapeKeepInputUsable(t *testing.T) {
	e, _ := newTreeEditor(t)
	e.composer.SetInput("/tree")
	e.Update(controller.SubmitMsg{Text: "/tree"})
	e.drainBus()
	require.True(t, e.composer.Tree.Open)
	assert.Empty(t, e.composer.Chat.Value)
	e.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyRune, Rune: 'x', Press: true})
	assert.Equal(t, "x", e.composer.Tree.Query)
	assert.Empty(t, e.composer.Chat.Value)
	e.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyEscape, Press: true})
	assert.False(t, e.composer.Tree.Open)
	e.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyRune, Rune: 'z', Press: true})
	assert.Equal(t, "z", e.composer.Chat.Value)
}
