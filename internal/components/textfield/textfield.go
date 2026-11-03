// Package textfield is the single-line text buffer shared by the palette,
// list picker, permission feedback box, and config form. It owns rune storage
// and the caret; hosts keep their own key routing, filtering, and commit
// behavior on top.
package textfield

import "strings"

// Field is a single-line buffer with a rune-index caret. The zero value is
// an empty field. Mutators reallocate the backing array so shallow struct
// copies (palette page stack) stay independent.
type Field struct {
	buf []rune
	cur int
}

// Text returns the buffer contents.
func (f *Field) Text() string { return string(f.buf) }

// Cursor is the rune index the caret sits at.
func (f *Field) Cursor() int { return f.cur }

// PeekBefore returns the rune left of the caret, if any.
func (f *Field) PeekBefore() (rune, bool) {
	if f.cur == 0 {
		return 0, false
	}
	return f.buf[f.cur-1], true
}

// SetText replaces the contents and parks the caret at the end.
func (f *Field) SetText(text string) {
	f.buf = []rune(text)
	f.cur = len(f.buf)
}

// Split returns the text before and after the caret, for rendering.
func (f *Field) Split() (before, after string) {
	return string(f.buf[:f.cur]), string(f.buf[f.cur:])
}

// Insert splices text at the caret. Newlines become spaces so the line stays
// single-line even on clipboard paste.
func (f *Field) Insert(text string) {
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	if text == "" {
		return
	}
	runes := []rune(text)
	out := make([]rune, 0, len(f.buf)+len(runes))
	out = append(out, f.buf[:f.cur]...)
	out = append(out, runes...)
	out = append(out, f.buf[f.cur:]...)
	f.buf = out
	f.cur += len(runes)
}

// DeleteBefore removes the rune left of the caret and reports whether it did.
func (f *Field) DeleteBefore() bool {
	if f.cur == 0 {
		return false
	}
	out := make([]rune, 0, len(f.buf)-1)
	out = append(out, f.buf[:f.cur-1]...)
	out = append(out, f.buf[f.cur:]...)
	f.buf = out
	f.cur--
	return true
}

// DeleteAt removes the rune under the caret and reports whether it did.
func (f *Field) DeleteAt() bool {
	if f.cur >= len(f.buf) {
		return false
	}
	out := make([]rune, 0, len(f.buf)-1)
	out = append(out, f.buf[:f.cur]...)
	out = append(out, f.buf[f.cur+1:]...)
	f.buf = out
	return true
}

// Move slides the caret by delta runes, clamped to the buffer.
func (f *Field) Move(delta int) {
	f.cur = min(max(f.cur+delta, 0), len(f.buf))
}

// Home parks the caret at the start of the line.
func (f *Field) Home() { f.cur = 0 }

// End parks the caret at the end of the line.
func (f *Field) End() { f.cur = len(f.buf) }

// KillBefore removes everything from the start of the line to the caret.
func (f *Field) KillBefore() {
	f.buf = append([]rune(nil), f.buf[f.cur:]...)
	f.cur = 0
}

// KillAfter removes everything from the caret to the end of the line.
func (f *Field) KillAfter() {
	f.buf = append([]rune(nil), f.buf[:f.cur]...)
}
