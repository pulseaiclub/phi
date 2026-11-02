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
