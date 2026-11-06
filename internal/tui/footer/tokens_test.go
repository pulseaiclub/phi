package footer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/session"
)

func TestFormatContextLabel(t *testing.T) {
	u := session.TokenUsage{PromptTokens: 5120, TotalTokens: 6000}
	got := formatContextLabel(u, 128000)
	require.Equal(t, "4% of 128k", got)
	require.Empty(t, formatContextLabel(session.TokenUsage{}, 128000), "empty usage should hide label")
	require.Empty(t, formatContextLabel(u, 0), "zero window should hide label")
}

// A cache-heavy turn is mostly cache reads, so the fill must count the whole
// context (total, or the bucket sum) and not collapse to 0% of a 1M window.
func TestFormatContextLabelCountsCachedPrompt(t *testing.T) {
	// Gross prompt 35272 of which 33792 came from cache.
	u := session.TokenUsage{
		PromptTokens: 1480, CachedTokens: 33792, CompletionTokens: 1789, TotalTokens: 37061,
	}
	require.Equal(t, "3% of 1.0M", formatContextLabel(u, 1_000_000))

	// Same turn without a provider total: the buckets still size the window.
	noTotal := session.TokenUsage{PromptTokens: 1480, CachedTokens: 33792, CompletionTokens: 1789}
	require.Equal(t, "3% of 1.0M", formatContextLabel(noTotal, 1_000_000))
}

func TestFormatUsageStats(t *testing.T) {
	got := formatUsageStats(session.TokenUsage{
		PromptTokens:     1200,
		CompletionTokens: 800,
		TotalTokens:      2000,
	})
	require.Equal(t, "↑1.2k ↓800 Σ2.0k", got)

	// Cache traffic is disjoint from ↑: the buckets sum to the total.
	got = formatUsageStats(session.TokenUsage{
		PromptTokens:     1200,
		CompletionTokens: 800,
		CachedTokens:     900,
		CacheWriteTokens: 100,
		TotalTokens:      3000,
	})
	require.Equal(t, "↑1.2k ↓800 C900 W100 Σ3.0k", got)

	// No reported total: Σ falls back to the bucket sum.
	got = formatUsageStats(session.TokenUsage{
		PromptTokens: 1200, CompletionTokens: 800, CachedTokens: 900,
	})
	require.Equal(t, "↑1.2k ↓800 C900 Σ2.9k", got)
}

func TestJoinBorderParts(t *testing.T) {
	got := joinBorderParts(
		"↑1.2k ↓800 Σ2.0k",
		"context: 4% of 128k",
	)
	require.Equal(t, "↑1.2k ↓800 Σ2.0k context: 4% of 128k", got)
}

func TestJoinBorderPartsSecondEmpty(t *testing.T) {
	got := joinBorderParts("", "context: 4% of 128k")
	require.Equal(t, "context: 4% of 128k", got)
}

func TestJoinBorderPartsSecondBlank(t *testing.T) {
	got := joinBorderParts("↑1.2k", "")
	require.Equal(t, "↑1.2k", got)
}

func TestJoinBorderPartsBothEmpty(t *testing.T) {
	got := joinBorderParts("", "")
	require.Empty(t, got)
}

func TestTokenStatusLabelAmbient(t *testing.T) {
	th := components.DefaultTheme()
	u := session.TokenUsage{PromptTokens: 1200, CompletionTokens: 800, TotalTokens: 2000}
	label := tokenStatusLabel(th, u, 128000)
	assert.Equal(t, ChromeLabelStyle(th), label.Style)
	assert.Contains(t, label.Text, "↑1.2k")
	assert.Contains(t, label.Text, "% of 128k")
	assert.Empty(t, label.Spans)
}

func TestTokenStatusLabelPressureEscalatesContextOnly(t *testing.T) {
	th := components.DefaultTheme()
	// 95% of 100k → danger tier on default window thresholds.
	u := session.TokenUsage{PromptTokens: 95000, TotalTokens: 95000}
	label := tokenStatusLabel(th, u, 100000)
	require.NotEmpty(t, label.Spans)
	assert.Equal(t, ChromeLabelStyle(th), label.Spans[0].Style)
	assert.Equal(t, th.Destructive, label.Spans[1].Style)
	assert.Contains(t, label.Spans[1].Text, "%")
}
