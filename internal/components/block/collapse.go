package block

import "github.com/pulseaiclub/xui"

// The collapsible blocks (tool / thinking / bash / agent) share one expansion
// policy: Enter/space toggles a focused block, and a click toggles it only on
// the title row at surface-local y. ClickAt is the whole mouse surface — the
// transcript pane calls it after it has ruled out a drag-selection.

// titleHit reports whether surface-local row y lands on a block's title row.
func titleHit(titleH, y int) bool {
	return y >= 0 && y < titleH
}

// toggleKey reports whether ev is the Enter/space expansion chord.
func toggleKey(ev xui.Event) bool {
	e, ok := ev.(xui.KeyEvent)
	if !ok || !e.Press {
		return false
	}
	return e.Code == xui.KeyEnter || (e.Code == xui.KeyRune && e.Rune == ' ')
}
