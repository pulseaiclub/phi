package compaction

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/session"
)

func TestShouldCompact(t *testing.T) {
	settings := Settings{
		enabled:          true,
		reverseTokens:    10,
		keepRecentTokens: 20000,
	}

	// threshold = contextWindow - reverseTokens = 90
	require.False(t, ShouldCompact(50, 100, settings), "below threshold")
	require.False(t, ShouldCompact(90, 100, settings), "at threshold")
	require.True(t, ShouldCompact(95, 100, settings), "above threshold")

	disabled := settings
	disabled.enabled = false
	require.False(t, ShouldCompact(95, 100, disabled), "compaction disabled")

	require.False(t, ShouldCompact(95, 0, settings), "contextWindow <= 0")

	// threshold clamping when reverseTokens > contextWindow
	settings2 := settings
	settings2.reverseTokens = 200
	// threshold becomes 0
	require.False(t, ShouldCompact(0, 100, settings2), "threshold clamped, contextTokens==0")
	require.True(t, ShouldCompact(1, 100, settings2), "threshold clamped, contextTokens>0")
}

func TestEstimateContextTokens(t *testing.T) {
	t.Run("reported usage anchors the estimate", func(t *testing.T) {
		entries := []session.MessageEntry{
			msgEntry("u1", llm.RoleUser, 100, 0),
			msgEntry("a1", llm.RoleAssistant, 100, 30_000),
			msgEntry("t1", llm.RoleTool, 500, 0),
		}
		// The reported 30k covers the request that produced a1, so only the tool
		// result that followed it is added.
		assert.Equal(t, 30_500, EstimateContextTokens(entries))
	})

	t.Run("no reported usage estimates the whole path", func(t *testing.T) {
		entries := []session.MessageEntry{
			msgEntry("u1", llm.RoleUser, 100, 0),
			msgEntry("a1", llm.RoleAssistant, 200, 0),
		}
		assert.Equal(t, 300, EstimateContextTokens(entries))
	})

	t.Run("usage on a user message does not anchor", func(t *testing.T) {
		entries := []session.MessageEntry{
			msgEntry("u1", llm.RoleUser, 100, 40_000),
			msgEntry("a1", llm.RoleAssistant, 200, 0),
		}
		assert.Equal(t, 300, EstimateContextTokens(entries))
	})

	t.Run("non-message entries contribute nothing", func(t *testing.T) {
		entries := []session.MessageEntry{
			session.CompactionEntry{},
			msgEntry("a1", llm.RoleAssistant, 100, 20_000),
			msgEntry("t1", llm.RoleTool, 100, 0),
		}
		assert.Equal(t, 20_100, EstimateContextTokens(entries))
	})
}
