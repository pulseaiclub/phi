package compaction

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

// captureCompactor records the prompts and caps handed to the LLM so tests can
// assert on what actually reaches the provider. Mid-turn compaction summarizes
// the history and the turn prefix in parallel, hence the mutex.
type captureCompactor struct {
	mu        sync.Mutex
	prompts   []string
	maxTokens []int
	text      string
	truncated bool
	// truncatedOnce caps only the first answer so tests can drive the rewrite
	// retry that follows a capped summary.
	truncatedOnce bool
	// textFor picks the canned answer per prompt, so tests can hand a blank
	// answer to one mid-turn bucket and a valid one to the other. The two
	// buckets run in parallel with no fixed order, so a single fixed text
	// cannot express that. Nil keeps the fixed text (with the empty-text
	// "SUMMARY" default) so older tests stay untouched.
	textFor func(prompt string) string
}

func (c *captureCompactor) Compact(_ context.Context, req llm.CompactRequest) (llm.CompactResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prompts = append(c.prompts, req.Prompt)
	c.maxTokens = append(c.maxTokens, req.MaxTokens)
	text := c.text
	if text == "" {
		text = "SUMMARY"
	}
	if c.textFor != nil {
		text = c.textFor(req.Prompt)
	}
	truncated := c.truncated || (c.truncatedOnce && len(c.prompts) == 1)
	return llm.CompactResult{Text: text, Truncated: truncated}, nil
}

func TestGenerateTurnPrefixSummary_IncludesPrompt(t *testing.T) {
	c := &captureCompactor{}

	got, err := generateTurnPrefixSummary(
		t.Context(),
		c,
		[]llm.Message{{Role: llm.RoleUser, Content: "fix the parser"}},
		1024,
	)

	require.NoError(t, err)
	assert.Equal(t, "SUMMARY", got)
	require.Len(t, c.prompts, 1)
	assert.Contains(t, c.prompts[0], "</conversation>\n\n"+compactionTurnPrefixPrompt,
		"the turn-prefix prompt must follow the conversation")
	assert.Contains(t, c.prompts[0], "## Original Request")
	assert.Contains(t, c.prompts[0], "## Context for Suffix")
	assert.Equal(t, []int{1024}, c.maxTokens)
}

func TestGenerateSummary_IncludesPrompt(t *testing.T) {
	c := &captureCompactor{}

	_, err := generateSummary(
		t.Context(),
		c,
		[]llm.Message{{Role: llm.RoleUser, Content: "hello"}},
		"",
		2048,
	)

	require.NoError(t, err)
	require.Len(t, c.prompts, 1)
	assert.Contains(t, c.prompts[0], "[User]: hello")
	assert.Contains(t, c.prompts[0], compactionSummaryPrompt)
	assert.Equal(t, []int{2048}, c.maxTokens)
}

// A capped answer is retried once as a rewrite of its own draft, so a model
// that rambled past the cap still produces a complete checkpoint.
func TestGenerateSummary_Truncated_RetriesRewrite(t *testing.T) {
	c := &captureCompactor{
		truncatedOnce: true,
		textFor: func(prompt string) string {
			if strings.Contains(prompt, "<draft-summary>") {
				return "## Goal\ncomplete"
			}
			return "## Goal\ncut off"
		},
	}

	got, err := generateSummary(t.Context(), c, []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, "", 4096)

	require.NoError(t, err)
	assert.Equal(t, "## Goal\ncomplete", got)
	require.Len(t, c.prompts, 2)
	assert.Contains(t, c.prompts[1], "<draft-summary>\n## Goal\ncut off\n</draft-summary>",
		"the rewrite must carry the capped draft")
	assert.Contains(t, c.prompts[1], compactionRewriteDraftPrompt)
	assert.Equal(t, []int{4096, 4096}, c.maxTokens, "the rewrite gets the same cap")
}

// A summary that stops at the token cap every time is a prefix of what the
// model meant to write. Persisting it as the session summary would drop the
// history it replaced, so both summary paths must still fail after the retry.
func TestGenerateSummary_Truncated_ReturnsError(t *testing.T) {
	c := &captureCompactor{truncated: true}

	_, err := generateSummary(t.Context(), c, []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, "", 2048)

	require.Error(t, err)
	assert.Equal(t, "Summarization failed: generation hit the token cap, summary incomplete", err.Error())
	assert.Len(t, c.prompts, 2, "a capped answer is retried exactly once")
}

func TestGenerateTurnPrefixSummary_Truncated_ReturnsError(t *testing.T) {
	c := &captureCompactor{truncated: true}

	_, err := generateTurnPrefixSummary(t.Context(), c, []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, 1024)

	require.Error(t, err)
	assert.Equal(t, "Turn prefix summarization failed: generation hit the token cap, summary incomplete", err.Error())
}

// A finished generation that carries no text summarizes nothing. Persisting it
// as the checkpoint would replace the history with blank, so every summary
// path must fail on it.
func TestGenerateSummary_BlankText_ReturnsError(t *testing.T) {
	for _, previousSummary := range []string{"", "earlier summary"} {
		for _, blank := range []string{"", "   ", "\n\t\n"} {
			name := fmt.Sprintf("previous=%q blank=%q", previousSummary, blank)
			t.Run(name, func(t *testing.T) {
				c := &captureCompactor{textFor: func(string) string { return blank }}

				_, err := generateSummary(
					t.Context(),
					c,
					[]llm.Message{{Role: llm.RoleUser, Content: "hi"}},
					previousSummary,
					2048,
				)

				require.Error(t, err)
				assert.Contains(t, err.Error(), "Summarization failed: model returned an empty summary")
			})
		}
	}
}

func TestGenerateTurnPrefixSummary_BlankText_ReturnsError(t *testing.T) {
	for _, blank := range []string{"", "   ", "\n\t\n"} {
		c := &captureCompactor{textFor: func(string) string { return blank }}

		_, err := generateTurnPrefixSummary(t.Context(), c, []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, 1024)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "Turn prefix summarization failed: model returned an empty summary")
	}
}

func TestSummarizationCap(t *testing.T) {
	// Default reserveTokens is 16384: 0.8 for history, 0.5 for the turn prefix,
	// both clamped to the ceiling a checkpoint is worth.
	assert.Equal(
		t,
		maxHistorySummaryTokens,
		summarizationCap(16384, historySummaryRatio, maxHistorySummaryTokens),
	)
	assert.Equal(
		t,
		maxTurnPrefixSummaryTokens,
		summarizationCap(16384, turnPrefixSummaryRatio, maxTurnPrefixSummaryTokens),
	)
	assert.Equal(t, 800, summarizationCap(1000, historySummaryRatio, maxHistorySummaryTokens),
		"a small headroom still binds below the ceiling")
	assert.Equal(t, 0, summarizationCap(0, historySummaryRatio, maxHistorySummaryTokens),
		"no budget means the provider default")
}

// A mid-turn cut summarizes two buckets at once, each with its own cap.
func TestSummarizeMidTurnCut_CapsEachSummary(t *testing.T) {
	c := &captureCompactor{}

	summary, err := summarizeMidTurnCut(t.Context(), CompactionPreparation{
		MessagesToSummarize: []llm.Message{{Role: llm.RoleUser, Content: "older history"}},
		TurnPrefixMessages:  []llm.Message{{Role: llm.RoleUser, Content: "turn prefix"}},
		ReserveTokens:       16384,
	}, c)

	require.NoError(t, err)
	assert.Contains(t, summary, "SUMMARY")
	assert.Contains(t, summary, "Turn Context (mid-turn cut)")
	assert.ElementsMatch(t, []int{maxHistorySummaryTokens, maxTurnPrefixSummaryTokens}, c.maxTokens)
}

// A blank answer from either mid-turn bucket must fail the whole cut. The file
// list is appended after the summaries, so it must never turn a blank bucket
// into a checkpoint that kept only file paths.
func TestCompact_MidTurnCut_BlankBucketSummary_ReturnsError(t *testing.T) {
	textFor := func(blankBucket, blank string) func(string) string {
		return func(prompt string) string {
			if strings.Contains(prompt, compactionTurnPrefixPrompt) {
				if blankBucket == "turn-prefix" {
					return blank
				}
				return "Turn summary."
			}
			if blankBucket == "history" {
				return blank
			}
			return "History summary."
		}
	}

	for _, blankBucket := range []string{"history", "turn-prefix"} {
		for _, blank := range []string{"", "   ", "\n\t\n"} {
			for _, withFileOps := range []bool{false, true} {
				name := fmt.Sprintf("%s blank=%q fileOps=%v", blankBucket, blank, withFileOps)
				t.Run(name, func(t *testing.T) {
					prep := CompactionPreparation{
						IsMidTurnCut:        true,
						MessagesToSummarize: []llm.Message{{Role: llm.RoleUser, Content: "older history"}},
						TurnPrefixMessages:  []llm.Message{{Role: llm.RoleUser, Content: "turn prefix"}},
						ReserveTokens:       16384,
					}
					if withFileOps {
						prep.FileOps = FileOperation{read: []string{"main.go"}}
					}
					c := &captureCompactor{textFor: textFor(blankBucket, blank)}

					_, err := Compact(t.Context(), prep, c)

					require.Error(t, err)
					want := "Summarization"
					if blankBucket == "turn-prefix" {
						want = "Turn prefix summarization"
					}
					assert.Contains(t, err.Error(), want+" failed: model returned an empty summary")
				})
			}
		}
	}
}

func TestCompact_EmptyHistory(t *testing.T) {
	for _, midTurn := range []bool{false, true} {
		for _, previousSummary := range []string{"", "Keep the public API unchanged.\nPreserve exact error messages."} {
			name := "history"
			if midTurn {
				name = "mid-turn"
			}
			if previousSummary != "" {
				name += "/previous-summary"
			} else {
				name += "/no-previous-summary"
			}
			t.Run(name, func(t *testing.T) {
				c := &captureCompactor{text: "Current turn context."}
				prep := CompactionPreparation{PreviousSummary: previousSummary, IsMidTurnCut: midTurn}
				want := previousSummary
				if want == "" {
					want = "No prior history."
				}
				wantCalls := 0
				if midTurn {
					prep.TurnPrefixMessages = []llm.Message{{Role: llm.RoleUser, Content: "fix the parser"}}
					want += "\n\n---\n\n**Turn Context (mid-turn cut):**\n\nCurrent turn context."
					wantCalls = 1
				}

				comp, err := Compact(t.Context(), prep, c)

				require.NoError(t, err)
				assert.Equal(t, want, comp.Summary)
				assert.Len(t, c.prompts, wantCalls, "an empty history must not trigger a model request")
			})
		}
	}
}

// A previous summary that was nothing but the file list strips down to blank.
// The placeholder makes the absence of history text explicit instead of
// persisting another file-list-only checkpoint, without a model call.
func TestCompact_EmptyHistory_BlankStrippedPreviousSummary(t *testing.T) {
	fileList := formatFileOperations([]string{"a.go"}, nil)
	c := &captureCompactor{}

	prep := CompactionPreparation{
		PreviousSummary:        fileList,
		PreviousFileOperations: fileList,
		FileOps:                FileOperation{read: []string{"b.go"}},
	}

	comp, err := Compact(t.Context(), prep, c)

	require.NoError(t, err)
	assert.Equal(t, "No prior history."+formatFileOperations([]string{"b.go"}, nil), comp.Summary)
	assert.Empty(t, c.prompts, "a blank stripped summary must not trigger a model request")
}
