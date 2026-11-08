package components

import (
	"testing"

	"github.com/pulseaiclub/xui"

	"github.com/stretchr/testify/assert"
)

func TestTruncateToWidth(t *testing.T) {
	tests := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{"fits untouched", "abc", 3, "abc"},
		{"wider max returns the whole string", "héllo", 10, "héllo"},
		{"ascii truncated", "abcdef", 3, "abc"},
		{"non-ascii truncated on a grapheme boundary", "héllo", 3, "hél"},
		{"combining mark stays with its base", "e\u0301x", 1, "e\u0301"},
		{"wide glyphs count two columns", "中文abc", 4, "中文"},
		{"wide glyph does not straddle the limit", "中文abc", 3, "中"},
		{"max smaller than the first wide glyph", "中", 1, ""},
		{"empty input", "", 3, ""},
		{"zero max", "abc", 0, ""},
		{"negative max", "abc", -1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, TruncateToWidth(tt.s, tt.max, xui.WidthUnicode))
		})
	}
}

func TestTruncateToWidthSuffix(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		max    int
		suffix string
		want   string
	}{
		{"fits untouched", "abc", 5, "…", "abc"},
		{"exact fit untouched", "abc", 3, "…", "abc"},
		{"cut appends suffix inside max", "abcdef", 4, "…", "abc…"},
		{"empty suffix is a hard cut", "abcdef", 3, "", "abc"},
		{"wide glyphs count against the suffix", "中文abcdef", 7, "…", "中文ab…"},
		{"suffix too wide for max is dropped", "abcdef", 1, "……", "a"},
		{"zero max", "abc", 0, "…", ""},
		{"negative max", "abc", -1, "…", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateToWidthSuffix(tt.s, tt.max, tt.suffix, xui.WidthUnicode)
			assert.Equal(t, tt.want, got)
			assert.LessOrEqual(t, xui.StringWidth(got, xui.WidthUnicode), max(tt.max, 0))
		})
	}
}
