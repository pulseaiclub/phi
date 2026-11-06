package footer

import (
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/status"
	"github.com/pulseaiclub/phi/internal/session"
)

func TestDrawExtensionStatus(t *testing.T) {
	f := NewFooterChrome(components.DefaultTheme())
	row := components.SurfaceText(f.Draw(components.DrawContext{Method: xui.WidthUnicode}, 40, State{
		ExtensionStatus: " review ",
	}))
	assert.Contains(t, row, "review")
	assert.NotContains(t, row, "job")
	assert.NotContains(t, row, "Stopped")
}

func TestDrawEmptyFooter(t *testing.T) {
	f := NewFooterChrome(components.DefaultTheme())

	row := components.SurfaceText(f.Draw(components.DrawContext{Method: xui.WidthUnicode}, 40, State{}))
	assert.Empty(t, strings.TrimSpace(row))
}

func TestStatusLabelUsesSnapshotUsage(t *testing.T) {
	th := components.DefaultTheme()
	label := StatusLabel(th, StatusInput{
		Usage:         session.TokenUsage{PromptTokens: 1200, CompletionTokens: 800, TotalTokens: 2000},
		ContextWindow: 128000,
	})
	assert.Contains(t, label.Text, "↑1.2k")
	assert.Contains(t, label.Text, "↓800")

	cleared := StatusLabel(th, StatusInput{ContextWindow: 128000})
	assert.False(t, cleared.Visible())
}

func TestStatusLabelSwapsActivityAndTokens(t *testing.T) {
	th := components.DefaultTheme()
	spin := status.NewSpinner(th.ToolName)
	label := StatusLabel(th, StatusInput{
		Activity:      "Generating…",
		ShowSpinner:   true,
		Spinner:       spin,
		Usage:         session.TokenUsage{PromptTokens: 1200, CompletionTokens: 800, TotalTokens: 2000},
		ContextWindow: 128000,
	})
	assert.Empty(t, label.Text)
	require.NotEmpty(t, label.Spans)
	var joined strings.Builder
	for _, sp := range label.Spans {
		joined.WriteString(sp.Text)
	}
	assert.Equal(t, "Generating…", joined.String())

	idle := StatusLabel(th, StatusInput{
		Usage:         session.TokenUsage{PromptTokens: 1200, CompletionTokens: 800, TotalTokens: 2000},
		ContextWindow: 128000,
	})
	assert.Contains(t, idle.Text, "↑1.2k")
	assert.Empty(t, idle.Spans)
}
