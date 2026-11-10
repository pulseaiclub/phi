package editor

import (
	"context"
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

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

func TestExtensionRequestRejectsInvalidAndExpiredRequests(t *testing.T) {
	e := diffEditor(t, t.TempDir())
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
	e := diffEditor(t, t.TempDir())

	reply := openExtensionSelect(t, e)
	diffKey(e, xui.KeyDown, 0)
	diffKey(e, xui.KeyEnter, 0)
	assert.Equal(t, pxb.HostResult{Body: "1", OK: true}, <-reply)
	e.composer.HideList()
	assert.Empty(t, reply, "closing an accepted picker again must not send another reply")

	reply = openExtensionSelect(t, e)
	diffKey(e, xui.KeyEscape, 0)
	assert.False(t, (<-reply).OK)
}

func TestExtensionSelectTimeoutOnlyDismissesOwnPicker(t *testing.T) {
	e := diffEditor(t, t.TempDir())
	old := openExtensionSelect(t, e)
	fresh := openExtensionSelect(t, e)
	assert.False(t, (<-old).OK, "replacement cancels the old request")

	e.Update(controller.ExtHostRequestMsg{Reply: old, Dismiss: true})
	assert.Empty(t, fresh, "stale timeout must not answer the new request")
	e.Update(controller.ExtHostRequestMsg{Reply: fresh, Dismiss: true})
	assert.False(t, (<-fresh).OK)
	assert.Empty(t, fresh)
}

func TestExtensionSelectCancelledByModal(t *testing.T) {
	for _, kind := range []controller.OverlayKind{
		controller.OverlayPermissionAsk, controller.OverlayExtConfirm, controller.OverlayErrorAsk,
	} {
		e := diffEditor(t, t.TempDir())
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
