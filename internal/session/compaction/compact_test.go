package compaction

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/session"
)

// sizedContent returns content of exactly `tokens` estimated tokens (4 chars
// per token), so fixtures control the cut budget through message size.
func sizedContent(tokens int) string {
	return strings.Repeat("x", tokens*4)
}

// msgEntry builds a persisted message entry of `tokens` estimated tokens.
// `usage` is the provider-reported context size, which only feeds TokensBefore
// (llm.Message.Usage is json:"-" and does not survive a reload, so it lives on
// the entry wrapper).
func msgEntry(id string, role llm.Role, tokens, usage int) session.MessageEntry {
	return session.SessionMessageEntry{
		SessionBaseEntry: session.SessionBaseEntry{ID: id},
		Message:          llm.Message{Role: role, Content: sizedContent(tokens)},
		Usage:            llm.Usage{TotalTokens: usage},
	}
}

// editEntry builds an assistant entry whose tool call edits path, which is what
// file-operation extraction collects from.
func editEntry(id, path string) session.MessageEntry {
	return session.SessionMessageEntry{
		SessionBaseEntry: session.SessionBaseEntry{ID: id},
		Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{
					Function: llm.Function{
						Name:      "edit",
						Arguments: `{"payload":"*** SM:EDIT ` + path + `\n*** SM:FIND\nx\n*** SM:PUT\ny\n"}`,
					},
				},
			},
		},
	}
}

func TestPrepareCompact_AlreadyCompacted_ReturnsEmptyPreparation(t *testing.T) {
	entries := []session.MessageEntry{
		msgEntry("e1", llm.RoleUser, 10, 0),
		session.CompactionEntry{
			SessionBaseEntry: session.SessionBaseEntry{ID: "comp1"},
			Compaction: session.Compaction{
				Summary: "prev",
			},
		},
	}
	settings := Settings{keepRecentTokens: 100}

	prep, err := PrepareCompact(entries, settings)

	assert.NoError(t, err)
	assert.NotNil(t, prep)
	assert.Empty(t, prep.FirstKeptEntryId)
	assert.Nil(t, prep.MessagesToSummarize)
}

func TestPrepareCompact_SessionNeedsMigration_ReturnsError(t *testing.T) {
	entries := []session.MessageEntry{
		session.SessionMessageEntry{
			SessionBaseEntry: session.SessionBaseEntry{ID: ""},
			Message:          llm.Message{Role: llm.RoleUser},
			Usage:            llm.Usage{TotalTokens: 10},
		},
	}
	settings := Settings{keepRecentTokens: 100}

	prep, err := PrepareCompact(entries, settings)

	assert.Error(t, err)
	assert.Nil(t, prep)
	assert.Contains(t, err.Error(), "migration")
}

func TestPrepareCompact_NoPreviousCompaction_SplitsByKeepRecentTokens(t *testing.T) {
	entries := []session.MessageEntry{
		msgEntry("e1", llm.RoleUser, 10, 0),
		msgEntry("e2", llm.RoleAssistant, 20, 20),
		msgEntry("e3", llm.RoleUser, 30, 0),
	}
	settings := Settings{keepRecentTokens: 25}

	prep, err := PrepareCompact(entries, settings)

	assert.NoError(t, err)
	assert.NotNil(t, prep)
	assert.Equal(t, "e3", prep.FirstKeptEntryId)
	assert.False(t, prep.IsMidTurnCut)
	assert.Len(t, prep.MessagesToSummarize, 2)
	assert.Equal(t, llm.RoleUser, prep.MessagesToSummarize[0].Role)
	assert.Equal(t, llm.RoleAssistant, prep.MessagesToSummarize[1].Role)
	// TokensBefore is the provider-reported context size, not the sum of the
	// estimated message sizes the cut uses.
	assert.Equal(t, 20, prep.TokensBefore)
	assert.Empty(t, prep.PreviousSummary)
	assert.Nil(t, prep.PreviousPreserveData)
}

// Everything fits in keepRecentTokens, so there is nothing to summarize: the
// preparation must stay empty rather than persist a "No prior history." summary
// over the previous one.
func TestPrepareCompact_UnderTokenLimit_ReturnsEmptyPreparation(t *testing.T) {
	entries := []session.MessageEntry{
		msgEntry("e1", llm.RoleUser, 10, 0),
		msgEntry("e2", llm.RoleAssistant, 20, 20),
	}
	settings := Settings{keepRecentTokens: 100}

	prep, err := PrepareCompact(entries, settings)

	assert.NoError(t, err)
	assert.NotNil(t, prep)
	assert.Empty(t, prep.FirstKeptEntryId)
	assert.Empty(t, prep.MessagesToSummarize)
	assert.Empty(t, prep.TurnPrefixMessages)
}

func TestPrepareCompact_WithPreviousCompaction_SetsSummaryAndPreserveData(t *testing.T) {
	entries := []session.MessageEntry{
		session.CompactionEntry{
			SessionBaseEntry: session.SessionBaseEntry{ID: "comp1"},
			Compaction: session.Compaction{
				Summary:      "old summary",
				PreserveData: map[string]any{"k": "v"},
			},
		},
		msgEntry("e1", llm.RoleUser, 10, 0),
		msgEntry("e2", llm.RoleAssistant, 10, 10),
		msgEntry("e3", llm.RoleUser, 10, 0),
		msgEntry("e4", llm.RoleAssistant, 10, 40),
	}
	// Budget reached on e3 (the last two messages cost 20), so e1+e2 are
	// summarized and the previous summary is carried for the update prompt.
	settings := Settings{keepRecentTokens: 20}

	prep, err := PrepareCompact(entries, settings)

	assert.NoError(t, err)
	assert.NotNil(t, prep)
	assert.Equal(t, "old summary", prep.PreviousSummary)
	assert.Equal(t, map[string]any{"k": "v"}, prep.PreviousPreserveData)
	assert.Equal(t, "e3", prep.FirstKeptEntryId)
	assert.False(t, prep.IsMidTurnCut)
	assert.Len(t, prep.MessagesToSummarize, 2)
}

func TestPrepareCompact_MidTurnCut_SplitsTurnPrefix(t *testing.T) {
	entries := []session.MessageEntry{
		msgEntry("e1", llm.RoleUser, 10, 0),
		msgEntry("e2", llm.RoleAssistant, 10, 10),
		msgEntry("e3", llm.RoleUser, 10, 0),
		msgEntry("e4", llm.RoleAssistant, 30, 40),
	}
	settings := Settings{keepRecentTokens: 20}

	prep, err := PrepareCompact(entries, settings)

	assert.NoError(t, err)
	assert.NotNil(t, prep)
	// The budget is reached on the newest assistant message, which is not a
	// turn start: the user message opening that turn lands in the prefix bucket
	// and everything before it is summarized.
	assert.Equal(t, "e4", prep.FirstKeptEntryId)
	assert.True(t, prep.IsMidTurnCut)
	assert.Len(t, prep.MessagesToSummarize, 2)
	assert.Equal(t, llm.RoleAssistant, prep.MessagesToSummarize[1].Role)
	assert.Len(t, prep.TurnPrefixMessages, 1)
	assert.Equal(t, llm.RoleUser, prep.TurnPrefixMessages[0].Role)
}

// The summary cap follows the headroom compaction frees, and a summary that
// stopped at that cap must abort the compaction instead of becoming the new
// session summary.
func TestCompact_TruncatedSummary_ReturnsError(t *testing.T) {
	entries := []session.MessageEntry{
		msgEntry("e1", llm.RoleUser, 10, 0),
		msgEntry("e2", llm.RoleAssistant, 20, 20),
		msgEntry("e3", llm.RoleUser, 30, 0),
	}
	settings := Settings{reverseTokens: 16384, keepRecentTokens: 25}

	prep, err := PrepareCompact(entries, settings)
	require.NoError(t, err)
	require.Equal(t, 16384, prep.ReserveTokens)

	c := &captureCompactor{truncated: true}
	comp, err := Compact(t.Context(), *prep, c)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "generation hit the token cap")
	assert.Empty(t, comp.Summary)
	assert.Equal(t, []int{13107}, c.maxTokens, "0.8 * reserveTokens")
}

func TestCompact_EmptyHistoryRefreshesFileOperations(t *testing.T) {
	for _, midTurn := range []bool{false, true} {
		name := "history"
		if midTurn {
			name = "mid-turn"
		}
		t.Run(name, func(t *testing.T) {
			previous := session.Compaction{
				Summary: "Keep the public API unchanged.\n\n<read-files>\na.go\nnotes.md\n</read-files>" +
					"\n\n<modified-files>\nold.go\n</modified-files>",
				Details: session.CompactionDetails{
					ReadFiles:     []string{"a.go", "notes.md"},
					ModifiedFiles: []string{"old.go"},
				},
			}
			c := &captureCompactor{text: "Current turn context."}
			for range 3 {
				messages := []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
					{
						Function: llm.Function{
							Name:      "edit",
							Arguments: `{"payload":"*** SM:EDIT a.go\n*** SM:FIND\nx\n*** SM:PUT\ny\n"}`,
						},
					},
					{
						Function: llm.Function{
							Name:      "edit",
							Arguments: `{"payload":"*** SM:EDIT b.go\n*** SM:FIND\nx\n*** SM:PUT\ny\n"}`,
						},
					},
				}}}
				fileOps := extractFileOperations(messages, []session.MessageEntry{
					session.CompactionEntry{Compaction: previous},
				}, 0)
				prep := CompactionPreparation{
					PreviousSummary: previous.Summary,
					PreviousFileOperations: formatFileOperations(
						previous.Details.ReadFiles,
						previous.Details.ModifiedFiles,
					),
					FileOps: *fileOps,
				}
				if midTurn {
					prep.IsMidTurnCut = true
					prep.TurnPrefixMessages = messages
				}

				comp, err := Compact(t.Context(), prep, c)

				require.NoError(t, err)
				assert.Contains(t, comp.Summary, "Keep the public API unchanged.")
				assert.Equal(t, 1, strings.Count(comp.Summary, "<read-files>"))
				assert.Equal(t, 1, strings.Count(comp.Summary, "<modified-files>"))
				assert.True(t, strings.HasSuffix(
					comp.Summary,
					"\n\n<read-files>\nnotes.md\n</read-files>\n\n<modified-files>\na.go\nb.go\nold.go\n</modified-files>",
				))
				assert.Equal(t, []string{"notes.md"}, comp.Details.ReadFiles)
				assert.Equal(t, []string{"a.go", "b.go", "old.go"}, comp.Details.ModifiedFiles)
				previous = comp
			}
			wantCalls := 0
			if midTurn {
				wantCalls = 3
			}
			assert.Len(t, c.prompts, wantCalls)
		})
	}
}

// A mid-turn cut drops the turn prefix as well as the history, and both buckets
// are summarized away. Every file touched in either bucket has to reach the
// file-operation lists: missing the prefix listed the edited files of the cut
// turn as merely read, and dropped their siblings entirely.
func TestPrepareCompact_MidTurnCut_CollectsTurnPrefixFileOps(t *testing.T) {
	entries := []session.MessageEntry{
		msgEntry("e1", llm.RoleUser, 10, 0),
		editEntry("e2", "history.go"),
		msgEntry("e3", llm.RoleUser, 10, 0),
		editEntry("e4", "prefix.go"),
		msgEntry("e5", llm.RoleAssistant, 30, 40),
	}
	settings := Settings{keepRecentTokens: 20}

	prep, err := PrepareCompact(entries, settings)

	require.NoError(t, err)
	require.True(t, prep.IsMidTurnCut)
	require.Equal(t, "e5", prep.FirstKeptEntryId)
	require.Len(t, prep.MessagesToSummarize, 2)
	require.Len(t, prep.TurnPrefixMessages, 2)
	assert.Equal(t, []string{"history.go", "prefix.go"}, prep.FileOps.edited)
}

// The file block reaches the persisted summary separated by exactly one blank
// line, and Details carries the same lists for the transcript.
func TestCompact_AppendsFileOpsBlock(t *testing.T) {
	entries := []session.MessageEntry{
		msgEntry("e1", llm.RoleUser, 10, 0),
		editEntry("e2", "history.go"),
		msgEntry("e3", llm.RoleUser, 10, 0),
		editEntry("e4", "prefix.go"),
		msgEntry("e5", llm.RoleAssistant, 30, 40),
	}
	settings := Settings{reverseTokens: 16384, keepRecentTokens: 20}
	prep, err := PrepareCompact(entries, settings)
	require.NoError(t, err)

	comp, err := Compact(t.Context(), *prep, &captureCompactor{})

	require.NoError(t, err)
	assert.Contains(t, comp.Summary, "SUMMARY\n\n<modified-files>\nhistory.go\nprefix.go\n</modified-files>")
	assert.NotContains(t, comp.Summary, "\n\n\n\n")
	assert.Equal(t, []string{"history.go", "prefix.go"}, comp.Details.ModifiedFiles)
	assert.Empty(t, comp.Details.ReadFiles)
}
