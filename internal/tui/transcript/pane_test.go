package transcript

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/block"
	"github.com/pulseaiclub/phi/internal/components/status"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// longMsgLines is tall enough to need more than one of the paneTestRows-tall
// pages the transcript draws.
const longMsgLines = 30

func TestTranscriptPane_ApplySessionAndSync(t *testing.T) {
	th := components.DefaultTheme()
	spin := status.NewSpinner(th.ToolName)
	pane := NewTranscriptPane(th, spin, "Phi test")

	pane.ApplySession(session.UserAppend{Text: "hello"})
	pane.Sync()

	require.False(t, pane.IsEmpty(), "expected transcript entries after user append")
	require.Len(t, pane.Snapshot().Messages, 1)
}

func TestTranscriptPane_IsStreaming(t *testing.T) {
	th := components.DefaultTheme()
	spin := status.NewSpinner(th.ToolName)
	pane := NewTranscriptPane(th, spin, "Phi test")

	require.False(t, pane.IsStreaming(), "empty pane should not stream")

	pane.ApplySession(session.AssistantMessageUpdate{Message: session.Message{
		ID:    "a1",
		State: session.StateStreaming,
	}})
	require.True(t, pane.IsStreaming(), "expected streaming after assistant StateStreaming")

	pane.ApplySession(session.AssistantMessageUpdate{Message: session.Message{
		ID:    "a1",
		State: session.StateComplete,
	}})
	require.False(t, pane.IsStreaming(), "expected idle after StreamEnd")
}

func TestTranscriptPane_LoadReplayClearsWidgets(t *testing.T) {
	th := components.DefaultTheme()
	spin := status.NewSpinner(th.ToolName)
	pane := NewTranscriptPane(th, spin, "Phi test")

	pane.ApplySession(session.UserAppend{Text: "x"})
	pane.Sync()
	require.False(t, pane.IsEmpty(), "setup: expected entries")

	pane.LoadReplay(session.Snapshot{})
	pane.Sync()
	require.True(t, pane.IsEmpty(), "LoadReplay should clear visible entries until snap has items")
}

const paneTestRows = 12

// bashPane draws one user turn plus a collapsed bash run and returns the drawn
// pane. Rows are bottom-anchored, so the bash title is the last viewport row.
func bashPane(t *testing.T) (*TranscriptPane, *controller.Bus, *block.BashBlock) {
	t.Helper()
	th := components.DefaultTheme()
	pane := NewTranscriptPane(th, status.NewSpinner(th.ToolName), "Phi test")
	bus := controller.NewBus(nil)
	pane.SetCopyHandlers(bus, func(string) bool { return true })
	pane.LoadReplay(session.Snapshot{
		Messages: []session.Message{
			{ID: "u1", Role: session.RoleUser, State: session.StateComplete, Text: "hello"},
			{
				ID: "a1", Role: session.RoleAssistant, State: session.StateComplete,
				Content: []session.ContentBlock{{Type: session.BlockToolUse, ID: "t1", Name: "bash"}},
			},
		},
		Tools: map[string]session.ToolRun{
			"t1": {ToolUseID: "t1", Name: "bash", Status: session.ToolDone, Detail: "ls", Output: "a.go"},
		},
	})
	pane.Sync()
	pane.Draw(
		components.DrawContext{Max: components.Size{Width: 60, Height: paneTestRows}},
		60, paneTestRows,
	)
	require.NotEmpty(t, pane.list.Entries, "setup: expected transcript entries")
	bash, ok := pane.list.Entries[len(pane.list.Entries)-1].(*block.BashBlock)
	require.True(t, ok, "setup: expected the bash run last")
	return pane, bus, bash
}

// Dragging from a collapsed title row must copy text, not expand: the block
// toggles only once the pane has ruled out a drag-selection.
func TestTranscriptPane_DragFromTitleRowCopies(t *testing.T) {
	pane, bus, bash := bashPane(t)
	var copied string
	pane.SetCopyHandlers(bus, func(text string) bool {
		copied = text
		return true
	})
	const row = paneTestRows - 1

	ctx := &components.EventContext{}
	pane.HandleMouse(ctx, xui.MouseEvent{X: 2, Y: row, Action: xui.MousePress, Button: xui.MouseLeft}, nil)
	pane.HandleMouse(ctx, xui.MouseEvent{X: 24, Y: row, Action: xui.MouseDrag, Button: xui.MouseLeft}, nil)
	pane.HandleMouse(ctx, xui.MouseEvent{X: 24, Y: row, Action: xui.MouseRelease, Button: xui.MouseLeft}, nil)

	require.Equal(t, "ls", copied, "drag across the title row copies the text without chrome")
	require.False(t, bash.Expanded, "dragging from the title row must not expand")

	batch := bus.Drain()
	require.Len(t, batch, 1)
	msg, ok := batch[0].(controller.ToastMsg)
	require.True(t, ok, "copy feedback goes through the bus")
	require.Equal(t, "Selection copied to clipboard", msg.Message)
}

// longMessagePane draws one numbered user prompt spanning more than one page and
// captures whatever the pane copies.
func longMessagePane(t *testing.T) (*TranscriptPane, *string) {
	t.Helper()
	rows := make([]string, longMsgLines)
	for i := range rows {
		rows[i] = fmt.Sprintf("L%02d", i)
	}
	th := components.DefaultTheme()
	pane := NewTranscriptPane(th, status.NewSpinner(th.ToolName), "Phi test")
	copied := new(string)
	pane.SetCopyHandlers(controller.NewBus(nil), func(text string) bool {
		*copied = text
		return true
	})
	pane.LoadReplay(session.Snapshot{Messages: []session.Message{
		{ID: "u1", Role: session.RoleUser, State: session.StateComplete, Text: strings.Join(rows, "\n")},
	}})
	pane.Sync()
	drawPane(pane)
	require.Negative(t, pane.list.ContentOrigin(), "setup: message must overflow one page")
	return pane, copied
}

func drawPane(pane *TranscriptPane) {
	pane.Draw(
		components.DrawContext{Max: components.Size{Width: 60, Height: paneTestRows}},
		60,
		paneTestRows,
	)
}

// Drag endpoint columns: left of the message text and past its right edge. The
// list pads entries by one column.
const (
	selTopX    = 2
	selBottomX = 20
)

// dragOverRows starts a drag on row from and moves it to row to, either way
// round. The first copied row takes its column from whichever endpoint sits
// nearer the top, so the press and the move get their columns by position.
func dragOverRows(pane *TranscriptPane, from, to int) {
	fromX, toX := selTopX, selBottomX
	if from > to {
		fromX, toX = selBottomX, selTopX
	}
	ctx := &components.EventContext{}
	pane.HandleMouse(ctx, xui.MouseEvent{X: fromX, Y: from, Action: xui.MousePress, Button: xui.MouseLeft}, nil)
	pane.HandleMouse(ctx, xui.MouseEvent{X: toX, Y: to, Action: xui.MouseDrag, Button: xui.MouseLeft}, nil)
}

// releaseDrag ends a left-button drag at (x, y); a drag that moved copies.
func releaseDrag(pane *TranscriptPane, x, y int) {
	pane.HandleMouse(
		&components.EventContext{},
		xui.MouseEvent{X: x, Y: y, Action: xui.MouseRelease, Button: xui.MouseLeft},
		nil,
	)
}

// scrollUp feeds wheel-up events, redrawing after each as the run loop does
// between frames.
func scrollUp(pane *TranscriptPane, times int) {
	ctx := &components.EventContext{}
	for range times {
		pane.HandleMouse(ctx, xui.MouseEvent{Button: xui.MouseWheelUp, Wheel: 1}, nil)
		drawPane(pane)
	}
}

// requireCopiedRows asserts the clipboard holds every numbered line of the
// message, in order, from the first row to the last.
func requireCopiedRows(t *testing.T, copied string) {
	t.Helper()
	rows := strings.Split(copied, "\n")
	require.Len(t, rows, longMsgLines, "copy must span both pages:\n%s", copied)
	for i, row := range rows {
		require.Equal(t, fmt.Sprintf("L%02d", i), strings.TrimSpace(row), "row %d", i)
	}
}

// A message taller than the viewport spans pages: a drag that scrolls the list
// must copy the rows the viewport scrolled away from, not just the visible page.
func TestTranscriptPane_DragSelectionCopiesScrolledAwayRows(t *testing.T) {
	pane, copied := longMessagePane(t)

	// Anchor on the last visible row (the message's last line), drag up to the
	// top edge, then scroll the rest of the way: only the first page is on
	// screen at the start, and only the second one is by the end.
	dragOverRows(pane, paneTestRows-1, 0)
	scrollUp(pane, longMsgLines)
	releaseDrag(pane, selTopX, 0)

	requireCopiedRows(t, *copied)
}

// Dragging onto an edge row scrolls the list on its own, so one gesture covers
// pages the pointer could never reach directly.
func TestTranscriptPane_DragAtEdgeAutoScrolls(t *testing.T) {
	pane, copied := longMessagePane(t)

	dragOverRows(pane, paneTestRows-1, 0)
	for range 2 * longMsgLines {
		drawPane(pane)
	}
	require.Zero(t, pane.list.ContentOrigin(), "drag on the top edge must scroll to the first row")
	releaseDrag(pane, selTopX, 0)

	requireCopiedRows(t, *copied)
}

// The opposite edge scrolls the other way, back toward the latest content.
func TestTranscriptPane_DragAtBottomEdgeScrollsDown(t *testing.T) {
	pane, _ := longMessagePane(t)
	scrollUp(pane, longMsgLines)
	require.Zero(t, pane.list.ContentOrigin(), "setup: scrolled to the first row")

	dragOverRows(pane, 0, paneTestRows-1)
	for range longMsgLines {
		drawPane(pane)
	}
	require.Negative(t, pane.list.ContentOrigin(), "drag on the bottom edge must scroll to the last row")
}

// The terminal can lose a release (pointer dragged out of the window). The next
// button-up motion over the list must end the drag, or the auto-scroll stays
// live and keeps pulling the viewport to an edge.
func TestTranscriptPane_LostReleaseEndsDrag(t *testing.T) {
	pane, copied := longMessagePane(t)

	dragOverRows(pane, paneTestRows-1, 0)
	drawPane(pane)
	// Button up, no release event: the selection is copied and the drag stops.
	pane.HandleMouse(
		&components.EventContext{},
		xui.MouseEvent{X: 2, Y: 0, Action: xui.MouseMotion},
		nil,
	)
	require.NotEmpty(t, *copied, "a lost release must still copy the selection")
	require.True(t, pane.SelectionActive(), "the highlight outlives the drag")

	scrolled := pane.list.ContentOrigin()
	for range longMsgLines {
		drawPane(pane)
	}
	require.Equal(t, scrolled, pane.list.ContentOrigin(), "a finished drag must stop auto-scrolling")
}

// viewportSelectionText reads a selection the pre-fix way: content coordinates
// mapped into the drawn viewport surface, extracted there.
func viewportSelectionText(pane *TranscriptPane, ax, ay, ex, ey int) string {
	o := pane.list.ContentOrigin()
	return components.ExtractSurfaceText(pane.lastListSurf, ax, ay+o, ex, ey+o)
}

func paneContentHeight(pane *TranscriptPane) int {
	total := 0
	for i := range pane.list.Entries {
		if i > 0 {
			total++ // item gap
		}
		total += pane.list.CachedHeight(i)
	}
	return total
}

// A selection inside the viewport must still copy exactly what the drawn
// surface holds; re-rendering only adds the rows the viewport virtualized away.
func TestTranscriptPane_SelectionTextMatchesViewportRead(t *testing.T) {
	const width = 60
	th := components.DefaultTheme()
	pane := NewTranscriptPane(th, status.NewSpinner(th.ToolName), "Phi test")
	pane.LoadReplay(session.Snapshot{
		Messages: []session.Message{
			{
				ID: "u1", Role: session.RoleUser, State: session.StateComplete,
				// CJK, a wrapping line, and short lines: the whole render surface.
				Text: "hello world\n中文宽度测试 mixed 内容\nshort\n" +
					"a line long enough to wrap inside the block's inner width\n" +
					strings.Repeat("line\n", 20),
			},
			{
				ID: "a1", Role: session.RoleAssistant, State: session.StateComplete,
				Content: []session.ContentBlock{{Type: session.BlockToolUse, ID: "t1", Name: "bash"}},
			},
		},
		Tools: map[string]session.ToolRun{
			"t1": {ToolUseID: "t1", Name: "bash", Status: session.ToolDone, Detail: "ls -la", Output: "a.go\nb.go"},
		},
	})
	pane.Sync()
	drawPane(pane)

	total, origin := paneContentHeight(pane), pane.list.ContentOrigin()
	first := max(0, -origin)
	last := min(total-1, -origin+paneTestRows-1)
	xRanges := [][2]int{{0, width - 1}, {1, 30}, {3, 3}, {2, 10}, {0, 0}, {20, 25}, {width - 1, width - 1}}
	for ay := first; ay <= last; ay++ {
		for ey := ay; ey <= last; ey++ {
			for _, xr := range xRanges {
				want := viewportSelectionText(pane, xr[0], ay, xr[1], ey)
				got := pane.list.SelectionText(xr[0], ay, xr[1], ey)
				require.Equal(t, want, got, "rows %d..%d x %d..%d", ay, ey, xr[0], xr[1])
			}
		}
	}
}

// A press that never moves is a click: the same row toggles instead of copying.
func TestTranscriptPane_ClickOnTitleRowToggles(t *testing.T) {
	pane, _, bash := bashPane(t)
	const row = paneTestRows - 1

	ctx := &components.EventContext{}
	pane.HandleMouse(ctx, xui.MouseEvent{X: 2, Y: row, Action: xui.MousePress, Button: xui.MouseLeft}, nil)
	require.False(t, bash.Expanded, "press alone must not expand")
	pane.HandleMouse(ctx, xui.MouseEvent{X: 2, Y: row, Action: xui.MouseRelease, Button: xui.MouseLeft}, nil)

	require.True(t, bash.Expanded, "click on the title row expands")
	require.Equal(t, len(pane.list.Entries)-1, pane.list.Selected, "the clicked row is selected for Cmd+C")
}
