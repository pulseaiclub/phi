package compaction

import (
	"github.com/pulseaiclub/phi/internal/core"
	"github.com/pulseaiclub/phi/internal/session"
)

// CutPointResult identifies where to cut the session history: the index of
// the first entry to keep and, when the cut falls mid-turn, that turn's
// starting index.
type CutPointResult struct {
	// firstKeptEntryIndex is the index of the first entry to keep.
	firstKeptEntryIndex int
	// turnStartIndex is the user message that opens the turn containing the
	// cut, or -1 when the cut is at a turn boundary.
	turnStartIndex int
	// isMidTurnCut is true when firstKept is not a user message, so the
	// orphaned start of that turn needs a turn-prefix summary.
	isMidTurnCut bool
}

func findCutPoint(
	messageEntries []session.MessageEntry,
	startIndex int,
	endIndex int,
	keepRecentTokens int,
) CutPointResult {
	cutPoints := collectCutPoints(messageEntries, startIndex, endIndex)
	if len(cutPoints) == 0 {
		return CutPointResult{
			firstKeptEntryIndex: startIndex,
			turnStartIndex:      -1,
			isMidTurnCut:        false,
		}
	}

	cutIndex := findCutIndex(
		messageEntries,
		startIndex,
		endIndex,
		keepRecentTokens,
		cutPoints,
	)

	// Entry at the cut point;
	// used to tell if we cut at a user message (a turn boundary).
	cutEntry := messageEntries[cutIndex]
	isUserMessage := cutEntry.GetType() == session.EntryMessage &&
		cutEntry.(session.SessionMessageEntry).Message.Role == core.RoleUser

	// [userMessage, cutIndex) is the orphaned turn prefix when cutting mid-turn.
	turnStartIndex := -1
	if !isUserMessage {
		turnStartIndex = findTurnStartIndex(messageEntries, cutIndex, startIndex)
	}

	return CutPointResult{
		firstKeptEntryIndex: cutIndex,
		turnStartIndex:      turnStartIndex,
		isMidTurnCut:        !isUserMessage && turnStartIndex != -1,
	}
}

// findCutIndex walks entries backward from endIndex accumulating each
// message's own estimated size until it reaches keepRecentTokens. It then
// finds the first valid cut point at or after the position where the budget
// was reached. If the budget is never reached, it falls back to the earliest
// candidate cut point.
func findCutIndex(
	entries []session.MessageEntry,
	startIndex int,
	endIndex int,
	keepRecentTokens int,
	cutPoints []int,
) int {
	accumulatedTokens := 0
	cutIndex := cutPoints[0]

	for i := endIndex - 1; i >= startIndex; i-- {
		entry := entries[i]
		if entry.GetType() != session.EntryMessage {
			continue
		}

		messageTokens := estimateMessageTokens(entry.(session.SessionMessageEntry).Message)
		if messageTokens == 0 {
			continue
		}
		accumulatedTokens += messageTokens

		if accumulatedTokens >= keepRecentTokens {
			for _, point := range cutPoints {
				if point >= i {
					cutIndex = point
					break
				}
			}
			break
		}
	}

	return cutIndex
}

// estimatedImageChars is the stand-in size for one attached image. Images
// arrive base64-encoded, so their string length says nothing about the tokens
// the model spends on them.
const estimatedImageChars = 4800

// estimateMessageTokens estimates one message's size with a chars/4 heuristic.
// The budget is spent per message, so this must never fall back to provider
// usage: an assistant message reports the size of the whole conversation up to
// that turn (and only assistant messages report anything), so accumulating it
// overflows keepRecentTokens on the newest message and collapses the cut to
// the last entry.
func estimateMessageTokens(msg core.Message) int {
	chars := len(msg.Content) + len(msg.ReasoningContent)
	for _, call := range msg.ToolCalls {
		chars += len(call.Function.Name) + len(call.Function.Arguments)
	}
	chars += len(msg.Images) * estimatedImageChars
	return (chars + 3) / 4
}

// collectCutPoints returns the indices of every user/assistant message
// entry in [startIndex, endIndex), in chronological order: the positions
// where a compaction cut may land. Picking among them is findCutIndex's job.
func collectCutPoints(entries []session.MessageEntry, startIndex, endIndex int) []int {
	var cutPoints []int

	for i := startIndex; i < endIndex; i++ {
		e := entries[i]
		if e.GetType() != session.EntryMessage {
			continue
		}
		msgEntry := e.(session.SessionMessageEntry)
		role := msgEntry.Message.Role
		if role == core.RoleUser || role == core.RoleAssistant {
			cutPoints = append(cutPoints, i)
		}
	}
	return cutPoints
}

// findTurnStartIndex walks backward from entryIndex to find the user message
// that starts the current turn.
func findTurnStartIndex(entries []session.MessageEntry, entryIndex, startIndex int) int {
	for i := entryIndex; i >= startIndex; i-- {
		entry := entries[i]
		ty := entry.GetType()

		if ty == session.EntryMessage {
			msgEntry := entry.(session.SessionMessageEntry)
			if msgEntry.Message.Role == core.RoleUser {
				return i
			}
		}
	}
	return -1
}
