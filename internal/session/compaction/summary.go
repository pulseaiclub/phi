package compaction

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/pulseaiclub/phi/internal/llm"
)

var (
	//go:embed compaction-summary.tmpl
	compactionSummaryPrompt string
	//go:embed compaction-update-summary.tmpl
	compactionUpdateSummaryPrompt string
	//go:embed compaction-turn-prefix.tmpl
	compactionTurnPrefixPrompt string
	//go:embed compaction-rewrite-draft.tmpl
	compactionRewriteDraftPrompt string
)

// Summaries are capped at a fraction of the headroom compaction frees
// (reserveTokens). A summary larger than the headroom it creates does not pay
// for itself, and the turn-prefix budget is smaller because it only recovers
// the tail of one turn.
//
// The ceilings keep a checkpoint a handoff note rather than a transcript: past
// a few thousand tokens it costs the next request more context than it saves,
// and a cap above the provider's own output limit only buys a truncated answer.
const (
	historySummaryRatio    = 0.8
	turnPrefixSummaryRatio = 0.5

	maxHistorySummaryTokens    = 4096
	maxTurnPrefixSummaryTokens = 2048
)

// summarizationCap returns the output cap for one summary. Zero means the
// provider default (no budget to derive a cap from).
func summarizationCap(reserveTokens int, ratio float64, ceiling int) int {
	if reserveTokens <= 0 {
		return 0
	}
	return min(int(float64(reserveTokens)*ratio), ceiling)
}

// errTruncatedSummary reports a summary that hit the output cap again after its
// rewrite: the text is a prefix of the summary the model meant to write, so
// persisting it would drop the history it replaced.
func errTruncatedSummary(label string) error {
	return fmt.Errorf("%s failed: generation hit the token cap, summary incomplete", label)
}

// errEmptySummary reports a finished generation that carries no text. A blank
// summary persists nothing on its own, and the file list appended around it
// would end up as a checkpoint that lost the history it replaced.
func errEmptySummary(label string) error {
	return fmt.Errorf("%s failed: model returned an empty summary", label)
}

// generateSummary generates a summary of the conversation history using an LLM.
// If a previousSummary is provided, it will update the existing summary with new messages.
// Otherwise, it generates an initial summary from the currentMessages.
func generateSummary(
	ctx context.Context,
	compactor llm.Compactor,
	currentMessages []llm.Message,
	previousSummary string,
	maxTokens int,
) (string, error) {
	basePrompt := compactionSummaryPrompt
	if previousSummary != "" {
		basePrompt = compactionUpdateSummaryPrompt
	}

	conversation := SerializeConversation(currentMessages)
	promptText := fmt.Sprintf("<conversation>\n%s\n</conversation>", conversation)
	if previousSummary != "" {
		promptText += fmt.Sprintf("<previous-summary>\n%s\n</previous-summary>", previousSummary)
	}
	promptText += basePrompt
	return compact(ctx, compactor, promptText, maxTokens, "Summarization")
}

// generateTurnPrefixSummary generates a summary specifically for turn prefixes
// (prefix-based compaction): the prefix of a turn whose suffix is kept. Without
// the prompt the model is handed a bare conversation and no instruction, and its
// continuation gets persisted as the session summary.
func generateTurnPrefixSummary(
	ctx context.Context,
	compactor llm.Compactor,
	messages []llm.Message,
	maxTokens int,
) (string, error) {
	conversation := SerializeConversation(messages)
	promptText := fmt.Sprintf("<conversation>\n%s\n</conversation>\n\n", conversation)
	promptText += compactionTurnPrefixPrompt
	return compact(ctx, compactor, promptText, maxTokens, "Turn prefix summarization")
}

// rewriteDraft asks the model to turn its own capped answer into a complete
// summary. A second call with the original prompt only truncates at the same
// place, but the draft already holds most of the content, so a tighter rewrite
// either fits the cap or the answer is not summarizable at all.
func rewriteDraft(
	ctx context.Context,
	compactor llm.Compactor,
	draft string,
	maxTokens int,
	label string,
) (string, error) {
	promptText := fmt.Sprintf("<draft-summary>\n%s\n</draft-summary>\n\n%s", draft, compactionRewriteDraftPrompt)
	res, err := compactor.Compact(ctx, llm.CompactRequest{Prompt: promptText, MaxTokens: maxTokens})
	if err != nil {
		return "", err
	}
	if res.Truncated {
		return "", errTruncatedSummary(label)
	}
	return res.Text, nil
}

// compact runs one summarization call. A capped answer gets one rewrite before
// it counts as a failure: persisting a prefix of the summary would silently
// drop the history it is meant to stand in for, but failing the whole
// compaction leaves a full context with no way forward, so the cap is retried
// once on its own text. A blank answer has nothing to rewrite from and fails
// immediately.
func compact(
	ctx context.Context,
	compactor llm.Compactor,
	prompt string,
	maxTokens int,
	label string,
) (string, error) {
	res, err := compactor.Compact(ctx, llm.CompactRequest{Prompt: prompt, MaxTokens: maxTokens})
	if err != nil {
		return "", err
	}
	text := res.Text
	if res.Truncated {
		text, err = rewriteDraft(ctx, compactor, res.Text, maxTokens, label)
		if err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(text) == "" {
		return "", errEmptySummary(label)
	}
	return text, nil
}
