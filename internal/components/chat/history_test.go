package chat

import (
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
)

func keyUp() xui.KeyEvent   { return xui.KeyEvent{Code: xui.KeyUp, Press: true} }
func keyDown() xui.KeyEvent { return xui.KeyEvent{Code: xui.KeyDown, Press: true} }

func TestHistoryUpRecallsNewestFirst(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3, History: []string{"one", "two"}}
	ctx := &components.EventContext{}
	c.Handle(ctx, keyUp())
	require.Equal(t, "two", c.Value)
	require.Equal(t, 3, c.Cursor)
	c.Handle(ctx, keyUp())
	require.Equal(t, "one", c.Value)
	// Top of the ring stays put instead of rewinding the caret.
	c.Handle(ctx, keyUp())
	require.Equal(t, "one", c.Value)
	require.True(t, ctx.Consume)
}

func TestHistoryDownRestoresDraft(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3, Value: "draft", Cursor: 5, History: []string{"one", "two"}}
	ctx := &components.EventContext{}
	c.Handle(ctx, keyUp())
	require.Equal(t, "two", c.Value)
	c.Handle(ctx, keyDown())
	require.Equal(t, "draft", c.Value)
	require.Equal(t, 5, c.Cursor)
	// Off the end of the ring, Down is a plain cursor move again.
	c.Handle(ctx, keyDown())
	require.Equal(t, "draft", c.Value)
}

func TestHistoryUpMidDraftMovesCaret(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3, Value: "first\nsecond", Cursor: len("first\nsecond"), History: []string{"old"}}
	ctx := &components.EventContext{}
	c.Handle(ctx, keyUp())
	require.Equal(t, "first\nsecond", c.Value, "Up on the last logical line must move the caret, not recall")
	require.Less(t, c.Cursor, len("first\nsecond"))
	// Once on the first line, Up recalls.
	c.Cursor = 0
	c.Handle(ctx, keyUp())
	require.Equal(t, "old", c.Value)
}

func TestHistoryUpAtFirstLineOfMultiLineEntry(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3, History: []string{"a\nb"}}
	ctx := &components.EventContext{}
	c.Handle(ctx, keyUp())
	require.Equal(t, "a\nb", c.Value)
	require.Equal(t, 3, c.Cursor)
	// Caret now sits on the last line, so Down is a caret move until it gets
	// back to the last line's end — where it returns to the (empty) draft.
	c.Handle(ctx, keyUp())
	require.Equal(t, "a\nb", c.Value, "Up on the last line moves the caret")
}

func TestHistorySkipsPickerNavigation(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3, History: []string{"one"}}
	c.MentionOpen = true
	ctx := &components.EventContext{}
	c.Handle(ctx, keyUp())
	require.Empty(t, c.Value)
	require.False(t, ctx.Consume)
}

func TestHistoryTypingLeavesBrowse(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3, History: []string{"one"}}
	ctx := &components.EventContext{}
	c.Handle(ctx, keyUp())
	require.Equal(t, "one", c.Value)
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'x', Press: true})
	require.Equal(t, "onex", c.Value)
	c.Handle(ctx, keyUp())
	require.Equal(t, "one", c.Value, "a typed-over entry became the draft")
}

func TestHistoryCtrlPAndN(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3, Value: "a\nz", Cursor: 3, History: []string{"one"}}
	ctx := &components.EventContext{}
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'p', Mods: xui.ModCtrl, Press: true})
	require.Equal(t, "one", c.Value, "Ctrl+P recalls regardless of caret line")
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'n', Mods: xui.ModCtrl, Press: true})
	require.Equal(t, "a\nz", c.Value)
}

func TestHistoryEmptyRingIgnoresKeys(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3, Value: "keep", Cursor: 4}
	ctx := &components.EventContext{}
	c.Handle(ctx, keyUp())
	c.Handle(ctx, keyDown())
	require.Equal(t, "keep", c.Value)
	require.Equal(t, 4, c.Cursor)
}

func TestHistoryResetBrowseAfterExternalSet(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3, History: []string{"one"}}
	ctx := &components.EventContext{}
	c.Handle(ctx, keyUp())
	require.Equal(t, "one", c.Value)
	c.Value, c.Cursor = "", 0
	c.ResetHistoryBrowse()
	c.Handle(ctx, keyUp())
	require.Equal(t, "one", c.Value)
}
