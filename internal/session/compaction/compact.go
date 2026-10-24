package compaction

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/util"
)

// CompactionPreparation holds everything Compact needs: the entries to
// summarize and the file operations captured from the summarized history.
// Recent messages stay in the session tree via FirstKeptEntryId.
type CompactionPreparation struct {
	FirstKeptEntryId       string
	MessagesToSummarize    []llm.Message
	TurnPrefixMessages     []llm.Message
	IsMidTurnCut           bool
	TokensBefore           int
	PreviousSummary        string
	PreviousFileOperations string
	// ReserveTokens is the headroom compaction keeps from the context window;
	// summaries are capped at a fraction of it.
	ReserveTokens int
	// TODO: wire into Compact / AppendCompaction so hook preserveData
	// survives across compaction rounds (currently collected but unused).
	PreviousPreserveData map[string]any
	FileOps              FileOperation
}

// PrepareCompact analyzes pathEntries and settings to decide what to
// summarize and what to keep. It returns an empty preparation when the
// session was already compacted.
func PrepareCompact(
	pathEntries []session.MessageEntry,
	settings Settings,
) (*CompactionPreparation, error) {
	// already compacted, skip
	if len(pathEntries) > 0 && pathEntries[len(pathEntries)-1].GetType() == session.EntryCompaction {
		return &CompactionPreparation{}, nil
	}

	// find the last compaction entry
	preCompactionIndex := -1
	for i := range slices.Backward(pathEntries) {
		entry := pathEntries[i]
		if entry.GetType() == session.EntryCompaction {
			preCompactionIndex = i
			break
		}
	}

	// [preCompactionIndex + 1, end)
	start := preCompactionIndex + 1
	end := len(pathEntries)
	cutPoint := findCutPoint(pathEntries, start, end, settings.keepRecentTokens)

	firstKeptEntry := pathEntries[cutPoint.firstKeptEntryIndex]
	if firstKeptEntry.GetID() == "" {
		return nil, errors.New("session needs migration")
	}

	firstKeptEntryID := firstKeptEntry.GetID()

	historyEnd := cutPoint.firstKeptEntryIndex
	if cutPoint.isMidTurnCut {
		historyEnd = cutPoint.turnStartIndex
	}

	var messagesToSummarize []llm.Message
	for i := start; i < historyEnd; i++ {
		msg := getMessageFromEntry(pathEntries[i])
		if msg != nil {
			messagesToSummarize = append(messagesToSummarize, *msg)
		}
	}

	// Messages for turn prefix summary (if splitting a turn)
	var turnPrefixMessages []llm.Message
	if cutPoint.isMidTurnCut {
		for i := cutPoint.turnStartIndex; i < cutPoint.firstKeptEntryIndex; i++ {
			msg := getMessageFromEntry(pathEntries[i])
			if msg != nil {
				turnPrefixMessages = append(turnPrefixMessages, *msg)
			}
		}
	}

	// Nothing falls outside keepRecentTokens: the cut point is the first entry,
	// so there is nothing to summarize. Persisting a summary here would replace
	// the previous summary with "No prior history." (and only add a message to
	// the context), so report an empty preparation instead; the caller skips
	// compaction on an empty FirstKeptEntryId.
	if len(messagesToSummarize) == 0 && len(turnPrefixMessages) == 0 {
		return &CompactionPreparation{}, nil
	}

	previousSummary := ""
	previousFileOperations := ""
	var previousPreserveData map[string]any
	if preCompactionIndex >= 0 {
		prevCompaction := pathEntries[preCompactionIndex].(session.CompactionEntry)
		previousSummary = prevCompaction.Compaction.Summary
		previousFileOperations = formatFileOperations(
			prevCompaction.Compaction.Details.ReadFiles,
			prevCompaction.Compaction.Details.ModifiedFiles,
		)
		previousPreserveData = prevCompaction.Compaction.PreserveData
	}

	// File operations must cover every bucket the cut drops, which is the history
	// plus the turn prefix on a mid-turn cut. Leaving the prefix out hides the
	// edits made in the turn the cut lands in, so the summary lists files that
	// were edited under "read" and drops the rest entirely.
	fileOps := extractFileOperations(
		slices.Concat(messagesToSummarize, turnPrefixMessages),
		pathEntries,
		preCompactionIndex,
	)

	lastUsage := getLastAssistantUsage(pathEntries)
	// ContextTokens, not TotalTokens: a provider that omits the total still has
	// usable buckets, and the marker must match what the readout shows.
	tokenBefore := lastUsage.ContextTokens()

	return &CompactionPreparation{
		FirstKeptEntryId:       firstKeptEntryID,
		MessagesToSummarize:    messagesToSummarize,
		TurnPrefixMessages:     turnPrefixMessages,
		TokensBefore:           tokenBefore,
		PreviousSummary:        previousSummary,
		PreviousFileOperations: previousFileOperations,
		PreviousPreserveData:   previousPreserveData,
		FileOps:                *fileOps,
		IsMidTurnCut:           cutPoint.isMidTurnCut,
		ReserveTokens:          settings.reverseTokens,
	}, nil
}

// Compact generates a summary for preparation via llm and returns the
// session.Compaction to persist.
func Compact(
	ctx context.Context,
	preparation CompactionPreparation,
	llm llm.Compactor,
) (session.Compaction, error) {
	var (
		summary string
		err     error
	)
	if preparation.IsMidTurnCut && len(preparation.TurnPrefixMessages) > 0 {
		summary, err = summarizeMidTurnCut(ctx, preparation, llm)
	} else {
		summary, err = summarizeHistory(ctx, preparation, llm)
	}
	if err != nil {
		return session.Compaction{}, err
	}

	readFiles, modifiedFiles := computeFileLists(&preparation.FileOps)
	fileOperations := formatFileOperations(readFiles, modifiedFiles)
	if fileOperations != "" {
		summary += fileOperations // formatFileOperations returns its own separator
	}

	return session.Compaction{
		Summary:          summary,
		FirstKeptEntryID: preparation.FirstKeptEntryId,
		TokensBefore:     preparation.TokensBefore,
		Details: session.CompactionDetails{
			ReadFiles:     readFiles,
			ModifiedFiles: modifiedFiles,
		},
	}, nil
}

// summarizeMidTurnCut runs history and turn-prefix summaries in parallel,
// then joins them when the cut falls inside a turn.
func summarizeMidTurnCut(
	ctx context.Context,
	preparation CompactionPreparation,
	llm llm.Compactor,
) (string, error) {
	var (
		historySummary       string
		turnPrefixSummary    string
		historySummaryErr    error
		turnPrefixSummaryErr error
	)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		historySummary, historySummaryErr = summarizeHistory(ctx, preparation, llm)
	}()

	go func() {
		defer wg.Done()
		turnPrefixSummary, turnPrefixSummaryErr = generateTurnPrefixSummary(
			ctx,
			llm,
			preparation.TurnPrefixMessages,
			summarizationCap(preparation.ReserveTokens, turnPrefixSummaryRatio, maxTurnPrefixSummaryTokens),
		)
	}()

	wg.Wait()

	if historySummaryErr != nil {
		return "", historySummaryErr
	}
	if turnPrefixSummaryErr != nil {
		return "", turnPrefixSummaryErr
	}

	return historySummary + "\n\n---\n\n**Turn Context (mid-turn cut):**\n\n" + turnPrefixSummary, nil
}

// summarizeHistory generates a single summary over MessagesToSummarize.
func summarizeHistory(
	ctx context.Context,
	preparation CompactionPreparation,
	llm llm.Compactor,
) (string, error) {
	if len(preparation.MessagesToSummarize) == 0 {
		// An empty history bucket can still have context from an earlier compaction.
		if preparation.PreviousSummary != "" {
			stripped := stripFileOperations(preparation.PreviousSummary, preparation.PreviousFileOperations)
			if strings.TrimSpace(stripped) != "" {
				return stripped, nil
			}
		}
		// Make the absence of history text explicit when the previous summary
		// was blank or contained only its appended file list.
		return "No prior history.", nil
	}
	return generateSummary(
		ctx,
		llm,
		preparation.MessagesToSummarize,
		preparation.PreviousSummary,
		summarizationCap(preparation.ReserveTokens, historySummaryRatio, maxHistorySummaryTokens),
	)
}

// getLastAssistantUsage reads usage from the entry, not llm.Message: the
// latter is json:"-" and so reads as zero for every reloaded session.
func getLastAssistantUsage(entries []session.MessageEntry) llm.Usage {
	for i := range slices.Backward(entries) {
		entry := entries[i]
		if entry.GetType() == session.EntryMessage {
			msgEntry := entry.(session.SessionMessageEntry)
			if msgEntry.Message.Role == llm.RoleAssistant {
				return msgEntry.Usage
			}
		}
	}
	return llm.Usage{
		TotalTokens: 0,
	}
}

func getMessageFromEntry(entry session.MessageEntry) *llm.Message {
	if entry.GetType() == session.EntryMessage {
		msgEntry := entry.(session.SessionMessageEntry)
		return &msgEntry.Message
	}
	return nil
}

const toolResultMaxChars = 500

// SerializeConversation formats messages as a single string for summary prompts:
// [User]: ..., [Assistant]: ..., [Assistant tool calls]: ..., [Tool result]: ...
func SerializeConversation(messages []llm.Message) string {
	var parts []string
	for _, msg := range messages {
		switch msg.Role {
		case llm.RoleUser:
			if msg.Content != "" {
				parts = append(parts, "[User]: "+msg.Content)
			}
		case llm.RoleAssistant:
			if msg.Content != "" {
				parts = append(parts, "[Assistant]: "+msg.Content)
			}
			if len(msg.ToolCalls) > 0 {
				var callStrs []string
				for _, tc := range msg.ToolCalls {
					callStrs = append(callStrs, tc.Function.Name+"("+tc.Function.Arguments+")")
				}
				parts = append(parts, "[Assistant tool calls]: "+strings.Join(callStrs, "; "))
			}
		case llm.RoleTool:
			if msg.Content != "" {
				parts = append(parts, "[Tool result]: "+util.TruncateRunes(msg.Content, toolResultMaxChars, "..."))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}
