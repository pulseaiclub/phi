package onboard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
)

// newDialog starts with both fields empty; tests type into them.
func newDialog() *Dialog {
	return New(components.DarkTheme(), "", "")
}

func press(d *Dialog, code xui.KeyCode, r rune) *components.EventContext {
	ctx := &components.EventContext{}
	d.Handle(ctx, xui.KeyEvent{Code: code, Rune: r, Press: true})
	return ctx
}

func typeText(t *testing.T, d *Dialog, text string) {
	t.Helper()
	for _, r := range text {
		press(d, xui.KeyRune, r)
	}
}

func fillBoth(t *testing.T, d *Dialog, name, key string) {
	t.Helper()
	typeText(t, d, name)
	press(d, xui.KeyTab, 0)
	typeText(t, d, key)
}

func TestSubmitSavesBothValues(t *testing.T) {
	d := newDialog()
	fillBoth(t, d, "  deepseek-flash  ", "sk-test-123")

	ctx := press(d, xui.KeyEnter, 0)
	assert.True(t, d.Submitted())
	assert.False(t, d.Aborted())
	assert.True(t, ctx.Quit, "a submitted dialog must close the loop")
	assert.Equal(t, "deepseek-flash", d.Name(), "value is trimmed")
	assert.Equal(t, "sk-test-123", d.APIKey())
}

func TestSubmitWithEmptyFieldsPointsAtTheField(t *testing.T) {
	d := newDialog()

	ctx := press(d, xui.KeyEnter, 0)
	assert.False(t, d.Submitted())
	assert.False(t, ctx.Quit)
	assert.Equal(t, "enter a model name", d.err)
	assert.Equal(t, fieldName, d.focus)

	typeText(t, d, "deepseek-flash")
	press(d, xui.KeyEnter, 0)
	assert.Equal(t, "enter the API key", d.err)
	assert.Equal(t, fieldKey, d.focus)
	assert.False(t, d.Submitted())

	// Typing clears the error.
	typeText(t, d, "sk-key")
	assert.Empty(t, d.err)
	press(d, xui.KeyEnter, 0)
	assert.True(t, d.Submitted())
}

func TestEscapeAborts(t *testing.T) {
	d := newDialog()
	fillBoth(t, d, "deepseek-flash", "sk-test")

	ctx := press(d, xui.KeyEscape, 0)
	assert.True(t, d.Aborted())
	assert.False(t, d.Submitted())
	assert.True(t, ctx.Quit)
}

func TestPrefillAndTabRoundTrip(t *testing.T) {
	d := New(components.DarkTheme(), "gemini-3-pro", "sk-prefill")
	assert.Equal(t, "gemini-3-pro", d.name.Text())
	assert.Equal(t, "sk-prefill", d.key.Text())

	// Tab from the name lands on the key field and wraps back around.
	assert.Equal(t, fieldName, d.focus)
	press(d, xui.KeyTab, 0)
	assert.Equal(t, fieldKey, d.focus)
	press(d, xui.KeyUp, 0)
	assert.Equal(t, fieldName, d.focus)
}

func TestPasteGoesToFocusedField(t *testing.T) {
	d := newDialog()
	typeText(t, d, "deep")
	ctx := &components.EventContext{}
	d.Handle(ctx, xui.PasteEvent{Text: "-flash"})
	assert.Equal(t, "deep-flash", d.Name())

	press(d, xui.KeyTab, 0)
	d.Handle(ctx, xui.PasteEvent{Text: "sk-1\nsk-2"})
	assert.Equal(t, "sk-1 sk-2", d.APIKey(), "paste stays single-line")
}

func TestDrawMasksTheKeyAndShowsTheCaret(t *testing.T) {
	d := newDialog()
	fillBoth(t, d, "deepseek-flash", "sk-secret-value")

	ctx := components.DrawContext{Max: components.Size{Width: 100, Height: 30}, Method: xui.WidthUnicode}
	surf := d.Draw(ctx)
	text := components.SurfaceText(surf)

	assert.Contains(t, text, "Set up phi")
	assert.Contains(t, text, "model")
	assert.Contains(t, text, "api key")
	assert.Contains(t, text, "deepseek-flash")
	assert.NotContains(t, text, "sk-secret-value", "the key must never be painted in the clear")
	assert.Contains(t, text, "••••••••••••••", "the key reads as dots")

	// The caret sits on the focused key row of the centered panel; the app
	// loop picks it up from the child surface (origin-relative).
	require.Len(t, surf.Children, 1)
	require.NotNil(t, surf.Children[0].Surface.Cursor)
	assert.Positive(t, surf.Children[0].Surface.Cursor.X)
	assert.Positive(t, surf.Children[0].Surface.Cursor.Y)
}

func TestDrawHintsWhatTheNameDerives(t *testing.T) {
	ctx := components.DrawContext{Max: components.Size{Width: 100, Height: 30}, Method: xui.WidthUnicode}

	d := newDialog()
	typeText(t, d, "deepseek-flash")
	assert.Contains(t, components.SurfaceText(d.Draw(ctx)), "https://api.deepseek.com")

	d = newDialog()
	typeText(t, d, "totally-unknown")
	text := components.SurfaceText(d.Draw(ctx))
	assert.Contains(t, text, "unknown name")
	assert.Contains(t, text, "api.openai.com")

	d = newDialog()
	typeText(t, d, "vendor/glm-5.3-flash:free")
	assert.Contains(t, components.SurfaceText(d.Draw(ctx)), "base_url with phi config")
}

func TestCtrlEditingKeys(t *testing.T) {
	d := newDialog()
	typeText(t, d, "deepseek-flash-delete-me")
	ctrl := func(r rune) {
		d.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyRune, Rune: r, Mods: xui.ModCtrl, Press: true})
	}
	// Ctrl+W rubs out the word left of the caret ("me").
	typeText(t, d, " deepseek flash delete me")
	ctrl('w')
	assert.Equal(t, "deepseek-flash-delete-me deepseek flash delete", d.Name())
	// Ctrl+U clears the line.
	ctrl('u')
	assert.Empty(t, d.Name())
}

func TestUnknownKeysAreSwallowed(t *testing.T) {
	d := newDialog()
	for _, code := range []xui.KeyCode{xui.KeyF1, xui.KeyPageUp, xui.KeyPageDown} {
		ctx := press(d, code, 0)
		assert.True(t, ctx.Consume, "code %v must not leak to a parent", code)
		assert.False(t, ctx.Quit)
	}
	assert.False(t, d.Submitted())
	assert.False(t, d.Aborted())
	assert.Contains(t, maskInput("abc"), "•••")
}
