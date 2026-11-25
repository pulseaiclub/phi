package block_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/block"
)

func TestCompactionBlockShowsTokensBefore(t *testing.T) {
	b := &block.CompactionBlock{Theme: components.DefaultTheme(), TokensBefore: 15000}
	got := components.SurfaceText(b.Draw(components.DrawContext{Max: components.Size{Width: 60}}))
	require.Contains(t, got, "Compacted from 15k tokens")
}

// An unknown count (older entries, or a compaction that never reported usage)
// keeps the plain marker instead of printing "from 0 tokens".
func TestCompactionBlockWithoutTokens(t *testing.T) {
	b := &block.CompactionBlock{Theme: components.DefaultTheme()}
	got := components.SurfaceText(b.Draw(components.DrawContext{Max: components.Size{Width: 60}}))
	require.Contains(t, got, "Compacted")
	require.NotContains(t, got, "tokens")
}

// The compaction marker hides the one thing a user needs after a cut: the
// summary that replaced the summarized history.
func TestCompactionBlockRevealsSummary(t *testing.T) {
	b := &block.CompactionBlock{
		TokensBefore: 43000,
		Summary:      "## Goal\nship it",
		Theme:        components.DefaultTheme(),
	}
	dctx := components.DrawContext{Max: components.Size{Width: 60, Height: 10}}

	collapsed := components.SurfaceText(b.Draw(dctx))
	require.Contains(t, collapsed, "Compacted from 43k tokens")
	require.NotContains(t, collapsed, "ship it")

	b.Expanded = true
	expanded := components.SurfaceText(b.Draw(dctx))
	require.Contains(t, expanded, "ship it")

	require.Equal(t, "Compacted from 43k tokens\n## Goal\nship it", b.CopyText())
}

func TestCompactionBlockWithoutSummaryIsNotInteractive(t *testing.T) {
	b := &block.CompactionBlock{Theme: components.DefaultTheme()}
	drawn := components.SurfaceText(b.Draw(components.DrawContext{Max: components.Size{Width: 60}}))
	require.NotContains(t, drawn, "▶")
	require.False(t, b.ClickAt(2, 0), "no summary means nothing to reveal")
	require.Equal(t, "Compacted", b.CopyText())
}
