package palette

import (
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
)

func TestCommandPaletteFilterAndAccept(t *testing.T) {
	accepted := ""
	p := &CommandPalette{
		Theme: components.DefaultTheme(),
		Commands: []PaletteCommand{
			{ID: "1", Noun: "mode", Verb: "use boost"},
			{ID: "2", Noun: "app", Verb: "help"},
			{ID: "3", Noun: "session", Verb: "switch", Shortcut: "Ctrl t"},
		},
		OnAccept: func(c PaletteCommand) { accepted = c.ID },
	}
	p.Show()
	require.True(t, p.Open && len(p.filtered) == 3, "open=%v filtered=%d", p.Open, len(p.filtered))

	ctx := &components.EventContext{}
	for _, r := range "help" {
		p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: r, Press: true})
	}
	require.Len(t, p.filtered, 1)
	require.Equal(t, "2", p.Commands[p.filtered[0]].ID)
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	require.Equal(t, "2", accepted)
	require.False(t, p.Open)
}

func TestCommandPaletteDraw(t *testing.T) {
	p := &CommandPalette{
		Theme: components.DefaultTheme(),
		Commands: []PaletteCommand{
			{ID: "1", Noun: "settings", Verb: "theme"},
			{ID: "2", Noun: "plugins", Verb: "reload"},
		},
	}
	p.Show()
	s := p.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
	require.Len(t, s.Children, 1)
	panel := s.Children[0].Surface
	var b strings.Builder
	for x := 0; x < panel.Size.Width; x++ {
		ch := panel.Buffer[x].Char
		if ch == "" {
			ch = " "
		}
		b.WriteString(ch)
	}
	top := b.String()
	require.Contains(t, top, "Command Palette")
}

func TestFuzzyMatch(t *testing.T) {
	ok, _ := fuzzyMatch("", "mode use boost")
	require.True(t, ok, "empty query should match")
	ok, score := fuzzyMatch("boost", "mode use boost")
	require.True(t, ok && score >= 0.15, "boost score=%v", score)
	ok, _ = fuzzyMatch("zzz", "mode use boost")
	require.False(t, ok, "zzz should not match")
}

func TestCommandPaletteSaveFilteredSelection(t *testing.T) {
	saved := ""
	success := false
	p := &CommandPalette{Commands: []PaletteCommand{
		{ID: "other", Verb: "other", OnSave: func() bool { saved = "other"; return true }},
		{ID: "target", Verb: "target", OnSave: func() bool { saved = "target"; return success }},
	}}
	p.Show()
	for _, r := range "target" {
		p.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyRune, Rune: r, Press: true})
	}
	require.Len(t, p.filtered, 1)
	ctx := &components.EventContext{}
	saveKey := xui.KeyEvent{Code: xui.KeyRune, Rune: 's', Mods: xui.ModCtrl, Press: true}
	p.Handle(ctx, saveKey)
	require.Empty(t, saved, "Ctrl+S should ask before saving")
	require.True(t, p.Open)
	require.True(t, ctx.Consume)
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'y', Press: true})
	require.Equal(t, "target", saved)
	require.True(t, p.Open, "failed saves must leave the selection available for retry")
	success = true
	p.Handle(ctx, saveKey)
	require.True(t, p.Open, "Ctrl+S should arm confirmation again")
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	require.False(t, p.Open)
}

func TestCommandPaletteSaveIgnoresUnavailableActions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		commands []PaletteCommand
	}{
		{"empty", nil},
		{"ordinary command", []PaletteCommand{{ID: "theme", Verb: "theme"}}},
		{"disabled", []PaletteCommand{{ID: "disabled", Disabled: true, OnSave: func() bool {
			require.Fail(t, "disabled save must not run")
			return true
		}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &CommandPalette{Commands: tc.commands}
			p.Show()
			p.Handle(&components.EventContext{}, xui.KeyEvent{
				Code: xui.KeyRune, Rune: 's', Mods: xui.ModCtrl, Press: true,
			})
			require.True(t, p.Open)
			require.Empty(t, p.Query())
			require.False(t, p.saveConfirming)
		})
	}
}

func TestCommandPaletteSaveConfirmFreezesSelection(t *testing.T) {
	saved := ""
	p := &CommandPalette{Commands: []PaletteCommand{
		{Verb: "first", OnSave: func() bool { saved = "first"; return true }},
		{Verb: "second", OnSave: func() bool { saved = "second"; return true }},
	}}
	p.Show()
	ctx := &components.EventContext{}
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 's', Mods: xui.ModCtrl, Press: true})
	for _, ev := range []xui.Event{
		xui.KeyEvent{Code: xui.KeyDown, Press: true},
		xui.KeyEvent{Code: xui.KeyRune, Rune: 'q', Press: true},
		xui.KeyEvent{Code: xui.KeyRune, Rune: 'y', Mods: xui.ModCtrl, Press: true},
		xui.PasteEvent{Text: "second"},
	} {
		p.Handle(ctx, ev)
	}
	require.Empty(t, p.Query())
	require.Zero(t, p.Selected)
	require.Empty(t, saved)
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	require.Equal(t, "first", saved)
}

func TestCommandPaletteSaveHintAndConfirmationDraw(t *testing.T) {
	p := &CommandPalette{Title: "Select Model", Commands: []PaletteCommand{
		{Verb: "first", OnSave: func() bool { return true }},
		{Verb: "second", OnSave: func() bool { return true }},
	}}
	p.Show()
	draw := func(width int) components.Surface {
		t.Helper()
		s := p.Draw(components.DrawContext{Max: components.Size{Width: width, Height: 24}, Method: xui.WidthUnicode})
		require.Len(t, s.Children, 1)
		return s.Children[0].Surface
	}
	panel := draw(100)
	text := components.SurfaceText(panel)
	require.Equal(t, 1, strings.Count(text, "ctrl+s save default"))
	require.Contains(t, text, "⏎ switch")
	require.Equal(t, 5, panel.Size.Height, "hint belongs on the border without an extra row")
	require.NotNil(t, panel.Cursor)
	p.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyRune, Rune: 's', Mods: xui.ModCtrl, Press: true})
	panel = draw(100)
	text = components.SurfaceText(panel)
	require.Contains(t, text, "Apply this model and save as global default?")
	require.Contains(t, text, "y/enter save · n/esc cancel")
	require.Nil(t, panel.Cursor)
	require.Contains(t, components.SurfaceText(draw(32)), "y save · n cancel")
	p.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyRune, Rune: 'n', Press: true})
	require.True(t, p.Open)
	require.False(t, p.saveConfirming)
	require.Contains(t, components.SurfaceText(draw(32)), "ctrl+s save default")
}

func TestCommandPaletteSaveCancelKeepsPickerUsable(t *testing.T) {
	saved := false
	p := &CommandPalette{Commands: []PaletteCommand{
		{ID: "model", Verb: "model", OnSave: func() bool { saved = true; return true }},
	}}
	p.Show()
	ctx := &components.EventContext{}
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 's', Mods: xui.ModCtrl, Press: true})
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEscape, Press: true})
	require.False(t, saved)
	require.True(t, p.Open)
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	require.False(t, p.Open, "after cancel, enter should return to normal selection")
	require.False(t, saved, "Enter after cancel must not save the default")
}

func TestCommandPaletteNestedSubmenu(t *testing.T) {
	picked := ""
	p := &CommandPalette{
		Theme: components.DefaultTheme(),
		Commands: []PaletteCommand{
			{
				ID:           "settings-theme",
				Noun:         "settings",
				Verb:         "theme",
				SubmenuTitle: "Select Theme",
				Submenu: []PaletteCommand{
					{ID: "dark", Verb: "Dark (builtin)", Run: func() { picked = "dark" }},
					{ID: "light", Verb: "Light (builtin)", Run: func() { picked = "light" }},
				},
			},
			{ID: "other", Noun: "app", Verb: "help", Run: func() { picked = "help" }},
		},
	}
	p.Show()
	ctx := &components.EventContext{}
	// Filter to theme command
	for _, r := range "theme" {
		p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: r, Press: true})
	}
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	require.True(t, p.Open, "expected nested open title=%q stack=%d open=%v", p.Title, len(p.stack), p.Open)
	require.Equal(t, "Select Theme", p.Title)
	require.Len(t, p.stack, 1)
	// Esc pops back
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEscape, Press: true})
	require.True(t, p.Open, "pop failed title=%q stack=%d", p.Title, len(p.stack))
	require.Equal(t, "Command Palette", p.Title)
	require.Empty(t, p.stack)
	// Enter submenu again and pick Dark
	p.SetQuery("")
	p.Selected = 0
	p.refilter()
	for _, r := range "theme" {
		p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: r, Press: true})
	}
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true}) // first theme
	require.Equal(t, "dark", picked)
	require.False(t, p.Open)
}
