package session

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pulseaiclub/phi/internal/llm"
)

func TestParseToolStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want ToolStatus
	}{
		{"queued", ToolQueued},
		{"in-progress", ToolInProgress},
		{"done", ToolDone},
		{"error", ToolError},
		{"cancelled", ToolCancelled},
		{"rejected", ToolRejected},
		{"rejected-by-user", ToolRejected},
		{"", ToolInProgress},
		{"unknown", ToolInProgress},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, ParseToolStatus(tt.in), "%q", tt.in)
	}
}

func TestToolStatusStringRoundTrip(t *testing.T) {
	t.Parallel()
	for _, s := range []ToolStatus{
		ToolQueued, ToolInProgress, ToolDone, ToolError, ToolCancelled, ToolRejected,
	} {
		assert.Equal(t, s, ParseToolStatus(s.String()), s.String())
	}
}

// A cache-heavy turn is mostly cache reads, so the context size has to come
// from the total (or the bucket sum), not from the uncached input alone —
// that read as an almost empty window on a 1M-window model.
func TestContextTokensCountsCachedPrompt(t *testing.T) {
	t.Parallel()
	// Real numbers from a 1M-window OpenAI Responses session: gross prompt
	// 35272, of which 33792 was served from cache.
	u := TokenUsage{PromptTokens: 1480, CachedTokens: 33792, CompletionTokens: 1789, TotalTokens: 37061}
	assert.Equal(t, 37061, u.ContextTokens())
	// No provider total: the disjoint buckets add up to the same context.
	assert.Equal(t, 37061, TokenUsage{
		PromptTokens: 1480, CachedTokens: 33792, CompletionTokens: 1789,
	}.ContextTokens())
	assert.Zero(t, TokenUsage{}.ContextTokens())
}

// The UI copy and the provider usage must agree on context size: the composer
// readout and the compaction threshold read one each.
func TestContextTokensMatchesProviderUsage(t *testing.T) {
	t.Parallel()
	cases := map[string]llm.Usage{
		"reported total": {PromptTokens: 1480, CompletionTokens: 1789, TotalTokens: 37061},
		"no total": {
			PromptTokens: 1480, CompletionTokens: 1789,
			PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: 33792},
		},
		"cache write": {
			PromptTokens: 12, CompletionTokens: 7,
			PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: 900, CacheWriteTokens: 50},
		},
		"empty":          {},
		"continued turn": {TotalTokens: 360_000, OccupiedTokens: 210_000},
	}
	for name, provider := range cases {
		assert.Equal(t, provider.ContextTokens(), TokenUsageFrom(provider).ContextTokens(), name)
	}
}
