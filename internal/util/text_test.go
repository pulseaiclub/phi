package util

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestNormalizeLF(t *testing.T) {
	require.Equal(t, "a\nb\nc", NormalizeLF("a\r\nb\rc"))
	require.Equal(t, "already\nlf", NormalizeLF("already\nlf"))
	require.Empty(t, NormalizeLF(""))
}

func TestTruncate(t *testing.T) {
	for _, tc := range []struct {
		name, input, suffix string
		limit               int
		bytes, runes        string
	}{
		{"empty", "", "…", 0, "", ""},
		{"below", "abc", "…", 4, "abc", "abc"},
		{"exact", "abc", "…", 3, "abc", "abc"},
		{"above", "abcd", "…", 3, "abc…", "abc…"},
		{"zero", "abc", "…", 0, "…", "…"},
		{"no suffix", "abcd", "", 2, "ab", "ab"},
		{"custom suffix", "abcd", "\n(truncated)", 2, "ab\n(truncated)", "ab\n(truncated)"},
		{"unicode", "中文🙂好", "…", 3, "中…", "中文🙂…"},
		{"split bytes", "中文", "…", 1, "…", "中…"},
		{"invalid truncated", "a\xffbc", "…", 2, "a\xff…", "a�…"},
		{"invalid unchanged", "a\xff", "…", 2, "a\xff", "a\xff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.bytes, TruncateBytes(tc.input, tc.limit, tc.suffix))
			require.Equal(t, tc.runes, TruncateRunes(tc.input, tc.limit, tc.suffix))
		})
	}
	require.Panics(t, func() { _ = TruncateBytes("abc", -1, "…") })
	require.Panics(t, func() { _ = TruncateRunes("abc", -1, "…") })
}

func TestTruncateBytesKeepsValidUTF8(t *testing.T) {
	// The sessions picker truncates CJK previews by byte budget; a rune cut in
	// half used to leak orphan continuation bytes to the tty and desync the
	// terminal's parser (garbled glyph, drifted row, stale right edge).
	got := TruncateBytes(
		"示例 fake preview 文本：ASCII 与 中文 mixed，验证 UTF-8 截断边界保持合法",
		72,
		"…",
	)
	require.True(t, utf8.ValidString(got), "truncated preview must stay valid UTF-8: %q", got)
	require.LessOrEqual(t, len(got), 72+len("…"))
	require.Equal(t, "示例 fake preview 文本：ASCII 与 中文 mixed，验证 UTF-8 截…", got)
}
