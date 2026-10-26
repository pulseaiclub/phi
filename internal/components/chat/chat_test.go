package chat

import (
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/layout"
	"github.com/pulseaiclub/phi/internal/components/text"
	imgutil "github.com/pulseaiclub/phi/internal/util/image"
)

func TestChatInputBorderLabels(t *testing.T) {
	c := &ChatInput{
		MinBodyRows: 3,
		TopLeftLabel: layout.BorderLabel{
			Text:  "nostromo—1—skill",
			Style: xui.Style{Fg: xui.RGBColor(0x5f, 0xc2, 0xc2)},
		},
		TopRightLabel: layout.BorderLabel{
			Text:  "high",
			Style: xui.Style{Fg: xui.IndexedColor(250)},
		},
		BottomLeftLabel: layout.BorderLabel{
			Text:  "↑1.2k ↓800 Σ2.0k 5% of 128k",
			Style: xui.Style{Fg: xui.RGBColor(0x7d, 0xc3, 0xff)},
		},
		BottomRightLabel: layout.BorderLabel{
			Text:  "~/Desktop/../examples/hello",
			Style: xui.Style{Fg: xui.IndexedColor(250)},
		},
		UseBlockCursor: true,
	}
	s := c.Draw(components.DrawContext{Max: components.Size{Width: 60, Height: 10}})
	assert.Equal(t, 60, s.Size.Width)
	assert.Equal(t, 5, s.Size.Height)
	// Corners
	assert.Equal(t, "╭", s.Buffer[0].Char)
	assert.Equal(t, "╮", s.Buffer[59].Char)
	bottom := 4 * 60
	assert.Equal(t, "╰", s.Buffer[bottom].Char)
	assert.Equal(t, "╯", s.Buffer[bottom+59].Char)
	top := rowString(s, 0)
	assert.Contains(t, top, "nostromo")
	assert.Contains(t, top, "high")
	bot := rowString(s, 4)
	assert.Contains(t, bot, "5% of 128k")
	assert.Contains(t, bot, "examples")
	require.NotNil(t, s.Cursor, "expected cursor")
}

func TestChatInputTyping(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3}
	ctx := &components.EventContext{}
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'h', Press: true})
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'i', Press: true})
	assert.Equal(t, "hi", c.Value)
	assert.Equal(t, 2, c.Cursor)
	submitted := ""
	c.OnSubmit = func(s string) { submitted = s }
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	assert.Equal(t, "hi", submitted)
}

func TestChatInputCtrlUClearsAll(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3, Value: "hello\nworld", Cursor: 5}
	ctx := &components.EventContext{}
	changed := 0
	c.OnChange = func(string) { changed++ }
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'u', Mods: xui.ModCtrl, Press: true})
	require.Empty(t, c.Value)
	require.Zero(t, c.Cursor)
	require.Equal(t, 1, changed)
	require.True(t, ctx.Consume)
	// A plain 'u' still types.
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'u', Press: true})
	require.Equal(t, "u", c.Value)
}

func TestChatInputCtrlUEmptyDoesNotNotify(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3}
	ctx := &components.EventContext{}
	changed := 0
	c.OnChange = func(string) { changed++ }
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'u', Mods: xui.ModCtrl, Press: true})
	require.Zero(t, changed)
	require.True(t, ctx.Consume)
}

func TestChatInputCtrlUClearsPendingAttachments(t *testing.T) {
	c := &ChatInput{
		MinBodyRows:   3,
		Value:         "hello",
		Cursor:        5,
		PendingSkills: []string{"building-plugins"},
		PendingImages: []imgutil.Attachment{
			{Label: "a.png", Result: imgutil.Result{Data: []byte("x"), MimeType: "image/png"}},
		},
	}
	ctx := &components.EventContext{}
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'u', Mods: xui.ModCtrl, Press: true})
	require.Empty(t, c.Value)
	require.Zero(t, c.Cursor)
	require.Empty(t, c.PendingSkills)
	require.Empty(t, c.PendingImages)
	require.True(t, ctx.Consume)
}

func TestChatInputCtrlAEJumpLineBounds(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3, Value: "hello world", Cursor: 5}
	ctx := &components.EventContext{}
	// Ctrl+A jumps to the start of the current line.
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'a', Mods: xui.ModCtrl, Press: true})
	require.Equal(t, 0, c.Cursor)
	// Ctrl+E jumps to the end of the current line.
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'e', Mods: xui.ModCtrl, Press: true})
	require.Equal(t, len("hello world"), c.Cursor)
	require.True(t, ctx.Consume)
}

func TestChatInputCtrlAEStayOnCurrentLine(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3, Value: "alpha\nbeta gamma", Cursor: len("alpha\nbe")}
	ctx := &components.EventContext{}
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'a', Mods: xui.ModCtrl, Press: true})
	require.Equal(t, len("alpha\n"), c.Cursor)
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'e', Mods: xui.ModCtrl, Press: true})
	require.Equal(t, len("alpha\nbeta gamma"), c.Cursor)
}

func TestChatInputMentionOpenDefersNav(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3, Value: "@a\nb", Cursor: 2, MentionOpen: true}
	ctx := &components.EventContext{}
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyDown, Press: true})
	require.False(t, ctx.Consume, "Down should bubble when MentionOpen")
	require.Equal(t, 2, c.Cursor, "cursor should stay put")
	submitted := false
	c.OnSubmit = func(string) { submitted = true }
	ctx = &components.EventContext{}
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	require.False(t, ctx.Consume, "Enter should bubble to picker when MentionOpen")
	require.False(t, submitted, "Enter should bubble to picker when MentionOpen")
}

func TestChatInputQuestionPicker(t *testing.T) {
	active := false
	c := &ChatInput{MinBodyRows: 3, MaxBodyRows: 8}
	c.OnQuestionChange = func(ok bool, _ string) { active = ok }
	ctx := &components.EventContext{}
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: '?', Press: true})
	require.True(t, active, "expected question picker active after typing ?")
	require.Equal(t, "?", c.Value)
	ctx = &components.EventContext{}
	c.QuestionOpen = true
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	require.False(t, ctx.Consume, "Enter should bubble to picker when QuestionOpen")
}

func TestChatInputNewlineModifiers(t *testing.T) {
	for _, mods := range []xui.Modifiers{xui.ModShift, xui.ModAlt, xui.ModCtrl} {
		c := &ChatInput{MinBodyRows: 3, MaxBodyRows: 8}
		ctx := &components.EventContext{}
		c.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: 'a', Press: true})
		submitted := false
		c.OnSubmit = func(string) { submitted = true }
		c.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Mods: mods, Press: true})
		require.False(t, submitted, "mods=%v should insert newline, not submit", mods)
		require.Equal(t, "a\n", c.Value, "mods=%v", mods)
	}
}

func TestChatInputGrowsUntilMax(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3, MaxBodyRows: 5, PaddingX: 1}
	method := xui.WidthUnicode
	w := 40
	require.Equal(t, 5, c.PreferredHeight(w, method), "empty preferred height")
	c.Value = "one\ntwo\nthree\nfour"
	require.Equal(t, 6, c.PreferredHeight(w, method), "4 lines preferred height")
	c.Value = "one\ntwo\nthree\nfour\nfive\nsix\nseven"
	require.Equal(t, 7, c.PreferredHeight(w, method), "over max preferred height (max body 5 + borders)")
	s := c.Draw(components.DrawContext{Max: components.Size{Width: w, Height: 20}, Method: method})
	require.Equal(t, 7, s.Size.Height, "draw height")
}

func TestChatInputPasteMultilineDoesNotSubmit(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3, MaxBodyRows: 8}
	submitted := false
	c.OnSubmit = func(string) { submitted = true }
	ctx := &components.EventContext{}
	c.Handle(ctx, xui.PasteEvent{Text: "a\nb\nc"})
	require.False(t, submitted, "paste must not submit")
	require.Equal(t, "a\nb\nc", c.Value)
	require.GreaterOrEqual(t, c.PreferredHeight(40, xui.WidthUnicode), 5, "expected grow after paste")
}

func TestChatInputCJKPasteNoContinuationReverse(t *testing.T) {
	c := &ChatInput{
		MinBodyRows:    3,
		MaxBodyRows:    8,
		PaddingX:       1,
		UseBlockCursor: true,
		CursorStyle:    xui.Style{Reverse: true},
	}
	ctx := &components.EventContext{}
	c.Handle(ctx, xui.PasteEvent{Text: "已修复中文粘贴"})
	s := c.Draw(components.DrawContext{Max: components.Size{Width: 40, Height: 10}, Method: xui.WidthUnicode})
	require.NotNil(t, s.Cursor, "expected cursor")
	cy := s.Cursor.Y
	revPrimaries := 0
	for x := 0; x < s.Size.Width; {
		cell := s.Buffer[cy*s.Size.Width+x]
		step := int(cell.Width)
		step = max(step, 1)
		if xui.StringWidth(cell.Char, xui.WidthUnicode) == 2 && cell.Width != 2 {
			require.FailNowf(t, "CJK width mismatch", "CJK %q stored with width %d at col %d", cell.Char, cell.Width, x)
		}
		if cell.Style.Reverse {
			revPrimaries++
		}
		x += step
	}
	require.Equal(t, 1, revPrimaries, "expected 1 reverse primary (block cursor)")
}

func TestCursorLineColFullWidthWrap(t *testing.T) {
	// 5 CJK chars → width 10; cursor at end of exactly-full line must wrap.
	sample := "一二三四五"
	line, col := text.CursorLineCol(sample, len(sample), 10, xui.WidthUnicode)
	require.Equal(t, 1, line)
	require.Zero(t, col)
}

func TestSnapSurfaceColToGlyphStart(t *testing.T) {
	s := components.NewSurface(6, 1, nil)
	s.SetCell(0, 0, xui.Cell{Char: "中", Width: 2})
	s.SetCell(2, 0, xui.Cell{Char: "文", Width: 2})
	require.Equal(t, 0, text.SnapSurfaceColToGlyphStart(s.Buffer, 6, 1, 0), "snap col 1")
	require.Equal(t, 2, text.SnapSurfaceColToGlyphStart(s.Buffer, 6, 3, 0), "snap col 3")
}

func TestSanitizeComposerTextDropsControls(t *testing.T) {
	in := "a\tb\r\nc\x00e\rd\n"
	got := sanitizeComposerText(in)
	want := "a    b\nce\nd\n"
	require.Equal(t, want, got)
	require.Equal(t, "hello", sanitizeComposerText("▎hello"), "chrome strip")
}

func TestChatInputPendingSkills(t *testing.T) {
	c := &ChatInput{
		MinBodyRows:   3,
		PendingSkills: []string{"building-plugins"},
		Theme:         components.DefaultTheme(),
	}
	method := xui.WidthUnicode
	require.Equal(t, 6, c.PreferredHeight(60, method), "preferred height with pending skill")
	s := c.Draw(components.DrawContext{Max: components.Size{Width: 60, Height: 10}, Method: method})
	require.Equal(t, 6, s.Size.Height, "draw height")
	require.Equal(t, "╭", s.Buffer[0].Char, "skills must be inside the border")
	inner := rowString(s, 1)
	require.Contains(t, inner, "Skills:")
	require.Contains(t, inner, "building-plugins")
	underlined := false
	row := 1 * s.Size.Width
	for x := 0; x < s.Size.Width; x++ {
		if s.Buffer[row+x].Style.Underline {
			underlined = true
			break
		}
	}
	require.True(t, underlined, "expected underlined skill name")
	// Cursor sits on the editor line below the skills chip.
	require.NotNil(t, s.Cursor, "expected cursor below skills")
	require.Equal(t, 2, s.Cursor.Y, "cursor below skills")

	// Backspace on empty input pops the pending skill.
	ctx := &components.EventContext{}
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyBackspace, Press: true})
	require.Empty(t, c.PendingSkills, "expected pending skills cleared")
	require.Equal(t, 5, c.PreferredHeight(60, method), "preferred height after clear")
}

func TestChatInputAddPendingSkillDedup(t *testing.T) {
	c := &ChatInput{}
	c.AddPendingSkill("building-plugins")
	c.AddPendingSkill("building-plugins")
	c.AddPendingSkill("example-skill")
	require.Len(t, c.PendingSkills, 2)
	require.True(t, c.PopPendingSkill())
	require.Equal(t, "building-plugins", c.PendingSkills[0])
}

func rowString(s components.Surface, y int) string {
	var b strings.Builder
	for x := 0; x < s.Size.Width; x++ {
		ch := s.Buffer[y*s.Size.Width+x].Char
		if ch == "" {
			ch = " "
		}
		b.WriteString(ch)
	}
	return b.String()
}

func TestChatInputCJKBlockCursorKeepsWidth(t *testing.T) {
	c := &ChatInput{
		MinBodyRows:    3,
		PaddingX:       1,
		Value:          "中",
		Cursor:         0,
		UseBlockCursor: true,
		CursorStyle:    xui.Style{Reverse: true},
	}
	s := c.Draw(components.DrawContext{Max: components.Size{Width: 40, Height: 10}, Method: xui.WidthUnicode})
	require.NotNil(t, s.Cursor, "expected cursor")
	cx, cy := s.Cursor.X, s.Cursor.Y
	cell := s.Buffer[cy*s.Size.Width+cx]
	require.Equal(t, "中", cell.Char)
	require.EqualValues(t, 2, cell.Width)
}

func TestCursorAfterCJKPasteAtTextEnd(t *testing.T) {
	sample := "13个技能 你把这个 skills挪动过去"
	c := &ChatInput{MinBodyRows: 3, PaddingX: 1, UseBlockCursor: false}
	c.Handle(&components.EventContext{}, xui.PasteEvent{Text: sample})
	w := 80
	s := c.Draw(components.DrawContext{Max: components.Size{Width: w, Height: 10}, Method: xui.WidthUnicode})
	require.NotNil(t, s.Cursor, "nil cursor")
	cy := s.Cursor.Y
	var lastContentEnd int
	for x := 0; x < w; {
		cell := s.Buffer[cy*w+x]
		step := int(cell.Width)
		step = max(step, 1)
		if !cell.Trail && cell.Char != "" && cell.Char != " " && cell.Char != "│" {
			lastContentEnd = x + step
		}
		x += step
	}
	require.Equal(t, lastContentEnd, s.Cursor.X, "cursorX")
	// Insertion caret must not sit on the last CJK primary (IME would overlay it).
	cell := s.Buffer[cy*w+s.Cursor.X]
	require.NotEqual(t, 2, xui.StringWidth(cell.Char, xui.WidthUnicode), "cursor on wide glyph %q", cell.Char)
}

func TestChatInputPendingImagesHeight(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3, MaxBodyRows: 8}
	base := c.PreferredHeight(40, xui.WidthUnicode)
	c.PendingImages = []imgutil.Attachment{
		{Label: "a.png", Result: imgutil.Result{Data: []byte("x"), MimeType: "image/png"}},
	}
	require.Equal(t, base+1, c.PreferredHeight(40, xui.WidthUnicode))
}

func TestChatInputBackspacePopsPendingImage(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3}
	c.PendingImages = []imgutil.Attachment{
		{Label: "a.png", Result: imgutil.Result{Data: []byte("x"), MimeType: "image/png"}},
	}
	ctx := &components.EventContext{}
	c.Handle(ctx, xui.KeyEvent{Press: true, Code: xui.KeyBackspace})
	require.Empty(t, c.PendingImages)
}

// The ghost hint draws after the cursor as the untyped rest of a suggestion,
// in the dim ghost style.
func TestDrawPaintsGhostTextAfterTheCursor(t *testing.T) {
	c := &ChatInput{
		MinBodyRows: 3,
		PaddingX:    1,
		Value:       "git sta",
		Cursor:      len("git sta"),
		GhostText:   "tus ",
	}
	s := c.Draw(components.DrawContext{Max: components.Size{Width: 40, Height: 10}, Method: xui.WidthUnicode})

	assert.Contains(t, rowString(s, 1), "git status ", "the hint completes the typed text inline")
	ghostCol := 1 + c.PaddingX + len("git sta")
	assert.Equal(t, xui.IndexedColor(244), s.Buffer[1*s.Size.Width+ghostCol].Style.Fg, "the hint is dim gray")
}

// The hint truncates at the border instead of wrapping onto its own line,
// where it would read as typed text.
func TestDrawTruncatesGhostTextAtTheBorder(t *testing.T) {
	c := &ChatInput{
		MinBodyRows: 3,
		PaddingX:    1,
		Value:       "git sta",
		Cursor:      len("git sta"),
		GhostText:   "tus ",
	}
	s := c.Draw(components.DrawContext{Max: components.Size{Width: 12, Height: 10}, Method: xui.WidthUnicode})

	assert.Contains(t, rowString(s, 1), "git stat", "the hint fills the remaining columns")
	assert.NotContains(t, rowString(s, 1), "git status", "the hint stops at the border")
}

// Mid-text the cursor has characters after it already, so a hint there would
// cover them; it is only drawn while the cursor ends the text.
func TestDrawSkipsGhostTextWhenTheCursorIsMidText(t *testing.T) {
	c := &ChatInput{
		MinBodyRows: 3,
		PaddingX:    1,
		Value:       "git status",
		Cursor:      len("git sta"),
		GhostText:   "xyz",
	}
	s := c.Draw(components.DrawContext{Max: components.Size{Width: 40, Height: 10}, Method: xui.WidthUnicode})

	assert.Contains(t, rowString(s, 1), "git status")
	assert.NotContains(t, rowString(s, 1), "xyz", "no hint over text the user already has")
}
