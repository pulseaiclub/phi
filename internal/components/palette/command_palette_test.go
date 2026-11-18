package palette

import (
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
)

func TestCommandPaletteUsesSharedPicker(t *testing.T) {
	accepted := ""
	p := &CommandPalette{Commands: []PaletteCommand{
		{ID: "1", Noun: "settings", Verb: "theme", Description: "Change the color theme"},
		{ID: "2", Noun: "app", Verb: "help", Description: "Show help", Run: func() { accepted = "2" }},
	}}
	p.Show()
	require.True(t, p.Picker.Open)

	s := p.Picker.Draw(components.DrawContext{
		Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode,
	})
	require.Len(t, s.Children, 1)
	assert.Equal(t, components.Point{X: 2, Y: 8}, s.Children[0].Origin)
	assert.IsType(t, &listpicker.Picker{}, s.Children[0].Surface.Widget)
	assert.Equal(t, p.Picker.Items, s.Children[0].Surface.Widget.(*listpicker.Picker).Items)
	text := components.SurfaceText(s.Children[0].Surface)
	assert.Contains(t, text, "Command Palette")
	assert.Contains(t, text, "theme  Change the color theme")
	assert.Contains(t, text, "help   Show help")
	panel := s.Children[0].Surface
	assert.GreaterOrEqual(t, panel.Size.Width, 56, "the command palette keeps a readable working width")
	row := panel.Buffer[4*panel.Size.Width : 5*panel.Size.Width]
	assert.Equal(t, "t", row[4].Char)
	assert.Equal(t, components.DefaultTheme().SelectionFg.Fg, row[11].Style.Fg)
	p.Picker.Selected = 1
	s = p.Picker.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
	panel = s.Children[0].Surface
	assert.Equal(t, components.DefaultTheme().Muted.Fg, panel.Buffer[4*panel.Size.Width+11].Style.Fg)

	for _, r := range "help" {
		p.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyRune, Rune: r, Press: true})
	}
	p.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	assert.Equal(t, "2", accepted)
	assert.False(t, p.Picker.Open)
}

func TestCommandPaletteNestedSubmenu(t *testing.T) {
	picked := ""
	p := &CommandPalette{Commands: []PaletteCommand{
		{
			ID: "settings-theme", Noun: "settings", Verb: "theme", SubmenuTitle: "Select Theme",
			Submenu: []PaletteCommand{
				{ID: "dark", Verb: chrome.CurrentMarker(true) + "Dark (builtin)", Run: func() { picked = "dark" }},
				{ID: "light", Verb: "Light (builtin)", Run: func() { picked = "light" }},
			},
		},
		{ID: "other", Noun: "app", Verb: "help", Run: func() { picked = "help" }},
	}}
	p.Show()
	for _, r := range "theme" {
		p.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyRune, Rune: r, Press: true})
	}
	p.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	require.True(t, p.Picker.Open)
	assert.Equal(t, "Select Theme", p.Title)
	require.Len(t, p.Picker.Items, 2)
	assert.GreaterOrEqual(
		t,
		p.Picker.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode}).Children[0].Surface.Size.Width,
		56,
	)
	assert.Empty(t, p.Picker.Items[0].Leading)
	assert.True(t, p.Picker.Items[0].Current)
	assert.Equal(t, "Dark (builtin)", p.Picker.Items[0].Primary)

	p.Picker.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyEscape, Press: true})
	require.True(t, p.Picker.Open)
	assert.Equal(t, "Command Palette", p.Title)
	assert.Equal(t, "theme", p.Picker.Items[0].Primary)

	p.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	p.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	assert.Equal(t, "dark", picked)
	assert.False(t, p.Picker.Open)
}

func TestCommandPaletteDisabledRowDoesNotRun(t *testing.T) {
	ran := false
	p := &CommandPalette{Commands: []PaletteCommand{
		{ID: "disabled", Verb: "unavailable", Disabled: true, Run: func() { ran = true }},
	}}
	p.Show()
	p.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	assert.True(t, p.Picker.Open)
	assert.False(t, ran)
}

func TestCommandPaletteSaveUsesSharedConfirmation(t *testing.T) {
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
	ctx := &components.EventContext{}
	saveKey := xui.KeyEvent{Code: xui.KeyRune, Rune: 's', Mods: xui.ModCtrl, Press: true}
	draw := func(width int) string {
		t.Helper()
		s := p.Picker.Draw(components.DrawContext{
			Max: components.Size{Width: width, Height: 24}, Method: xui.WidthUnicode,
		})
		require.Len(t, s.Children, 1)
		return components.SurfaceText(s.Children[0].Surface)
	}
	text := draw(100)
	assert.Equal(t, 1, strings.Count(text, "ctrl+s save default"))
	p.Handle(ctx, saveKey)
	assert.True(t, p.Picker.IsConfirming())
	assert.Empty(t, saved, "Ctrl+S asks before saving")
	text = draw(100)
	assert.Contains(t, text, "Apply this model and save as global default?")
	assert.Contains(t, text, "y/enter save")
	assert.Contains(t, draw(32), "y save · n cancel")
	p.Handle(ctx, xui.PasteEvent{Text: "other"})
	assert.Equal(t, "target", p.Picker.Query(), "confirmation freezes the filter")
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEscape, Press: true})
	assert.False(t, p.Picker.IsConfirming())
	assert.True(t, p.Picker.Open)

	p.Handle(ctx, saveKey)
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'y', Press: true})
	assert.Equal(t, "target", saved)
	assert.True(t, p.Picker.Open, "failed saves leave the selection available for retry")
	success = true
	p.Handle(ctx, saveKey)
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	assert.False(t, p.Picker.Open)
}
