package compaction

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pulseaiclub/phi/internal/core"
	"github.com/pulseaiclub/phi/internal/session"
)

func TestEstimateMessageTokens(t *testing.T) {
	tests := []struct {
		name string
		msg  core.Message
		want int
	}{
		{"empty message", core.Message{}, 0},
		{"rounds up to 4 chars per token", core.Message{Role: core.RoleUser, Content: "abcde"}, 2},
		{"tool result content counts", core.Message{Role: core.RoleTool, Content: sizedContent(50)}, 50},
		{"reasoning counts", core.Message{Role: core.RoleAssistant, ReasoningContent: strings.Repeat("t", 400)}, 100},
		{
			"tool call name and arguments count",
			core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
				{Function: core.Function{Name: "read", Arguments: `{"path":"a.go"}`}},
			}},
			5, // 4 + 16 chars
		},
		{
			"images use a fixed stand-in",
			core.Message{Role: core.RoleUser, Images: []core.Image{{Data: "x", MimeType: "image/png"}}},
			estimatedImageChars / 4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, estimateMessageTokens(tt.msg))
		})
	}
}

func TestFindCutIndex_ExceedsTokensChoosesNearestCutPoint(t *testing.T) {
	entries := []session.MessageEntry{
		msgEntry("e1", core.RoleUser, 10, 0),
		msgEntry("e2", core.RoleUser, 10, 0),
		session.CompactionEntry{}, // non-message, contributes no size
		msgEntry("e3", core.RoleUser, 10, 0),
		msgEntry("e4", core.RoleUser, 10, 0),
	}
	startIndex := 0
	endIndex := len(entries)
	keepRecentTokens := 15
	cutPoints := []int{1, 3, 4}

	cutIndex := findCutIndex(entries, startIndex, endIndex, keepRecentTokens, cutPoints)

	// The last two messages cost 20 >= 15, so the budget is reached at e3.
	assert.Equal(t, 3, cutIndex)
}

func TestFindCutIndex_NotExceedTokensReturnsFirstCutPoint(t *testing.T) {
	entries := []session.MessageEntry{
		msgEntry("e1", core.RoleUser, 10, 0),
		msgEntry("e2", core.RoleUser, 10, 0),
		msgEntry("e3", core.RoleUser, 10, 0),
	}
	startIndex := 0
	endIndex := len(entries)
	keepRecentTokens := 200
	cutPoints := []int{0, 1, 2}

	cutIndex := findCutIndex(entries, startIndex, endIndex, keepRecentTokens, cutPoints)

	assert.Equal(t, cutPoints[0], cutIndex)
}

func TestFindCutIndex_BudgetReachedExactlyCutsThere(t *testing.T) {
	entries := []session.MessageEntry{
		msgEntry("e1", core.RoleUser, 10, 0),
		msgEntry("e2", core.RoleAssistant, 20, 0),
		msgEntry("e3", core.RoleUser, 10, 0),
	}

	cutIndex := findCutIndex(entries, 0, len(entries), 30, []int{0, 1, 2})

	// 10 + 20 == 30 exactly: the budget is reached at e2 (>= comparison).
	// A strict > would keep walking and cut at e1 instead.
	assert.Equal(t, 1, cutIndex)
}

// The provider reports the size of the whole context on every assistant
// message, and only assistant messages carry it. Summing that instead of
// per-message sizes tripped keepRecentTokens on the newest message, so the cut
// collapsed to the last entry and the budget never bound anything.
func TestFindCutIndex_IgnoresCumulativeUsage(t *testing.T) {
	const messages = 30
	entries := make([]session.MessageEntry, 0, messages)
	cutPoints := make([]int, 0, messages)
	for i := range messages {
		role := core.RoleUser
		usage := 0
		if i%2 == 1 {
			role = core.RoleAssistant
			usage = 70000 + i // cumulative context, as real sessions report it
		}
		entries = append(entries, msgEntry(fmt.Sprintf("e%d", i), role, 100, usage))
		cutPoints = append(cutPoints, i)
	}

	// 100 tokens per message and a 1000-token budget keep the last 10 messages.
	cutIndex := findCutIndex(entries, 0, len(entries), 1000, cutPoints)

	assert.Equal(t, 20, cutIndex)
}
