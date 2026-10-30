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
