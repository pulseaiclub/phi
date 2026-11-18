package text

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
)

func TestHighlightShell(t *testing.T) {
	th := components.DefaultTheme()
	base := th.Foreground

	spans := HighlightShell("echo hello", base, th)
	require.NotEmpty(t, spans)
	assert.Equal(t, "echo hello", spansText(spans), "text must survive tokenisation")

	// chroma's bash lexer colors a leading "echo" as a function-name token,
	// which chromaStyle maps onto Accent.
	assert.Equal(t, "echo", spans[0].Text)
	assert.Equal(t, th.Accent.Fg, spans[0].Style.Fg,
		"named token should carry a token color, not plain foreground")
}

func TestHighlightShellKeepsBaseFlags(t *testing.T) {
	th := components.DefaultTheme()
	cancelled := th.Foreground
	cancelled.Strikethrough = true

	spans := HighlightShell("echo hello", cancelled, th)
	require.NotEmpty(t, spans)
	for _, s := range spans {
		if s.Text == "echo" {
			assert.True(t, s.Style.Strikethrough, "colored tokens keep base emphasis flags")
		} else {
			assert.True(t, s.Style.Equal(cancelled), "plain runs keep base verbatim")
		}
	}
}

func TestHighlightShellMultilineAndEmpty(t *testing.T) {
	th := components.DefaultTheme()

	multi := HighlightShell("if true; then\n  echo hi\nfi", th.Foreground, th)
	require.NotEmpty(t, multi)
	assert.Equal(t, "if true; then\n  echo hi\nfi", spansText(multi))

	assert.Nil(t, HighlightShell("", th.Foreground, th))
	assert.Nil(t, HighlightShell("   ", th.Foreground, th))
}
