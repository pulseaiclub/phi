package editor

import (
	"context"
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/tui/codepane"
	"github.com/pulseaiclub/phi/internal/tui/composer"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/diffpane"
	"github.com/pulseaiclub/phi/internal/tui/overlays"
)

// newExtensionEditor wires only what extension requests touch: a composer with
// no redraw callback and inactive overlays.
func newExtensionEditor(t *testing.T) *Editor {
	t.Helper()
	return &Editor{
		composer: composer.NewComposerPane(components.DefaultTheme(), "", t.TempDir()),
		overlays: &overlays.Overlays{},
		code:     &codepane.Pane{},
		diff:     &diffpane.Pane{},
	}
}

func openExtensionSelect(t *testing.T, e *Editor) chan pxb.HostResult {
	t.Helper()
	reply := make(chan pxb.HostResult, 1)
	e.Update(controller.ExtHostRequestMsg{
		Context: t.Context(),
		Request: pxb.HostRequest{
			Method: "select",
			Arg:    `{"title":"pick","items":[{"label":"first"},{"label":"second"}]}`,
		},
		Reply: reply,
	})
	return reply
}

func extensionKey(e *Editor, code xui.KeyCode) {
	e.composer.Handle(&components.EventContext{}, xui.KeyEvent{Press: true, Code: code})
}

func TestExtensionRequestRejectsInvalidAndExpiredRequests(t *testing.T) {
	e := newExtensionEditor(t)
	for _, arg := range []string{"{", `{"items":[]}`} {
		reply := make(chan pxb.HostResult, 1)
		e.Update(controller.ExtHostRequestMsg{
			Context: t.Context(), Reply: reply,
			Request: pxb.HostRequest{Method: "select", Arg: arg},
		})
		assert.NotEmpty(t, (<-reply).Error)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	reply := make(chan pxb.HostResult, 1)
	e.Update(controller.ExtHostRequestMsg{
		Context: ctx, Reply: reply,
		Request: pxb.HostRequest{Method: "select", Arg: `{"items":[{"label":"one"}]}`},
	})
	assert.Empty(t, reply)
}

func TestExtensionSelectRepliesWithIndexOrCancellation(t *testing.T) {
	e := newExtensionEditor(t)

	reply := openExtensionSelect(t, e)
	extensionKey(e, xui.KeyDown)
	extensionKey(e, xui.KeyEnter)
	assert.Equal(t, pxb.HostResult{Body: "1", OK: true}, <-reply)
	assert.Empty(t, reply, "accept must send exactly one reply")

	reply = openExtensionSelect(t, e)
	extensionKey(e, xui.KeyEscape)
	assert.False(t, (<-reply).OK)
}

func TestExtensionSelectStaleTimeoutLeavesNewPickerOpen(t *testing.T) {
	e := newExtensionEditor(t)
	old := openExtensionSelect(t, e)
	fresh := openExtensionSelect(t, e)
	assert.False(t, (<-old).OK, "replacement cancels the old request")

	e.Update(controller.ExtHostRequestMsg{Reply: old, Dismiss: true})
	assert.Empty(t, fresh, "stale timeout must not answer the new request")
	extensionKey(e, xui.KeyEscape)
	assert.False(t, (<-fresh).OK)
}

func TestExtensionSelectTimeoutDismissesOwnPicker(t *testing.T) {
	e := newExtensionEditor(t)
	reply := openExtensionSelect(t, e)
	e.Update(controller.ExtHostRequestMsg{Reply: reply, Dismiss: true})
	assert.False(t, (<-reply).OK)
	assert.Empty(t, reply)
}

func TestExtensionSelectCancelledByModal(t *testing.T) {
	for _, kind := range []controller.OverlayKind{
		controller.OverlayPermissionAsk, controller.OverlayExtConfirm, controller.OverlayErrorAsk,
	} {
		e := newExtensionEditor(t)
		e.overlays = overlays.NewOverlays(components.DefaultTheme(), controller.NewActivityHandler(),
			e.composer, nil, nil, nil)
		reply := openExtensionSelect(t, e)
		e.Update(controller.OverlayMsg{Kind: kind, ErrorText: "test error"})
		require.NotEmpty(t, reply)
		assert.False(t, (<-reply).OK)
		_, open := e.composer.ListOverlay(components.DrawContext{})
		assert.False(t, open)
		assert.True(t, e.overlays.Active())
	}
}

func TestExtensionSelectRejectedByViewer(t *testing.T) {
	for _, viewer := range []string{"code", "diff"} {
		t.Run(viewer, func(t *testing.T) {
			e := newTestEditor(t)
			if viewer == "code" {
				e.code.Open("a.go")
			} else {
				e.diff.OpenText("diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-old\n+new\n", nil)
			}
			reply := openExtensionSelect(t, e)
			require.NotEmpty(t, reply)
			assert.Contains(t, (<-reply).Error, "viewer")
			_, open := e.composer.ListOverlay(components.DrawContext{})
			assert.False(t, open)
		})
	}
}
