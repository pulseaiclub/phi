package block

import (
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/status"
)

func TestShellHighlightMemoizes(t *testing.T) {
	th := components.DefaultTheme()
	var h shellHighlight

	first := h.spans("echo hi", th.Foreground, th)
	require.NotEmpty(t, first)
	assert.Same(t, &first[0], &h.spans("echo hi", th.Foreground, th)[0], "repeat call must hit the memo")

	// A different base style is a different key.
	again := h.spans("echo hi", th.Foreground, components.DarculaTheme())
	assert.NotSame(t, &first[0], &again[0])

	// Overflow resets wholesale instead of growing without bound.
	for i := range shellHighlightLimit + 1 {
		h.spans(string(rune('a'+i%26))+"-"+string(rune(i)), th.Foreground, th)
	}
	assert.LessOrEqual(t, len(h.cache), shellHighlightLimit)
}

func TestShellHighlightSpansPreserveText(t *testing.T) {
	th := components.DefaultTheme()
	assert.Nil(t, (&shellHighlight{}).spans("", th.Foreground, th))

	spans := (&shellHighlight{}).spans("ls -la", th.Foreground, th)
	require.NotEmpty(t, spans)
	assert.Equal(t, "ls -la", spansTextBlock(spans))
}

func TestBashBlockTitleHighlightsCommand(t *testing.T) {
	th := components.DefaultTheme()
	b := &BashBlock{Command: "echo hi", Status: status.ToolDone, Theme: th}

	title := b.titleSpans(th)
	require.NotEmpty(t, title)
	assert.Equal(t, "$ ", title[0].Text)
	require.Equal(t, "echo hi", spansTextBlock(title[1:]))

	var colored bool
	for _, s := range title[1:] {
		if s.Text == "echo" && !s.Style.Fg.Equal(th.Foreground.Fg) {
			colored = true
		}
	}
	assert.True(t, colored, "command should carry token colors, not plain foreground")
}

func TestAgentChildBashRowHighlightsCommand(t *testing.T) {
	th := components.DefaultTheme()
	a := &AgentBlock{
		Name:     "agent_spawn",
		Status:   status.ToolDone,
		Children: []ChildTool{{Name: "bash", Detail: "echo hi", Status: status.ToolDone}},
		Theme:    th,
	}
	lines := a.childToolLines(a.Children[0], "  ", "", th, 60, xui.WidthUnicode)
	require.NotEmpty(t, lines)
	// Prefix spans stay intact; command spans follow them per grapheme.
	require.Equal(t, "  ✓ bash echo hi", spansTextBlock(lines[0]))

	var colored bool
	for _, s := range lines[0][4:] {
		if !s.Style.Fg.Equal(th.Foreground.Fg) {
			colored = true
		}
	}
	assert.True(t, colored, "child bash command should carry token colors")
}

// spansTextBlock joins span text; mirrors text.spansText without the import.
func spansTextBlock(spans []components.Span) string {
	out := ""
	for _, s := range spans {
		out += s.Text
	}
	return out
}
