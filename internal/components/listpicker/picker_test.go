package listpicker

import (
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
)

func TestPickerFilterAndAccept(t *testing.T) {
	var got string
	p := &Picker{
		Theme: components.DefaultTheme(),
		OnAccept: func(item Item) {
			got = item.ID
		},
	}
	p.Show([]Item{
		{ID: "aaaaaaaa-1111", Leading: "2m ago", Primary: "aaaaaaaa", Detail: "fix resume leak"},
		{ID: "bbbbbbbb-2222", Leading: "1h ago", Primary: "bbbbbbbb", Detail: "add themes", Badge: "current"},
		{ID: "cccccccc-3333", Leading: "2h ago", Primary: "cccccccc", Detail: "hello world"},
	}, ShowConfig{Title: "Sessions"})
	require.True(t, p.Open)
	assert.Equal(t, 1, p.Selected, "prefer badged row")

	ctx := &components.EventContext{}
	for _, r := range "resume" {
		p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: r, Press: true})
	}
	require.Len(t, p.filtered, 1)
	assert.Equal(t, "aaaaaaaa-1111", p.Items[p.filtered[0]].ID)

	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	assert.Equal(t, "aaaaaaaa-1111", got)
	assert.False(t, p.Open)
}

func TestPickerTabAccepts(t *testing.T) {
	var got string
	p := &Picker{
		Theme:    components.DefaultTheme(),
		OnAccept: func(item Item) { got = item.ID },
	}
	p.Show([]Item{{ID: "a", Primary: "a"}, {ID: "b", Primary: "b"}}, ShowConfig{})

	ctx := &components.EventContext{}
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyTab, Press: true})

	assert.Equal(t, "a", got, "Tab should pick the highlighted row")
	assert.Equal(t, 0, p.Selected, "Tab must not move the selection")
	assert.False(t, p.Open)
}

func TestPickerEscapeCloses(t *testing.T) {
	p := &Picker{Theme: components.DefaultTheme()}
	p.Show([]Item{{ID: "abc", Primary: "abc", Detail: "x"}}, ShowConfig{})
	ctx := &components.EventContext{}
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEscape, Press: true})
	assert.False(t, p.Open)
}

func TestPickerEmptyDraw(t *testing.T) {
	p := &Picker{Theme: components.DefaultTheme()}
	p.Show(nil, ShowConfig{Title: "Sessions", Empty: "No sessions in this directory"})
	s := p.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
	require.Len(t, s.Children, 1)
	text := components.SurfaceText(s.Children[0].Surface)
	assert.Contains(t, text, "Sessions")
	assert.Contains(t, text, "No sessions")
}

func TestPickerDrawBadge(t *testing.T) {
	p := &Picker{Theme: components.DefaultTheme()}
	p.Show([]Item{
		{ID: "deadbeef-0001", Leading: "2m ago", Primary: "deadbeef", Detail: "first", Badge: "current"},
	}, ShowConfig{Title: "Sessions"})
	s := p.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
	require.Len(t, s.Children, 1)
	text := components.SurfaceText(s.Children[0].Surface)
	assert.Contains(t, text, "Sessions")
	assert.Contains(t, text, "deadbeef")
	assert.Contains(t, text, "current")
}

func TestPickerDeleteConfirmFlow(t *testing.T) {
	var deleted []string
	p := &Picker{
		Theme: components.DefaultTheme(),
		OnDelete: func(item Item) {
			deleted = append(deleted, item.ID)
		},
	}
	p.Show([]Item{{ID: "a", Primary: "a"}, {ID: "b", Primary: "b"}}, ShowConfig{})
	ctx := &components.EventContext{}

	// First ctrl+x only arms; y confirms; the overlay stays open.
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'x', Mods: xui.ModCtrl, Press: true})
	assert.Empty(t, deleted)
	assert.True(t, p.Open)

	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'y', Press: true})
	assert.Equal(t, []string{"a"}, deleted)
	assert.True(t, p.Open, "delete keeps the picker open for further deletes")
}

func TestPickerDeleteCancelModes(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []xui.KeyEvent
	}{
		{"n", []xui.KeyEvent{{Code: xui.KeyRune, Rune: 'n', Press: true}}},
		{"esc", []xui.KeyEvent{{Code: xui.KeyEscape, Press: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var deleted int
			p := &Picker{
				Theme: components.DefaultTheme(),
				OnDelete: func(Item) {
					deleted++
				},
			}
			p.Show([]Item{{ID: "a", Primary: "a"}}, ShowConfig{})
			ctx := &components.EventContext{}

			p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'x', Mods: xui.ModCtrl, Press: true})
			for _, k := range tc.keys {
				p.Handle(ctx, k)
			}
			assert.Zero(t, deleted)

			// Disarmed: enter accepts instead of deleting.
			var accepted string
			p.OnAccept = func(item Item) { accepted = item.ID }
			p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
			assert.Equal(t, "a", accepted, "after cancel, enter accepts normally")
		})
	}
}

// While armed, every non y/n/esc key is swallowed: the selection cannot move
// and the filter cannot drift until the destructive question is answered.
func TestPickerConfirmSwallowsOtherKeys(t *testing.T) {
	var deleted int
	p := &Picker{
		Theme: components.DefaultTheme(),
		OnDelete: func(Item) {
			deleted++
		},
	}
	p.Show([]Item{{ID: "a", Primary: "a"}, {ID: "b", Primary: "b"}}, ShowConfig{})
	ctx := &components.EventContext{}

	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'x', Mods: xui.ModCtrl, Press: true})
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyDown, Press: true})
	for _, r := range "filter" {
		p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: r, Press: true})
	}
	assert.Zero(t, deleted)
	assert.Equal(t, 0, p.Selected)
	assert.Empty(t, p.Query)

	// Still armed: enter confirms rather than accepting the (unmoved) row.
	p.OnAccept = func(Item) { t.Error("accept must not fire while armed") }
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	assert.Equal(t, 1, deleted)
}

func TestPickerDeleteEscapesConfirmWithoutClosing(t *testing.T) {
	p := &Picker{Theme: components.DefaultTheme(), OnDelete: func(Item) {}}
	p.Show([]Item{{ID: "a", Primary: "a"}}, ShowConfig{})
	ctx := &components.EventContext{}

	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'x', Mods: xui.ModCtrl, Press: true})
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEscape, Press: true})
	assert.True(t, p.Open, "esc cancels the confirm, not the picker")

	// A second esc closes as usual.
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEscape, Press: true})
	assert.False(t, p.Open)
}

func TestPickerCtrlXWithoutOnDeleteStaysFilter(t *testing.T) {
	p := &Picker{Theme: components.DefaultTheme()}
	p.Show([]Item{{ID: "a", Primary: "a"}}, ShowConfig{})
	ctx := &components.EventContext{}

	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'x', Mods: xui.ModCtrl, Press: true})
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'y', Press: true})
	assert.Equal(t, "y", p.Query, "without OnDelete, y stays an ordinary filter rune")
}

func TestPickerSetItemsKeepsQueryAndClamps(t *testing.T) {
	p := &Picker{Theme: components.DefaultTheme()}
	p.Show([]Item{
		{ID: "a", Primary: "alpha"},
		{ID: "b", Primary: "beta"},
		{ID: "c", Primary: "gamma"},
	}, ShowConfig{})
	ctx := &components.EventContext{}
	for _, r := range "a" {
		p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: r, Press: true})
	}
	require.Equal(t, "a", p.Query)

	// Drop the matched row; the filter stays, selection clamps, Empty shows.
	// ("b b" carries no 'a', so the stale query matches nothing.)
	p.SetItems([]Item{{ID: "b", Primary: "b"}})
	assert.Equal(t, "a", p.Query)
	assert.Empty(t, p.filtered)
	assert.True(t, p.Open)

	// Clearing the filter by hand still works afterwards.
	p.Query = ""
	p.refilter()
	assert.Len(t, p.filtered, 1)
}

func TestPickerDrawConfirmChrome(t *testing.T) {
	p := &Picker{Theme: components.DefaultTheme(), OnDelete: func(Item) {}}
	p.Show([]Item{{ID: "a", Primary: "a"}}, ShowConfig{
		Title:          "Sessions",
		ConfirmMessage: "Delete this session?",
	})
	ctx := &components.EventContext{}
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'x', Mods: xui.ModCtrl, Press: true})

	s := p.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
	require.Len(t, s.Children, 1)
	text := components.SurfaceText(s.Children[0].Surface)
	assert.Contains(t, text, "Delete this session?")
	assert.Contains(t, text, "y delete")
	assert.Contains(t, text, "n cancel")
}
