package listpicker

import (
	"slices"
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
		{ID: "aaaaaaaa-1111", Primary: "aaaaaaaa", Detail: "fix resume leak"},
		{ID: "bbbbbbbb-2222", Primary: "bbbbbbbb", Detail: "add themes"},
		{ID: "cccccccc-3333", Primary: "cccccccc", Detail: "hello world"},
	}, ShowConfig{Title: "Sessions"})
	require.True(t, p.Open)
	assert.Zero(t, p.Selected)

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

func TestPickerDrawTwoColumns(t *testing.T) {
	p := &Picker{Theme: components.DefaultTheme()}
	p.Show([]Item{
		{ID: "deadbeef-0001", Primary: "deadbeef", Detail: "first"},
	}, ShowConfig{Title: "Sessions"})
	s := p.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
	require.Len(t, s.Children, 1)
	text := components.SurfaceText(s.Children[0].Surface)
	assert.Contains(t, text, "Sessions")
	assert.Contains(t, text, "deadbeef")

	rows := strings.Split(text, "\n")
	require.GreaterOrEqual(t, len(rows), 5)
	assert.Contains(t, rows[1], "Sessions", "the title has its own row")
	assert.NotContains(t, rows[2], "─", "the search row has no divider")
	assert.Empty(t, strings.Trim(rows[3], "│ "), "the list breathes below the search row")
	assert.Contains(t, rows[4], "deadbeef", "the list starts below the gap")
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

func TestPickerColumnsAlignAcrossRowsAndFiltering(t *testing.T) {
	p := &Picker{}
	items := []Item{
		{ID: "license", Primary: "LICENSE", Detail: "."},
		{ID: "readme", Primary: "README.md", Detail: "docs"},
		{ID: "image", Leading: "●", Primary: "图标.png", Detail: "assets"},
	}
	for range 10 {
		items = append(items, Item{Primary: "short", Detail: "."})
	}
	// The widest row is off-screen; scrolling or filtering must not move the columns.
	items = append(items, Item{Primary: "abcdefghijklmnopqrst", Detail: "description"})
	p.Show(items, ShowConfig{Title: "Files"})
	draw := func() components.Surface {
		t.Helper()
		s := p.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
		require.Len(t, s.Children, 1)
		return s.Children[0].Surface
	}
	panel := draw()
	for i, first := range []string{"L", "R", "图"} {
		row := panel.Buffer[(4+i)*panel.Size.Width : (5+i)*panel.Size.Width]
		require.Equal(t, first, row[4].Char, "names share one left edge")
		assert.Equal(t, " ", row[3].Char)
		assert.True(t, row[4].Style.Bold)
		assert.Equal(t, " ", row[24].Char)
		assert.Equal(t, " ", row[25].Char, "column boxes are separated by two cells")
		assert.Equal(t, []string{".", "d", "a"}[i], row[26].Char, "details share one left edge")
	}
	assert.Equal(t, "●", panel.Buffer[6*panel.Size.Width+2].Char)
	for x := 1; x < panel.Size.Width-1; x++ {
		assert.Equal(t, components.DefaultTheme().SelectionBg.Bg, panel.Buffer[4*panel.Size.Width+x].Style.Bg)
	}

	typeQuery(p, &components.EventContext{}, "license")
	require.Len(t, p.filtered, 1)
	panel = draw()
	assert.Equal(t, "L", panel.Buffer[4*panel.Size.Width+4].Char)
	assert.Equal(t, ".", panel.Buffer[4*panel.Size.Width+26].Char)

	p.Commands = []Command{{Name: "fetch", Detail: "description"}, {Name: "pull", Detail: "sync"}}
	p.Show(nil, ShowConfig{})
	typeQuery(p, &components.EventContext{}, "!")
	panel = draw()
	for i, name := range []string{"!fetch", "!pull"} {
		row := panel.Buffer[(4+i)*panel.Size.Width : (5+i)*panel.Size.Width]
		for x, char := range name {
			assert.Equal(t, string(char), row[4+x].Char)
		}
		assert.Equal(t, " ", row[10].Char)
		assert.Equal(t, " ", row[11].Char)
		assert.Equal(t, []string{"d", "s"}[i], row[12].Char)
	}
}

func TestPickerUsesColorForCurrentChoice(t *testing.T) {
	p := &Picker{Theme: components.DefaultTheme()}
	p.Show([]Item{{Primary: "current", Current: true}, {Primary: "other"}}, ShowConfig{})
	p.Selected = 1
	s := p.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
	require.Len(t, s.Children, 1)
	panel := s.Children[0].Surface
	row := panel.Buffer[4*panel.Size.Width : 5*panel.Size.Width]
	x := slices.IndexFunc(row, func(cell xui.Cell) bool { return cell.Char == "c" })
	require.NotEqual(t, -1, x)
	assert.Equal(t, components.DefaultTheme().Success.Fg, row[x].Style.Fg)
	assert.NotContains(t, components.SurfaceText(panel), "●")

	// Selecting the current row keeps the hue on the selection bar, so the
	// branch you are on never reads as just another choice.
	p.Selected = 0
	s = p.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
	panel = s.Children[0].Surface
	row = panel.Buffer[4*panel.Size.Width : 5*panel.Size.Width]
	x = slices.IndexFunc(row, func(cell xui.Cell) bool { return cell.Char == "c" })
	require.NotEqual(t, -1, x)
	assert.Equal(t, components.DefaultTheme().Success.Fg, row[x].Style.Fg)
	assert.Equal(t, components.DefaultTheme().SelectionBg.Bg, row[x].Style.Bg)
}

func TestPickerTabNavigatesOnlyWhenConfigured(t *testing.T) {
	items := []Item{{ID: "a", Primary: "a"}, {ID: "b", Primary: "b"}}
	ctx := &components.EventContext{}
	tab := xui.KeyEvent{Code: xui.KeyTab, Press: true}

	p := &Picker{}
	var got string
	p.OnAccept = func(item Item) { got = item.ID }
	p.Show(items, ShowConfig{})
	p.Handle(ctx, tab)
	assert.Equal(t, "a", got, "default Tab accepts the highlighted row")
	assert.False(t, p.Open)

	got = ""
	p.Show(items, ShowConfig{TabNavigates: true})
	p.Handle(ctx, tab)
	assert.Equal(t, 1, p.Selected, "configured Tab moves the highlight")
	p.Handle(ctx, tab)
	assert.Equal(t, 1, p.Selected, "Tab clamps at the last row")
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyTab, Mods: xui.ModShift, Press: true})
	assert.Equal(t, 0, p.Selected, "Shift+Tab moves back up")
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyTab, Mods: xui.ModShift, Press: true})
	assert.Equal(t, 0, p.Selected, "Shift+Tab clamps at the first row")
	assert.True(t, p.Open, "navigation keys never close the picker")
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	assert.Equal(t, "a", got, "Enter still fires the highlighted row")
}

func TestPickerCentersSingleColumnPages(t *testing.T) {
	p := &Picker{}
	p.Show([]Item{
		{ID: "short", Primary: "short"},
		{ID: "long", Primary: "a much longer model name"},
	}, ShowConfig{})
	s := p.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
	require.Len(t, s.Children, 1)
	panel := s.Children[0].Surface

	const start = (72 - len("a much longer model name")) / 2
	assert.Equal(t, "s", panel.Buffer[4*panel.Size.Width+start].Char)
	assert.Equal(t, "a", panel.Buffer[5*panel.Size.Width+start].Char)
	assert.NotContains(t, components.SurfaceText(panel), "—")

	typeQuery(p, &components.EventContext{}, "short")
	panel = p.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode}).Children[0].Surface
	assert.Equal(t, "s", panel.Buffer[4*panel.Size.Width+start].Char, "filtering does not move the column")
	assert.Equal(t, 7, panel.Size.Height, "one result does not reserve empty list rows")
}

func TestPickerFrameTracksResultCount(t *testing.T) {
	p := &Picker{Commands: []Command{{Name: "fetch", Detail: "fetch remotes"}}}
	items := []Item{{ID: "target", Primary: "target"}}
	for range 20 {
		items = append(items, Item{ID: "other", Primary: "other"})
	}
	p.Show(items, ShowConfig{})
	ctx := &components.EventContext{}
	// Explicit heights: deriving them from p.maxItems() would follow whatever
	// the cap is, and the frame would never contradict the code it mirrors.
	check := func(height int) {
		t.Helper()
		s := p.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
		require.Len(t, s.Children, 1)
		assert.Equal(t, components.Size{Width: 72, Height: height}, s.Children[0].Surface.Size)
		assert.Equal(t, components.Point{X: 4, Y: (24 - height) / 3}, s.Children[0].Origin)
	}
	check(18) // 21 items capped at twelve visible rows
	typeQuery(p, ctx, "target")
	require.Len(t, p.filtered, 1)
	check(7)
	typeQuery(p, ctx, "missing")
	require.Empty(t, p.filtered)
	check(7)
	p.Show(nil, ShowConfig{Title: "Empty"})
	check(7)
	typeQuery(p, ctx, "!")
	require.True(t, p.commandMode)
	check(7)
}

func TestPickerFitsSmallTerminal(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
		panelHeight   int
	}{
		{"narrow", 32, 24, 7},
		{"short", 80, 12, 7},
		{"minimum full header", 80, 7, 7},
		{"compact", 80, 6, 6},
		{"no content space", 80, 3, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Picker{}
			p.Show([]Item{{Leading: "●", Primary: strings.Repeat("宽", 40), Detail: "description"}},
				ShowConfig{Title: strings.Repeat("标题", 30)})
			s := p.Draw(components.DrawContext{
				Max: components.Size{Width: tc.width, Height: tc.height}, Method: xui.WidthUnicode,
			})
			require.Len(t, s.Children, 1)
			child := s.Children[0]
			panel := child.Surface
			assert.Equal(t, tc.panelHeight, panel.Size.Height)
			assert.GreaterOrEqual(t, child.Origin.Y, 0)
			assert.LessOrEqual(t, child.Origin.Y+panel.Size.Height, tc.height)
			assert.LessOrEqual(t, child.Origin.X+panel.Size.Width, tc.width)
			for y := 1; y < panel.Size.Height-1; y++ {
				assert.Equal(t, "│", panel.Buffer[y*panel.Size.Width+panel.Size.Width-1].Char,
					"right border at row %d must survive long content", y)
			}
			if tc.height >= 4 {
				text := components.SurfaceText(panel)
				assert.Contains(t, text, "宽")
				assert.Contains(t, text, "descr", "the second column remains visible")
				require.NotNil(t, panel.Cursor)
				assert.Less(t, panel.Cursor.Y, panel.Size.Height-1)
			}
		})
	}
}

// A detail wider than the remaining columns elides with a visible ellipsis
// instead of ending mid-word like a rendering bug.
func TestPickerDrawElidesLongDetail(t *testing.T) {
	p := &Picker{Theme: components.DefaultTheme(), Width: 40}
	detail := "Conducts multi-axis code review before merging any change"
	p.Show([]Item{{ID: "a", Primary: "alpha", Detail: detail}}, ShowConfig{Title: "Skills"})
	lines := drawRowLines(t, p)

	assert.Contains(t, lines[4], "…", "the cut detail carries an ellipsis")
	assert.NotContains(t, lines[4], "any change", "the cut tail is gone")
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

// Subsequence matching is opt-in: the palette lost "thm" → theme when it
// moved onto the shared picker, and this pins the return.
func TestPickerFuzzyMatchesNameSubsequences(t *testing.T) {
	items := []Item{
		{ID: "theme-dark", Primary: "Dark (builtin)", Keywords: "settings theme dark"},
		{ID: "help", Primary: "help", Keywords: "app help"},
	}
	ctx := &components.EventContext{}
	search := func(q string) []string {
		p := &Picker{}
		p.Show(items, ShowConfig{Fuzzy: true})
		for _, r := range q {
			p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: r, Press: true})
		}
		var got []string
		for _, h := range p.filtered {
			got = append(got, p.rows()[h.index].ID)
		}
		return got
	}

	assert.Contains(t, search("thm"), "theme-dark", "subsequence finds the row")
	assert.NotContains(t, search("thm"), "help", "unrelated rows stay out")
	assert.Empty(t, search("zzz"), "no subsequence, no hit")

	p := &Picker{}
	p.Show(items, ShowConfig{})
	for _, r := range "thm" {
		p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: r, Press: true})
	}
	assert.Empty(t, p.filtered, "without Fuzzy the exact tiers are the whole story")
}

// A confirmation names one row. If SetItems swaps the list while it is
// pending, Enter must not save or delete whichever row now sits there.
func TestConfirmDoesNotFireOnASwappedRow(t *testing.T) {
	ctx := &components.EventContext{}
	ctrlS := xui.KeyEvent{Code: xui.KeyRune, Rune: 's', Mods: xui.ModCtrl, Press: true}
	enter := xui.KeyEvent{Code: xui.KeyEnter, Press: true}

	var saved []string
	p := &Picker{}
	p.OnSave = func(item Item) bool { saved = append(saved, item.ID); return true }
	p.Show([]Item{{ID: "a", Primary: "a"}, {ID: "b", Primary: "b"}}, ShowConfig{})
	p.Handle(ctx, ctrlS)
	require.True(t, p.IsConfirming())

	p.SetItems([]Item{{ID: "z", Primary: "z"}, {ID: "b", Primary: "b"}})
	p.Handle(ctx, enter)
	assert.Empty(t, saved, "a swapped row is not the row the user confirmed")
	assert.False(t, p.IsConfirming(), "the mismatch disarms instead of guessing")
	assert.True(t, p.Open)

	// The same row surviving a refresh still saves.
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyDown, Press: true}) // onto "b"
	p.Handle(ctx, ctrlS)
	require.True(t, p.IsConfirming())
	p.SetItems([]Item{{ID: "z", Primary: "z"}, {ID: "b", Primary: "b"}})
	p.Handle(ctx, enter)
	assert.Equal(t, []string{"b"}, saved, "a refresh that keeps the row must not block the save")
	assert.False(t, p.Open, "a successful save closes")

	// A CanSave flip between arming and Enter stops the save.
	p.Show([]Item{{ID: "b", Primary: "b"}}, ShowConfig{})
	allow := true
	p.CanSave = func(Item) bool { return allow }
	p.Handle(ctx, ctrlS)
	require.True(t, p.IsConfirming())
	allow = false
	p.Handle(ctx, enter)
	assert.Len(t, saved, 1, "CanSave is rechecked when the confirmation fires")
	assert.False(t, p.IsConfirming())
}
