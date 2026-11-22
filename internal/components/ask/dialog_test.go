package ask

import (
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
)

// testDialog mirrors the error wiring: three options with direct hotkeys.
func testDialog() *Dialog {
	return &Dialog{
		Title: "Error",
		Hint:  chrome.AskHint("↑↓ move", "select", "dismiss"),
		Options: []Option{
			{Label: "Retry", Keys: "r"},
			{Label: "Copy to Clipboard", Keys: "cy"},
			{Label: "Dismiss", Keys: "dn"},
		},
	}
}

func pressCode(code xui.KeyCode) xui.KeyEvent {
	return xui.KeyEvent{Press: true, Code: code}
}

func pressRune(r rune) xui.KeyEvent {
	return xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: r}
}

func TestDialogKeyNavigation(t *testing.T) {
	d := testDialog()

	// Down ×2 lands on the last option; Down wraps to the first.
	_, _ = d.HandleKey(pressCode(xui.KeyDown))
	_, _ = d.HandleKey(pressCode(xui.KeyDown))
	require.Equal(t, 2, d.selected)
	_, _ = d.HandleKey(pressCode(xui.KeyDown))
	require.Equal(t, 0, d.selected)

	// Up wraps from the first to the last.
	_, _ = d.HandleKey(pressCode(xui.KeyUp))
	require.Equal(t, 2, d.selected)

	// Enter commits the selected option.
	_, _ = d.HandleKey(pressCode(xui.KeyUp))
	pick, chosen := d.HandleKey(pressCode(xui.KeyEnter))
	require.True(t, chosen)
	require.Equal(t, 1, pick)
}

func TestDialogEscapeCommitsDismissal(t *testing.T) {
	d := testDialog()
	pick, chosen := d.HandleKey(pressCode(xui.KeyEscape))
	require.True(t, chosen)
	require.Equal(t, -1, pick)
}

func TestDialogHotkeys(t *testing.T) {
	for rune, want := range map[rune]int{'r': 0, 'R': 0, 'c': 1, 'C': 1, 'y': 1, 'd': 2, 'n': 2} {
		d := testDialog()
		pick, chosen := d.HandleKey(pressRune(rune))
		require.True(t, chosen, "rune %q must commit", rune)
		require.Equal(t, want, pick, "rune %q", rune)
	}
}

func TestDialogIgnoresModifiedRunes(t *testing.T) {
	d := testDialog()
	pick, chosen := d.HandleKey(xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: 'r', Mods: xui.ModCtrl})
	require.False(t, chosen)
	require.Equal(t, 0, pick)
}

func TestDialogMovementRunes(t *testing.T) {
	d := testDialog()
	// j/l wrap; k/h clamp at the first row.
	_, _ = d.HandleKey(pressRune('j'))
	require.Equal(t, 1, d.selected)
	_, _ = d.HandleKey(pressRune('l'))
	require.Equal(t, 2, d.selected)
	_, _ = d.HandleKey(pressRune('j'))
	require.Equal(t, 0, d.selected)
	_, _ = d.HandleKey(pressRune('h'))
	require.Equal(t, 0, d.selected, "h must clamp at the first row")
	_, _ = d.HandleKey(pressRune('k'))
	require.Equal(t, 0, d.selected)

	_, chosen := d.HandleKey(pressRune('x'))
	require.False(t, chosen, "unmapped runes only redraw")
}

func TestDialogNoOptionsIsSafe(t *testing.T) {
	d := &Dialog{}
	pick, chosen := d.HandleKey(pressCode(xui.KeyDown))
	require.False(t, chosen)
	require.Equal(t, 0, pick)
	require.Equal(t, 8, d.PreferredHeight(60, 0))
}

func TestDialogHeightAccountsForWrapping(t *testing.T) {
	long := "LLM API error (400): The service ID does not exist, or the model " +
		"does not match the service. Please confirm the service ID in the console."
	d := testDialog()
	d.Message = long
	// At width 60 the message wraps to ~3 rows; the panel must fit
	// title + message + gap + 3 options + hint inside its borders.
	h := d.PreferredHeight(60, 0)
	require.GreaterOrEqual(t, h, 2+1+1+3+1+len(d.Options)+1)

	// Every option row must be inside the border (row 0 and h-1 are the frame).
	surf := d.Draw(components.DefaultTheme(), components.DefaultTheme().Destructive, 60, h, 0)
	plain := components.SurfaceText(surf)
	for _, opt := range d.Options {
		require.Contains(t, plain, opt.Label)
	}
	require.Contains(t, plain, "dismiss", "hint line must survive")
}

func TestDialogConstrainedHeightsKeepActions(t *testing.T) {
	d := testDialog()
	d.Message = strings.Repeat("long wrapped error ", 20)

	for _, height := range []int{8, 9} {
		surf := d.Draw(components.DefaultTheme(), components.DefaultTheme().Destructive, 60, height, 0)
		body := components.SurfaceText(surf)
		for _, opt := range d.Options {
			require.Contains(t, body, opt.Label, "height %d must retain action %q", height, opt.Label)
		}
		require.Contains(t, body, "dismiss", "height %d must retain the hint when it fits")
		require.Equal(t, "│", surf.Buffer[1*surf.Size.Width+surf.Size.Width-1].Char)
	}
}

func bodyText(d *Dialog, width, height int) string {
	th := components.DefaultTheme()
	lines := d.visibleLines(th, th.Destructive, innerWidth(width), height, 0)
	var b strings.Builder
	for _, line := range lines {
		for _, span := range line {
			b.WriteString(span.Text)
		}
	}
	return b.String()
}

func TestDialogNarrowWidthWrapsActionsWithoutOverwritingBorder(t *testing.T) {
	d := testDialog()
	d.Message = strings.Repeat("error ", 20)
	d.selected = 1

	for _, width := range []int{12, 8} {
		surf := d.Draw(components.DefaultTheme(), components.DefaultTheme().Destructive, width, 9, 0)
		require.Contains(t, bodyText(d, width, 9), "Copy to Clipboard",
			"selected action must remain visible at width %d", width)
		for y := 1; y < surf.Size.Height-1; y++ {
			require.Equal(t, "│", surf.Buffer[y*surf.Size.Width+surf.Size.Width-1].Char,
				"width %d row %d right border must survive", width, y)
		}
	}
}

func TestDialogEmptyMessageUsesMinimumLayout(t *testing.T) {
	d := testDialog()
	require.Equal(t, 8, d.PreferredHeight(60, 0))
	body := bodyText(d, 60, 8)
	for _, opt := range d.Options {
		require.Contains(t, body, opt.Label)
	}
	require.Contains(t, body, "dismiss")
}
