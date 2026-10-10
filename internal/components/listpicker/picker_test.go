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

// OnClose means "went down without a choice": a caller waiting on it must not
// read an accepted row as a dismissal.
func TestPickerCloseReportsOnlyDismissals(t *testing.T) {
	var accepted []string
	dismissed := 0
	p := &Picker{
		Theme:    components.DefaultTheme(),
		OnAccept: func(item Item) { accepted = append(accepted, item.ID) },
		OnClose:  func() { dismissed++ },
	}
	ctx := &components.EventContext{}

	p.Show([]Item{{ID: "a", Primary: "a"}}, ShowConfig{})
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	assert.Equal(t, []string{"a"}, accepted)
	assert.Zero(t, dismissed)

	p.Show([]Item{{ID: "b", Primary: "b"}}, ShowConfig{})
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEscape, Press: true})
	assert.Equal(t, []string{"a"}, accepted)
	assert.Equal(t, 1, dismissed)

	p.Hide()
	assert.Equal(t, 1, dismissed, "hiding a closed picker reports nothing")
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
