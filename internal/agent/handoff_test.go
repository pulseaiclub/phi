package agent

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/session"
)

func TestBuildHandoffProjectsTextAndKeepsSnapshot(t *testing.T) {
	s, err := NewSession()
	require.NoError(t, err)
	private := &llm.ProviderState{Model: "private-model-state"}
	require.NoError(t, s.Append(
		llm.Message{Role: llm.RoleUser, Content: "Keep error responses unchanged."},
		llm.Message{
			Role:             llm.RoleAssistant,
			Content:          "Reuse the counter.",
			ReasoningContent: "private reasoning",
			ProviderState:    private,
			ToolCalls: []llm.ToolCall{
				{ID: "read-1", Function: llm.Function{Name: "read", Arguments: "private args"}},
			},
		},
		llm.Message{Role: llm.RoleTool, ToolCallID: "read-1", Content: "Allow(key string) bool"},
	))
	before := s.BuildContext()
	got := s.BuildHandoff()
	assert.Equal(t, job.ContextModeHandoff, got.Mode)
	assert.Contains(t, got.Text, "[user]\nKeep error responses unchanged.")
	assert.Contains(t, got.Text, "[assistant]\nReuse the counter.")
	assert.Contains(t, got.Text, "[tool]\nAllow(key string) bool")
	assert.NotContains(t, got.Text, "private")
	assert.Equal(t, before, s.BuildContext())
	assert.Same(t, private, s.BuildContext()[1].ProviderState)
	require.NoError(t, s.AddUser("Later steering"))
	assert.NotContains(t, got.Text, "Later steering")
	assert.Contains(t, s.BuildHandoff().Text, "Later steering")
}

func TestBuildHandoffExcludesUnsettledBatch(t *testing.T) {
	for _, results := range [][]llm.Message{
		nil,
		{{Role: llm.RoleTool, ToolCallID: "read", Content: "sibling result"}},
		{{Role: llm.RoleTool, ToolCallID: "unknown", Content: "orphan"}},
	} {
		s, err := NewSession()
		require.NoError(t, err)
		require.NoError(t, s.AddUser("Original goal"))
		require.NoError(t, s.Append(llm.Message{
			Role: llm.RoleAssistant, Content: "Unsettled batch text",
			ToolCalls: []llm.ToolCall{{ID: "read"}, {ID: "spawn"}},
		}))
		require.NoError(t, s.Append(results...))
		require.NoError(t, s.AddUser("After incomplete batch"))
		got := s.BuildHandoff().Text
		assert.Contains(t, got, "Original goal")
		assert.NotContains(t, got, "Unsettled")
		assert.NotContains(t, got, "sibling")
		assert.NotContains(t, got, "orphan")
		assert.NotContains(t, got, "After incomplete")
	}
}

func TestBuildHandoffUsesCompactedBranch(t *testing.T) {
	s, err := NewSession()
	require.NoError(t, err)
	require.NoError(t, s.AddUser("Obsolete history"))
	require.NoError(t, s.AppendCompaction(session.Compaction{Summary: "Keep the approved design"}))
	require.NoError(t, s.AddUser("Implement login limits"))
	got := s.BuildHandoff().Text
	assert.Contains(t, got, compactionSummaryContent("Keep the approved design"))
	assert.Contains(t, got, "Implement login limits")
	assert.NotContains(t, got, "Obsolete history")
}

func TestBuildHandoffBoundsUTF8AndPrefersRecentText(t *testing.T) {
	s, err := NewSession()
	require.NoError(t, err)
	require.NoError(t, s.AddUser("Original constraints"))
	for range 20 {
		require.NoError(t, s.Append(llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("背景", 3000)}))
	}
	require.NoError(t, s.AddUser("Latest correction"))
	got := s.BuildHandoff().Text
	assert.LessOrEqual(t, len(got), handoffMaxBytes)
	assert.True(t, utf8.ValidString(got))
	assert.Contains(t, got, "Original constraints")
	assert.Contains(t, got, "Latest correction")
	assert.Contains(t, got, handoffOmitted)
	assert.Contains(t, got, handoffTruncated)
}

func TestBuildHandoffCompletedBatchAndBoundedToolResult(t *testing.T) {
	s, err := NewSession()
	require.NoError(t, err)
	require.NoError(t, s.Append(
		llm.Message{Role: llm.RoleUser, Content: "Inspect login"},
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "a"}, {ID: "b"}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "b", Content: strings.Repeat("日志", 2000) + "END-OF-LOG"},
		llm.Message{Role: llm.RoleTool, ToolCallID: "a", Content: "Counter found"},
		llm.Message{Role: llm.RoleUser, Content: "Proceed with the counter"},
	))
	got := s.BuildHandoff().Text
	assert.Contains(t, got, "Counter found")
	assert.Contains(t, got, "Proceed with the counter")
	assert.NotContains(t, got, "END-OF-LOG")
	start := strings.Index(got, "[tool]\n") + len("[tool]\n")
	end := strings.Index(got[start:], "\n\n")
	require.Positive(t, end)
	assert.LessOrEqual(t, end, handoffToolBytes)
	assert.True(t, utf8.ValidString(got[start:start+end]))
}

func TestHandoffTextUTF8Boundary(t *testing.T) {
	text := "a中文"
	got, truncated := handoffText(text, len(text))
	assert.False(t, truncated)
	assert.Equal(t, text, got)

	got, truncated = handoffText(strings.Repeat(text, 10), len(handoffTruncated)+3)
	assert.True(t, truncated)
	assert.Equal(t, "a"+handoffTruncated, got)
	assert.True(t, utf8.ValidString(got))
}

func TestBuildHandoffEmptyAndNonText(t *testing.T) {
	s, err := NewSession()
	require.NoError(t, err)
	assert.Equal(t, job.ParentContext{Mode: job.ContextModeHandoff}, s.BuildHandoff())
	require.NoError(t, s.Append(
		llm.Message{Role: llm.RoleAssistant, ReasoningContent: "private"},
		llm.Message{Role: llm.RoleUser, Images: []llm.Image{{Data: "private-image", MimeType: "image/png"}}},
	))
	assert.Empty(t, s.BuildHandoff().Text)
}
