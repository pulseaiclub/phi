package app

import (
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chat"
	"github.com/pulseaiclub/phi/internal/components/palette"
)

type stubWidget struct {
	got int
}

func (s *stubWidget) Handle(ctx *components.EventContext, ev xui.Event) {
	if _, ok := ev.(xui.KeyEvent); ok {
		s.got++
		ctx.Consume = true
	}
}

func (s *stubWidget) Draw(components.DrawContext) components.Surface {
	return components.NewSurface(1, 1, s)
}

func TestDispatchSkipsFocusMissingFromSurface(t *testing.T) {
	root := &stubWidget{}
	chat := &stubWidget{}
	a := &App{
		root:     root,
		focused:  chat,
		lastSurf: components.NewSurface(8, 4, root),
	}
	a.dispatch(&components.EventContext{}, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: 'j'})
	assert.Equal(t, 0, chat.got)
	assert.Equal(t, 1, root.got)
	assert.Equal(t, root, a.focused)
}

func TestDispatchKeepsFocusWhenPainted(t *testing.T) {
	root := &stubWidget{}
	chat := &stubWidget{}
	surf := components.NewSurface(8, 4, root)
	surf.Children = []components.SubSurface{{Surface: components.NewSurface(8, 2, chat)}}
	a := &App{root: root, focused: chat, lastSurf: surf}
	a.dispatch(&components.EventContext{}, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: 'j'})
	assert.Equal(t, 1, chat.got)
	assert.Equal(t, 0, root.got)
}

// While a modal owns the keyboard, a press on the draft below it must not
// move focus — typing and Enter would otherwise land in the draft with the
// palette still open.
func TestMousePressKeepsFocusBehindAModal(t *testing.T) {
	root := &stubWidget{}
	draft := &chat.ChatInput{}
	// The modal holds focus through its own widget, painted into the tree —
	// like the palette, whose panel surface carries the focused picker.
	modal := &palette.CommandPalette{}
	build := func() *App {
		surf := components.NewSurface(8, 4, root)
		surf.Children = []components.SubSurface{
			{Surface: components.NewSurface(8, 2, modal), Z: 10},
			{Origin: components.Point{X: 0, Y: 2}, Surface: components.NewSurface(8, 2, draft), Z: 1},
		}
		return &App{root: root, focused: modal, lastSurf: surf}
	}
	pressDraft := func(a *App) {
		a.handleEvent(xui.MouseEvent{X: 1, Y: 3, Action: xui.MousePress, Button: xui.MouseLeft})
	}

	a := build()
	a.ModalInput = func() bool { return true }
	pressDraft(a)
	assert.Equal(t, modal, a.focused, "the draft cannot take keys from the modal")

	a = build()
	pressDraft(a)
	assert.Equal(t, draft, a.focused, "without a modal the draft takes focus as usual")
}
