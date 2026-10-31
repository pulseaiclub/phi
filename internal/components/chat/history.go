package chat

import "strings"

// Composer input history.
//
// The composer keeps the recall ring in History (oldest first) and only tracks
// an index into it: histPos is 0 when the user is not browsing, and 1..len
// while browsing (histPos-1 is the visible entry). histDraft holds whatever was
// typed before the first Up, so Down past the newest entry restores it.
//
// Entries live in a per-project store (see internal/session/shellhist); this
// file knows nothing about files, only about moving through a []string.

// historyReset leaves browse mode. Any real edit calls it, so the moment the
// user types into a recalled entry it becomes a normal draft again.
func (c *ChatInput) historyReset() {
	if c.histBusy {
		return
	}
	c.resetBrowse()
}

// ResetHistoryBrowse leaves browse mode after the caller rewrote Value itself
// (ClearInput, SetInput), where no edit event fires to reset it.
func (c *ChatInput) ResetHistoryBrowse() { c.resetBrowse() }

func (c *ChatInput) resetBrowse() {
	c.histPos = 0
	c.histDraft = ""
}

// historyApply swaps in a recalled entry. histBusy keeps the notifyChange that
// follows from calling historyReset and dropping the index it just set.
func (c *ChatInput) historyApply(text string) {
	c.histBusy = true
	c.Value = text
	c.Cursor = len(text)
	c.notifyChange()
	c.histBusy = false
}

// historyUp recalls an older entry. It reports whether the key was consumed.
// At the top of the ring it stays put rather than falling through to a cursor
// move, so holding Up cannot silently rewind the caret.
func (c *ChatInput) historyUp() bool {
	if len(c.History) == 0 {
		return false
	}
	if c.histPos == 0 {
		c.histDraft = c.Value
		c.histPos = len(c.History)
		c.historyApply(c.History[len(c.History)-1])
		return true
	}
	if c.histPos > 1 {
		c.histPos--
		c.historyApply(c.History[c.histPos-1])
	}
	return true
}

// historyDown moves toward newer entries and restores the draft once it walks
// off the end of the ring.
func (c *ChatInput) historyDown() bool {
	if c.histPos == 0 {
		return false
	}
	if c.histPos < len(c.History) {
		c.histPos++
		c.historyApply(c.History[c.histPos-1])
		return true
	}
	draft := c.histDraft
	c.histPos = 0
	c.histDraft = ""
	c.historyApply(draft)
	return true
}

// historyAtFirstLine reports whether the caret sits on the first logical line,
// which is where Up means "recall" instead of "move up". Logical rather than
// wrapped lines keep the rule width-independent and predictable.
func (c *ChatInput) historyAtFirstLine() bool {
	return !strings.Contains(c.Value[:c.clampOffset(c.Cursor)], "\n")
}

// historyAtLastLine reports whether the caret sits on the last logical line,
// which is where Down means "next entry" instead of "move down".
func (c *ChatInput) historyAtLastLine() bool {
	return !strings.Contains(c.Value[c.clampOffset(c.Cursor):], "\n")
}

// clampOffset keeps the slicing below safe when a caller sets Cursor directly
// instead of going through Handle's clampCursor.
func (c *ChatInput) clampOffset(off int) int {
	return min(max(off, 0), len(c.Value))
}
