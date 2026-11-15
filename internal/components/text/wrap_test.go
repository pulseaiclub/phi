package text

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/xui"
)

func TestWrapEditorLines(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		width int
		want  []string
	}{
		{name: "empty text yields one empty line", text: "", width: 5, want: []string{""}},
		{name: "short line not wrapped", text: "hello", width: 10, want: []string{"hello"}},
		{name: "exact fit not wrapped", text: "abcde", width: 5, want: []string{"abcde"}},
		{name: "ascii wrap at width", text: "abcdefg", width: 3, want: []string{"abc", "def", "g"}},
		{name: "zero width clamps to 1", text: "ab", width: 0, want: []string{"a", "b"}},
		{name: "negative width clamps to 1", text: "ab", width: -3, want: []string{"a", "b"}},
		{name: "hard newlines preserved", text: "ab\ncd", width: 10, want: []string{"ab", "cd"}},
		{name: "blank lines preserved", text: "a\n\nb", width: 10, want: []string{"a", "", "b"}},
		{name: "trailing newline kept", text: "a\n", width: 10, want: []string{"a", ""}},
		{name: "leading newline kept", text: "\na", width: 10, want: []string{"", "a"}},
		{name: "cjk wraps at width", text: "你好世界", width: 4, want: []string{"你好", "世界"}},
		{name: "cjk odd width drops to one glyph per line", text: "你好世界", width: 3, want: []string{"你", "好", "世", "界"}},
		{name: "wide grapheme never split", text: "你", width: 1, want: []string{"你"}},
		{name: "mixed ascii and cjk", text: "a你b", width: 2, want: []string{"a", "你", "b"}},
		{name: "combining mark stays with base", text: "e\u0301xy", width: 1, want: []string{"e\u0301", "x", "y"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WrapEditorLines(tt.text, tt.width, xui.WidthUnicode)
			require.Equal(t, tt.want, got)
			assert.Equal(t, strings.ReplaceAll(tt.text, "\n", ""), strings.Join(got, ""),
				"wrapping must not drop or reorder characters")
		})
	}
}

func TestWrapEditorLines_WidthMethodsAgree(t *testing.T) {
	// Both width methods share the same approximation today; wrapping must not
	// silently diverge if one path special-cases the method.
	for _, text := range []string{"hello world", "你好世界", "a\nb\n"} {
		assert.Equal(t, WrapEditorLines(text, 3, xui.WidthUnicode), WrapEditorLines(text, 3, xui.WidthWCWidth))
	}
}

func TestCursorLineCol(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		cursor int
		width  int
		line   int
		col    int
	}{
		{name: "start of text", text: "hello", cursor: 0, width: 10, line: 0, col: 0},
		{name: "mid line", text: "hello", cursor: 3, width: 10, line: 0, col: 3},
		{name: "negative cursor clamps to 0", text: "hello", cursor: -5, width: 10, line: 0, col: 0},
		{name: "cursor past end clamps to len", text: "hello", cursor: 99, width: 10, line: 0, col: 5},
		{name: "after hard newline moves to next row", text: "ab\ncd", cursor: 3, width: 10, line: 1, col: 0},
		{name: "first char of second line", text: "ab\ncd", cursor: 4, width: 10, line: 1, col: 1},
		{name: "soft wrap moves to next visual row", text: "abcdef", cursor: 4, width: 3, line: 1, col: 1},
		{name: "end of full visual row spills to next", text: "abcdef", cursor: 3, width: 3, line: 1, col: 0},
		{name: "cjk column is display width", text: "你好", cursor: 3, width: 10, line: 0, col: 2},
		{name: "cjk at full-width row spills to next", text: "你好", cursor: 3, width: 2, line: 1, col: 0},
		{name: "empty text", text: "", cursor: 0, width: 5, line: 0, col: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line, col := CursorLineCol(tt.text, tt.cursor, tt.width, xui.WidthUnicode)
			assert.Equal(t, tt.line, line)
			assert.Equal(t, tt.col, col)
		})
	}
}

func TestSnapSurfaceColToGlyphStart(t *testing.T) {
	// Row layout for "你a" on a 3-wide row: 你 owns cols 0-1 (Width 2), the
	// continuation column stays an unpainted default pad (Width 0), a sits at col 2.
	row := []xui.Cell{
		{Char: "你", Width: 2},
		{Width: 0, Default: true},
		{Char: "a", Width: 1},
	}
	wideAfterNarrow := []xui.Cell{
		{Char: "a", Width: 1},
		{Char: "你", Width: 2},
		{Width: 0, Default: true},
	}
	// Row 0 uses the `row` layout, row 1 the wideAfterNarrow one, so the same
	// col snaps differently per row.
	twoRows := append(append([]xui.Cell{}, row...), wideAfterNarrow...)

	tests := []struct {
		name string
		buf  []xui.Cell
		rowW int
		col  int
		row  int
		want int
	}{
		{name: "nil buffer returns col", buf: nil, rowW: 3, col: 2, row: 0, want: 2},
		{name: "empty buffer returns col", buf: []xui.Cell{}, rowW: 3, col: 1, row: 0, want: 1},
		{name: "zero row width returns col", buf: row, rowW: 0, col: 2, row: 0, want: 2},
		{name: "negative col returns col", buf: row, rowW: 3, col: -1, row: 0, want: -1},
		{name: "negative row returns col", buf: row, rowW: 3, col: 1, row: -1, want: 1},
		{name: "row past buffer returns col", buf: row, rowW: 3, col: 1, row: 5, want: 1},
		{name: "trailing col of wide glyph snaps left", buf: row, rowW: 3, col: 1, row: 0, want: 0},
		{name: "leading col of wide glyph stays", buf: row, rowW: 3, col: 0, row: 0, want: 0},
		{name: "narrow glyph col stays", buf: row, rowW: 3, col: 2, row: 0, want: 2},
		{
			name: "wide glyph after narrow snaps to its own start",
			buf:  wideAfterNarrow,
			rowW: 3,
			col:  2,
			row:  0,
			want: 1,
		},
		// Row 1 is the wideAfterNarrow layout, so col 2 snaps to 1 there; on
		// row 0 it would stay 2. Returns 1 only if the row offset is applied.
		{name: "second row offset respected", buf: twoRows, rowW: 3, col: 2, row: 1, want: 1},
		{
			name: "truncated row stops at buffer end",
			buf:  []xui.Cell{{Char: "你", Width: 2}},
			rowW: 5,
			col:  4,
			row:  0,
			want: 4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SnapSurfaceColToGlyphStart(tt.buf, tt.rowW, tt.col, tt.row))
		})
	}
}
