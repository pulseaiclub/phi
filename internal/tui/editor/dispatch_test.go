package editor

import (
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/tui/composer"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// The modal overlay owns every key while it shows: the composer must not
// receive the rune the user aimed at the permission prompt.
func TestOverlayKeysStopAtEditor(t *testing.T) {
	e := newTestEditor(t)
	e.overlays.Apply(controller.OverlayMsg{
		Kind: controller.OverlayPermissionAsk,
		Request: permission.Request{
			Kind:    permission.KindExec,
			Command: "rm -rf /tmp/x",
		},
		PermReply: make(chan controller.AskReply, 1),
	})
	require.True(t, e.overlays.PermissionActive())

	e.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyRune, Rune: 'j', Press: true})
	assert.Empty(t, e.composer.Chat.Value, "overlay keys must not leak into the composer")

	ctx := &components.EventContext{}
	e.Handle(ctx, xui.KeyEvent{Code: xui.KeyEscape, Press: true})
	assert.False(t, e.overlays.Active(), "Esc resolves the overlay at the Editor level")
}

// PageUp/PageDown scroll the transcript even while the composer holds focus.
func TestPageKeysReachTranscript(t *testing.T) {
	e := newTestEditor(t)
	ctx := &components.EventContext{}
	e.Handle(ctx, xui.KeyEvent{Code: xui.KeyPageUp, Press: true})
	assert.True(t, ctx.Consume, "the transcript claims page keys before the composer")
}

// Ctrl+C quits at the root, not inside the composer.
func TestCtrlCQuitsAtEditor(t *testing.T) {
	e := newTestEditor(t)
	ctx := &components.EventContext{}
	e.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'c', Mods: xui.ModCtrl, Press: true})
	assert.True(t, ctx.Quit)
}

// A list overlay owns mouse input before the transcript can interpret it as a
// click or selection gesture.
func TestListOverlayConsumesMouseInput(t *testing.T) {
	e := newTestEditor(t)
	e.composer.Wire(composer.WireHooks{
		Submitter:    e.submitter,
		Commands:     e.commands,
		Bus:          e.bus,
		RequestFocus: e.App.RequestFocus,
	})
	e.composer.ShowList(nil, listpicker.ShowConfig{}, nil, nil)

	ctx := &components.EventContext{}
	e.Handle(ctx, xui.MouseEvent{Action: xui.MousePress, Button: xui.MouseLeft})
	assert.True(t, ctx.Consume, "open list overlays must consume mouse input")
}
