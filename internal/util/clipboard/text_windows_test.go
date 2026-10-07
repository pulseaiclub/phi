//go:build windows

package clipboard

import (
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/require"
)

func TestEncodeWindowsClipboardTextPreservesUnicode(t *testing.T) {
	want := "你好世界\n中文 + English\nemoji 😀🚀\né ñ ü\n多行\n文本"

	got, err := encodeWindowsClipboardText(want)
	require.NoError(t, err)
	require.NotEmpty(t, got)
	require.Equal(t, uint16(0), got[len(got)-1])
	require.Equal(t, want, string(utf16.Decode(got[:len(got)-1])))
}

// GlobalSize can report more than the string holds, so the reader must stop at
// the terminator instead of trusting the buffer length.
func TestUTF16StringZStopsAtTerminator(t *testing.T) {
	buf := append(utf16.Encode([]rune("你好世界")), 0, 'x')

	require.Equal(t, "你好世界", utf16StringZ(buf))
	require.Equal(t, "no terminator", utf16StringZ(utf16.Encode([]rune("no terminator"))))
	require.Empty(t, utf16StringZ(nil))
}
