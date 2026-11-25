package overlays

import (
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

const testErrText = "request ended without sending any chunks"

// recordingHooks is the ShellHooks stub: it records resolved error replies
// through the caller's slices, so tests keep reading replies/texts directly.
type recordingHooks struct {
	replies *[]controller.ErrorReply
	texts   *[]string
}

func (*recordingHooks) FocusEditor() {}

func (h *recordingHooks) ResolveErrorReply(r controller.ErrorReply, text string) {
	*h.replies = append(*h.replies, r)
	*h.texts = append(*h.texts, text)
}

// errorOverlays builds overlays whose resolve hook records replies.
func errorOverlays(replies *[]controller.ErrorReply, texts *[]string) *Overlays {
	return NewOverlays(
		components.DefaultTheme(),
		controller.NewActivityHandler(),
		nil,
		&recordingHooks{replies: replies, texts: texts},
	)
}

func beginErrorAsk(o *Overlays) {
	o.beginErrorAsk(controller.OverlayMsg{Kind: controller.OverlayErrorAsk, ErrorText: testErrText})
}

func pressRune(r rune) xui.KeyEvent {
	return xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: r}
}

func TestErrorAskRendersAndResolves(t *testing.T) {
	var replies []controller.ErrorReply
	var texts []string
	o := errorOverlays(&replies, &texts)
	beginErrorAsk(o)
	require.NotNil(t, o.err, "expected error dialog")
	require.True(t, o.Active(), "error dialog must block the composer")

	surf, overlay := o.DrawBottom(components.DrawContext{Max: components.Size{Width: 60, Height: 12}}, 60, 12)
	require.True(t, overlay)
	require.Equal(t, 60, surf.Size.Width)
	require.Equal(t, 12, surf.Size.Height)

	h, overlay := o.PreferredBottomHeight(60, 0)
	require.True(t, overlay)
	require.GreaterOrEqual(t, h, 8)

	o.handleErrorKey(&components.EventContext{}, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: 'r'})
	require.Nil(t, o.err, "expected cleared")
	require.Len(t, replies, 1)
	require.True(t, replies[0].Retry)
	require.False(t, replies[0].Copy)
	require.Equal(t, testErrText, texts[0], "resolve callback gets the error text")
}

func TestErrorAskKeysResolve(t *testing.T) {
	tests := []struct {
		name string
		key  xui.KeyEvent
		want controller.ErrorReply
	}{
		{"r retries", pressRune('r'), controller.ErrorReply{Retry: true}},
		{"y copies", pressRune('y'), controller.ErrorReply{Copy: true}},
		{"n dismisses", pressRune('n'), controller.ErrorReply{}},
		{"escape dismisses", xui.KeyEvent{Press: true, Code: xui.KeyEscape}, controller.ErrorReply{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var replies []controller.ErrorReply
			var texts []string
			o := errorOverlays(&replies, &texts)
			beginErrorAsk(o)
			o.handleErrorKey(&components.EventContext{}, tt.key)
			require.Len(t, replies, 1)
			require.Equal(t, tt.want, replies[0])
		})
	}
}

func TestErrorAskEnterResolvesSelected(t *testing.T) {
	var replies []controller.ErrorReply
	var texts []string
	o := errorOverlays(&replies, &texts)
	beginErrorAsk(o)
	ctx := &components.EventContext{}

	// Down ×2 → Dismiss; Enter resolves empty.
	o.handleErrorKey(ctx, xui.KeyEvent{Press: true, Code: xui.KeyDown})
	o.handleErrorKey(ctx, xui.KeyEvent{Press: true, Code: xui.KeyDown})
	o.handleErrorKey(ctx, xui.KeyEvent{Press: true, Code: xui.KeyEnter})
	require.Len(t, replies, 1)
	require.Equal(t, controller.ErrorReply{}, replies[0])
	require.Nil(t, o.err)
}

func TestErrorDismissClearsOverlay(t *testing.T) {
	var replies []controller.ErrorReply
	var texts []string
	o := errorOverlays(&replies, &texts)
	beginErrorAsk(o)
	o.Apply(controller.OverlayMsg{Kind: controller.OverlayErrorDismiss})
	require.Nil(t, o.err, "overlay should clear without resolving")
	require.Empty(t, replies, "dismiss must not invoke the callback")
}

func TestErrorAskKeepsOriginalMessageForCopy(t *testing.T) {
	var replies []controller.ErrorReply
	var texts []string
	o := errorOverlays(&replies, &texts)
	original := "  error with surrounding whitespace  "
	o.beginErrorAsk(controller.OverlayMsg{ErrorText: original})
	o.handleErrorKey(&components.EventContext{}, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: 'c'})

	require.Len(t, replies, 1)
	require.True(t, replies[0].Copy)
	require.Equal(t, original, texts[0])
}

func TestErrorAskReplacesOtherModals(t *testing.T) {
	var replies []controller.ErrorReply
	var texts []string
	o := errorOverlays(&replies, &texts)
	beginErrorAsk(o)
	o.beginErrorAsk(controller.OverlayMsg{Kind: controller.OverlayErrorAsk, ErrorText: "replacement"})
	require.NotNil(t, o.err)
	require.Equal(t, "replacement", o.err.Message)
	require.Equal(t, []controller.ErrorReply{{}}, replies)
}
