package listpicker

import (
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
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
	typeQuery(p, ctx, "resume")
	require.Len(t, p.filtered, 1)
	assert.Equal(t, "aaaaaaaa-1111", p.Items[p.filtered[0].index].ID)

	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	assert.Equal(t, "aaaaaaaa-1111", got)
	assert.False(t, p.Open)
}

// The filter ranks by where the query landed: an exact name wins, then a name
// that starts with it, then a name that contains it, and only then a row that
// merely mentions it in the detail. Caller order decides inside a tier.
func TestPickerRanksFilterHits(t *testing.T) {
	tests := []struct {
		name  string
		query string
		items []Item
		want  []string
	}{
		{
			name:  "branch name beats commit subject",
			query: "origin/main",
			items: []Item{
				{ID: "merge", Primary: "fix/merge-guard", Detail: "merge origin/main into guard"},
				{ID: "maintenance", Primary: "origin/maintenance", Detail: "wip"},
				{ID: "main", Primary: "origin/main", Detail: "chore: bump"},
				{ID: "dev", Primary: "origin/develop"},
			},
			want: []string{"origin/main", "origin/maintenance", "fix/merge-guard"},
		},
		{
			name:  "prefix beats contains",
			query: "origin/m",
			items: []Item{
				{ID: "feat", Primary: "feature/origin/m"},
				{ID: "main", Primary: "origin/main"},
			},
			want: []string{"origin/main", "feature/origin/m"},
		},
		{
			name:  "equal tiers keep caller order",
			query: "wip",
			items: []Item{
				{ID: "a", Primary: "a", Detail: "wip one"},
				{ID: "b", Primary: "b", Detail: "wip two"},
			},
			want: []string{"a", "b"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &Picker{Theme: components.DefaultTheme()}
			p.Show(tc.items, ShowConfig{})
			ctx := &components.EventContext{}
			typeQuery(p, ctx, tc.query)
			require.Equal(t, tc.query, p.Query())

			assert.Equal(t, tc.want, p.primaryOrder())
			assert.Equal(t, 0, p.Selected, "the best match sits under the cursor")
		})
	}
}

// primaryOrder is the visible order of the rows the filter kept.
func (p *Picker) primaryOrder() []string {
	rows := p.rows()
	out := make([]string, 0, len(p.filtered))
	for _, h := range p.filtered {
		out = append(out, rows[h.index].Primary)
	}
	return out
}

// typeQuery feeds text through the picker one rune at a time, like a keyboard.
func typeQuery(p *Picker, ctx *components.EventContext, text string) {
	for _, r := range text {
		p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: r, Press: true})
	}
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

	rows := strings.Split(text, "\n")
	require.GreaterOrEqual(t, len(rows), 4)
	assert.NotContains(t, rows[1], "─", "the search row stays separate from the divider")
	assert.Contains(t, rows[2], "─", "the divider sits below the search row")
	assert.Contains(t, rows[3], "deadbeef", "the list starts below the divider")
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
	assert.Empty(t, p.Query())

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
	assert.Equal(t, "y", p.Query(), "without OnDelete, y stays an ordinary filter rune")
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
	require.Equal(t, "a", p.Query())

	// Drop the matched row; the filter stays, selection clamps, Empty shows.
	// ("b b" carries no 'a', so the stale query matches nothing.)
	p.SetItems([]Item{{ID: "b", Primary: "b"}})
	assert.Equal(t, "a", p.Query())
	assert.Empty(t, p.filtered)
	assert.True(t, p.Open)

	// Clearing the filter by hand still works afterwards.
	p.SetQuery("")
	p.refilter()
	assert.Len(t, p.filtered, 1)
}

// The filter line carries commands too: `!name` swaps the rows for the caller's
// commands, and Enter runs the highlighted one instead of accepting a row.
func TestPickerCommandRunsAndCloses(t *testing.T) {
	ran, args := "", ""
	p := &Picker{
		Theme: components.DefaultTheme(),
		Commands: []Command{
			{Name: "fetch", Detail: "fetch remotes", Run: func(a string) { ran, args = "fetch", a }},
			{Name: "commit", Detail: "commit work", Run: func(string) { ran = "commit" }},
		},
		OnAccept: func(item Item) { t.Errorf("command line must not accept row %q", item.ID) },
	}
	p.Show([]Item{{ID: "alpha", Primary: "alpha"}}, ShowConfig{})
	ctx := &components.EventContext{}

	typeQuery(p, ctx, "!co")
	assert.True(t, p.commandMode)
	assert.Equal(t, []string{"!commit"}, p.primaryOrder())
	assert.Equal(t, 0, p.Selected, "the best command sits under the cursor")

	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	assert.Equal(t, "commit", ran)
	assert.Empty(t, args)
	assert.False(t, p.Open, "a command closes the overlay unless it asks to stay")
}

// Text after the name is an operand, not a filter: `!fetch origin` must keep
// offering fetch, and hand "origin" to Run.
func TestPickerCommandKeepsOperandOutOfFilter(t *testing.T) {
	got := ""
	p := &Picker{Theme: components.DefaultTheme()}
	p.Commands = []Command{{Name: "fetch", Run: func(a string) { got = a }}}
	p.Show([]Item{{ID: "alpha", Primary: "alpha"}}, ShowConfig{})
	ctx := &components.EventContext{}

	typeQuery(p, ctx, "!fetch origin")
	assert.Equal(t, []string{"!fetch"}, p.primaryOrder(), "the operand must not filter the name away")
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	assert.Equal(t, "origin", got)
}

// A command that refreshes the rows keeps the overlay up and hands the filter
// line back, so the picker stays usable after one command.
func TestPickerCommandKeepOpenReturnsToRows(t *testing.T) {
	ran := false
	p := &Picker{Theme: components.DefaultTheme()}
	p.Commands = []Command{{
		Name:     "fetch",
		KeepOpen: true,
		Run: func(string) {
			ran = true
			p.SetItems([]Item{{ID: "beta", Primary: "beta"}})
		},
	}}
	p.Show([]Item{{ID: "alpha", Primary: "alpha"}}, ShowConfig{})
	ctx := &components.EventContext{}

	typeQuery(p, ctx, "!fetch")
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})

	assert.True(t, ran)
	assert.True(t, p.Open, "KeepOpen keeps the overlay up")
	assert.False(t, p.commandMode)
	assert.Empty(t, p.Query(), "the line is consumed, not left as a filter")
	assert.Equal(t, []string{"beta"}, p.primaryOrder())
}

// No commands configured means no command line: `!` stays an ordinary rune, so
// domains that never opt in keep their old filter behavior.
func TestPickerCommandPrefixWithoutCommandsStaysFilter(t *testing.T) {
	p := &Picker{Theme: components.DefaultTheme()}
	p.Show([]Item{
		{ID: "x", Primary: "!weird"},
		{ID: "y", Primary: "other"},
	}, ShowConfig{})
	ctx := &components.EventContext{}

	typeQuery(p, ctx, "!we")
	assert.False(t, p.commandMode)
	assert.Equal(t, []string{"!weird"}, p.primaryOrder())
}

// Configuring commands must not disturb the plain path: text filters rows, the
// hit accepts, and no command runs.
func TestPickerCommandsLeaveRowAcceptAlone(t *testing.T) {
	var got string
	ran := false
	p := &Picker{
		Theme:    components.DefaultTheme(),
		OnAccept: func(item Item) { got = item.ID },
	}
	p.Commands = []Command{{Name: "fetch", Run: func(string) { ran = true }}}
	p.Show([]Item{{ID: "alpha", Primary: "alpha"}, {ID: "beta", Primary: "beta"}}, ShowConfig{})
	ctx := &components.EventContext{}

	typeQuery(p, ctx, "al")
	assert.False(t, p.commandMode)

	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	assert.Equal(t, "alpha", got)
	assert.False(t, ran)
	assert.False(t, p.Open)
}

// The selected row in command mode is a command, not a caller row: Ctrl+X must
// not arm a delete over it.
func TestPickerCommandIgnoresDeleteChord(t *testing.T) {
	deleted := 0
	p := &Picker{Theme: components.DefaultTheme(), OnDelete: func(Item) { deleted++ }}
	p.Commands = []Command{{Name: "fetch"}}
	p.Show([]Item{{ID: "alpha", Primary: "alpha"}}, ShowConfig{})
	ctx := &components.EventContext{}

	typeQuery(p, ctx, "!f")
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'x', Mods: xui.ModCtrl, Press: true})
	assert.False(t, p.confirming)

	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	assert.Zero(t, deleted)
	assert.False(t, p.Open)
}

// Chrome: the item list advertises the command line, and the command list wears
// its own title, hint, and empty copy.
func TestPickerCommandDrawChrome(t *testing.T) {
	p := &Picker{Theme: components.DefaultTheme()}
	p.Commands = []Command{
		{Name: "fetch", Detail: "fetch remotes"},
		{Name: "commit", Detail: "commit work"},
		{Name: "checkout", Detail: "switch branch"},
	}
	p.Show([]Item{{ID: "alpha", Primary: "alpha"}}, ShowConfig{Title: "Sessions"})
	ctx := &components.EventContext{}

	draw := func() string {
		s := p.Draw(components.DrawContext{
			Max:    components.Size{Width: 80, Height: 24},
			Method: xui.WidthUnicode,
		})
		require.Len(t, s.Children, 1)
		return components.SurfaceText(s.Children[0].Surface)
	}

	text := draw()
	assert.Contains(t, text, "Sessions")
	assert.Contains(t, text, CommandPrefix+" commands", "the row list advertises the line")

	typeQuery(p, ctx, "!fetch")
	text = draw()
	assert.Contains(t, text, "Commands")
	assert.Contains(t, text, "fetch remotes")
	assert.Contains(t, text, "⏎ run")

	p.SetQuery(CommandPrefix + "checkout")
	p.refilter()
	text = draw()

	assert.Contains(t, text, "!checkout", "a name wider than the item column is not cut")
	assert.Contains(t, text, "switch branch")

	p.SetQuery(CommandPrefix + "zzz")
	p.refilter()

	assert.Contains(t, draw(), "No command matches", "the empty copy is command-specific")
}

// A caller that set its own Hint keeps it: the command advert does not step on
// copy the domain already chose.
func TestPickerCommandAdvertRespectsHint(t *testing.T) {
	p := &Picker{Theme: components.DefaultTheme(), Commands: []Command{{Name: "fetch"}}}
	p.Show(nil, ShowConfig{Hint: " ↑↓ move" + chrome.Sep + "⏎ select"})

	s := p.Draw(components.DrawContext{Max: components.Size{Width: 100, Height: 24}, Method: xui.WidthUnicode})
	require.Len(t, s.Children, 1)
	text := components.SurfaceText(s.Children[0].Surface)
	assert.Contains(t, text, "⏎ select")
	assert.NotContains(t, text, CommandPrefix+" commands")
}

// drawRowLines renders the panel and returns its line buffer, one string per row.
func drawRowLines(t *testing.T, p *Picker) []string {
	t.Helper()
	s := p.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
	require.Len(t, s.Children, 1)
	return strings.Split(strings.TrimSuffix(components.SurfaceText(s.Children[0].Surface), "\n"), "\n")
}

// Rows with no leading content must not pay the leading gutter: skills carry
// no marker, and a blank margin ahead of every name is dead space. Lists that
// do use the column keep it, blanks included, so a branch ● stays aligned
// with the unmarked rows around it.
func TestPickerDrawLeadingColumnFollowsContent(t *testing.T) {
	t.Run("collapses when no row leads", func(t *testing.T) {
		p := &Picker{Theme: components.DefaultTheme()}
		p.Show([]Item{{ID: "a", Primary: "alpha", Detail: "does a"}}, ShowConfig{Title: "Skills"})
		lines := drawRowLines(t, p)
		assert.True(t, strings.HasPrefix(lines[3], "│ alpha"),
			"first row starts at the left padding, got %q", lines[3])
	})

	t.Run("keeps the column when a row leads", func(t *testing.T) {
		p := &Picker{Theme: components.DefaultTheme()}
		p.Show([]Item{
			{ID: "a", Leading: "2m ago", Primary: "alpha", Detail: "does a"},
			{ID: "b", Primary: "beta", Detail: "does b"},
		}, ShowConfig{Title: "Sessions"})
		lines := drawRowLines(t, p)
		assert.True(t, strings.HasPrefix(lines[3], "│ 2m ago    alpha"),
			"led row keeps the fixed column, got %q", lines[3])
		assert.True(t, strings.HasPrefix(lines[4], "│           beta"),
			"unled row keeps the same column, got %q", lines[4])
	})
}

// A detail wider than the remaining columns elides with a visible ellipsis
// instead of ending mid-word like a rendering bug.
func TestPickerDrawElidesLongDetail(t *testing.T) {
	p := &Picker{Theme: components.DefaultTheme(), Width: 40}
	detail := "Conducts multi-axis code review before merging any change"
	p.Show([]Item{{ID: "a", Primary: "alpha", Detail: detail}}, ShowConfig{Title: "Skills"})
	lines := drawRowLines(t, p)

	assert.Contains(t, lines[3], "…", "the cut detail carries an ellipsis")
	assert.NotContains(t, lines[3], "any change", "the cut tail is gone")
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
	assert.Contains(t, text, "y/enter delete")
	assert.Contains(t, text, "n/esc cancel")
}
