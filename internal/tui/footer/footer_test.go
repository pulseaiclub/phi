package footer

import (
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/layout"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

type stubComposer struct {
	label layout.BorderLabel
	set   bool
}

func (s *stubComposer) SetBottomLeftLabel(label layout.BorderLabel) {
	s.label = label
	s.set = true
}

func (s *stubComposer) ClearBottomLeftLabel() {
	s.label = layout.BorderLabel{}
	s.set = false
}

func TestDrawCombinesParts(t *testing.T) {
	f := NewFooterChrome(components.DefaultTheme(), 0)
	f.SetExtensionStatus(" review ")
	f.SetLiveJobs(func() int { return 2 })
	f.Activity().Apply(controller.ActivityCancelled)

	row := components.SurfaceText(f.Draw(components.DrawContext{Method: xui.WidthUnicode}, 40))
	assert.Contains(t, row, "review · 2 jobs")
	assert.NotContains(t, row, "Stopped")
}

func TestDrawSingleJobSingular(t *testing.T) {
	f := NewFooterChrome(components.DefaultTheme(), 0)
	f.SetLiveJobs(func() int { return 1 })

	row := components.SurfaceText(f.Draw(components.DrawContext{Method: xui.WidthUnicode}, 40))
	assert.Contains(t, row, "1 job")
}

func TestDrawEmptyFooter(t *testing.T) {
	f := NewFooterChrome(components.DefaultTheme(), 0)

	row := components.SurfaceText(f.Draw(components.DrawContext{Method: xui.WidthUnicode}, 40))
	assert.Empty(t, strings.TrimSpace(row))
}

func TestUpdateTokenDisplayZeroClearsLabel(t *testing.T) {
	f := NewFooterChrome(components.DefaultTheme(), 128000)
	comp := &stubComposer{}
	f.BindComposer(comp)

	f.UpdateTokenDisplay(session.TokenUsage{PromptTokens: 1200, TotalTokens: 1200})
	require.True(t, comp.set)

	// A session switch to one without usage must drop the stale counts.
	f.UpdateTokenDisplay(session.TokenUsage{})
	assert.False(t, comp.set)
	assert.Empty(t, comp.label.Text)
}

func TestClearTokenDisplayDropsLabel(t *testing.T) {
	f := NewFooterChrome(components.DefaultTheme(), 128000)
	comp := &stubComposer{}
	f.BindComposer(comp)

	f.UpdateTokenDisplay(session.TokenUsage{PromptTokens: 1200, TotalTokens: 1200})
	require.True(t, comp.set)

	f.ClearTokenDisplay()
	assert.False(t, comp.set)
}

// A model switch changes the window the fill label is measured against; the
// footer starts with the startup model's window, so it must be replaceable.
func TestSetContextWindowRefreshesLabel(t *testing.T) {
	f := NewFooterChrome(components.DefaultTheme(), 192000)
	comp := &stubComposer{}
	f.BindComposer(comp)
	f.UpdateTokenDisplay(session.TokenUsage{PromptTokens: 50000, TotalTokens: 50000})
	require.True(t, comp.set)
	assert.Contains(t, comp.label.Text, "%/192k")

	f.SetContextWindow(1_000_000)
	assert.Contains(t, comp.label.Text, "%/1.0M")

	f.SetContextWindow(0)
	assert.NotContains(t, comp.label.Text, "%", "unknown window drops the fill fragment")
	assert.Contains(t, comp.label.Text, "↑50k", "usage stats survive a window reset")
}

func TestStatusSlotSwapsActivityAndTokens(t *testing.T) {
	f := NewFooterChrome(components.DefaultTheme(), 128000)
	comp := &stubComposer{}
	f.BindComposer(comp)

	f.UpdateTokenDisplay(session.TokenUsage{
		PromptTokens:     1200,
		CompletionTokens: 800,
		TotalTokens:      2000,
	})
	require.True(t, comp.set)
	assert.Contains(t, comp.label.Text, "↑1.2k")
	assert.Contains(t, comp.label.Text, "↓800")

	f.Activity().Apply(controller.ActivityStreaming)
	require.True(t, comp.set)
	assert.Empty(t, comp.label.Text)
	require.NotEmpty(t, comp.label.Spans)
	var joined strings.Builder
	for _, sp := range comp.label.Spans {
		joined.WriteString(sp.Text)
	}
	assert.Equal(t, "Generating…", joined.String())
	lit := 0
	fg := components.DefaultTheme().Foreground
	for _, sp := range comp.label.Spans {
		if sp.Style == fg {
			lit++
		}
	}
	assert.Positive(t, lit)

	f.Activity().Apply(controller.ActivityIdle)
	assert.Contains(t, comp.label.Text, "↑1.2k")
	assert.Empty(t, comp.label.Spans)
}
