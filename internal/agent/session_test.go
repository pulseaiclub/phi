package agent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/job"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/session"
)

func TestSessionPersistFlush(t *testing.T) {
	dir := t.TempDir()
	sess, err := NewSession(
		WithCwd(dir),
		WithSessionDir(dir),
		WithPersist(true),
	)
	require.NoError(t, err)
	assert.NotEmpty(t, sess.ID())
	assert.NotEmpty(t, sess.File())

	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleUser, Content: "hi"}))
	_, err = os.Stat(sess.File())
	assert.True(t, os.IsNotExist(err), "should not flush before first assistant")

	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleAssistant, Content: "hello"}))
	require.FileExists(t, sess.File())

	b, err := os.ReadFile(sess.File())
	require.NoError(t, err)
	var header struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	first := splitFirstJSONL(b)
	require.NoError(t, json.Unmarshal(first, &header))
	assert.Equal(t, "EntrySession", header.Type)
	assert.Equal(t, sess.ID(), header.ID)
}

func TestSessionPersistFalseNoDisk(t *testing.T) {
	sess, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	assert.Empty(t, sess.File())
	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleUser, Content: "a"}))
	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleAssistant, Content: "b"}))
	assert.Empty(t, sess.File())
}

func TestEngineSetModelKeepsSession(t *testing.T) {
	dir := t.TempDir()
	sess, err := NewSession(
		WithCwd(dir),
		WithSessionDir(dir),
		WithPersist(true),
	)
	require.NoError(t, err)
	eng, err := NewEngine(
		llm.ModelConfig{Name: "model-a", APIKey: "k", BaseURL: "http://example"},
		sess,
	)
	require.NoError(t, err)

	id := eng.SessionID()
	file := eng.SessionFile()
	require.NotEmpty(t, id)
	require.NotEmpty(t, file)

	require.NoError(t, eng.session.Append(llm.Message{Role: llm.RoleUser, Content: "keep me"}))
	require.NoError(t, eng.session.Append(llm.Message{Role: llm.RoleAssistant, Content: "ok"}))
	n := eng.session.Len()

	eng.SetModel(llm.ModelConfig{
		Name:          "model-b",
		APIKey:        "k",
		BaseURL:       "http://example",
		ContextWindow: 8192,
		SkillPaths:    []string{dir},
	})
	assert.Equal(t, id, eng.SessionID())
	assert.Equal(t, file, eng.SessionFile())
	assert.Equal(t, n, eng.session.Len())
	assert.Equal(t, 8192, eng.modelCfg.ContextWindow)
	assert.Equal(t, []string{dir}, eng.modelCfg.SkillPaths)
}

func TestSessionBuildContextLabelsCompactionSummary(t *testing.T) {
	dir := t.TempDir()
	sess, err := NewSession(WithCwd(dir), WithSessionDir(dir))
	require.NoError(t, err)

	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleUser, Content: "first"}))
	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleAssistant, Content: "answer"}))
	_, err = sess.AppendCompaction(session.Compaction{Summary: "we renamed the SDK"})
	require.NoError(t, err)
	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleUser, Content: "carry on"}))

	msgs := sess.BuildContext()
	require.Len(t, msgs, 2)
	assert.Equal(t, llm.RoleUser, msgs[0].Role)
	assert.Equal(
		t,
		"The conversation history before this point was compacted into the following summary:\n\n<summary>\nwe renamed the SDK\n</summary>",
		msgs[0].Content,
	)
	assert.Equal(t, "carry on", msgs[1].Content)

	// The entry keeps the raw summary: the next round feeds it back to the
	// summarizer as PreviousSummary, which must not see the label.
	found := false
	for _, entry := range sess.PathEntries() {
		if ce, ok := entry.(session.CompactionEntry); ok {
			assert.Equal(t, "we renamed the SDK", ce.Compaction.Summary)
			found = true
		}
	}
	assert.True(t, found, "compaction entry missing from the path")
}

func splitFirstJSONL(b []byte) []byte {
	for i, c := range b {
		if c == '\n' {
			return b[:i]
		}
	}
	return b
}

func TestSessionModelSelectionAdvancesCursor(t *testing.T) {
	sess, err := NewSession(WithModel("initial"))
	require.NoError(t, err)
	assert.Equal(t, "initial", sess.ModelName())
	require.NotEmpty(t, sess.LastID(), "the initial model is a session entry")
	require.NoError(t, sess.AddUser("hello"))
	before := sess.LastID()
	context := sess.BuildContext()
	require.NoError(t, sess.SetModel("next"))
	assert.NotEqual(t, before, sess.LastID(), "compaction must detect model changes while summarizing")
	assert.Equal(t, sess.manager.LeafID(), sess.LastID())
	assert.Equal(t, context, sess.BuildContext(), "model selection must not add an LLM message")
	leaf, count := sess.LastID(), sess.Len()
	require.NoError(t, sess.SetModel("next"))
	assert.Equal(t, leaf, sess.LastID())
	assert.Equal(t, count, sess.Len())
	require.NoError(t, sess.AddUser("continue"))
	assert.Equal(t, "next", sess.ModelName())
	require.Len(t, sess.BuildContext(), 2)
}

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
