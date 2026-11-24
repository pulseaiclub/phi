package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/job"

	"github.com/pulseaiclub/phi/internal/llm"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
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
	_, err = s.AppendCompaction(session.Compaction{Summary: "Keep the approved design"})
	require.NoError(t, err)
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

func TestSessionAnthropicContinuation(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"opaque+=="}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"encrypted+=="}}`,
		`data: {"type":"content_block_stop","index":1}`,
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"text","text":"checking"}}`,
		`data: {"type":"content_block_stop","index":2}`,
		`data: {"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu.original","name":"read","input":{}}}`,
		`data: {"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.go\"}"}}`,
		`data: {"type":"content_block_stop","index":3}`,
		`data: {"type":"message_stop"}`,
	}, "\n\n") + "\n\n"
	requests := make(chan []byte, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	defer server.Close()
	cfg := llm.ModelConfig{
		Name: "claude", API: llm.Anthropic, APIKey: "test", BaseURL: server.URL,
		Think: llm.ThinkConfig{Enabled: true},
	}
	client := llmclient.NewClient(cfg, llmclient.Hooks{}, nil, "system")
	dir := t.TempDir()
	sess, err := NewSession(WithCwd(dir), WithSessionDir(dir), WithPersist(true))
	require.NoError(t, err)
	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleUser, Content: "read a.go"}))
	var final *llm.Message
	for ev, streamErr := range client.Stream(t.Context(), sess.BuildContext()) {
		require.NoError(t, streamErr)
		if ev.Final != nil {
			final = ev.Final
		}
	}
	<-requests
	require.NotNil(t, final)
	require.NotNil(t, final.ProviderState)
	require.Len(t, final.ProviderState.Data.Items, 4)
	require.NoError(t, sess.Append(*final))
	resumed, err := NewSession(WithResumePath(sess.File()))
	require.NoError(t, err)
	history := resumed.BuildContext()
	require.Len(t, history, 2)
	assert.Equal(t, final.ProviderState, history[1].ProviderState)
	assert.Equal(t, final.Content, history[1].Content)
	assert.Equal(t, final.ToolCalls, history[1].ToolCalls)
	require.NoError(t, resumed.Append(llm.Message{
		Role: llm.RoleTool, ToolCallID: final.ToolCalls[0].ID, Content: "package main",
	}))
	for _, streamErr := range client.Stream(t.Context(), resumed.BuildContext()) {
		require.NoError(t, streamErr)
	}
	var wire struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	body := <-requests
	require.NoError(t, json.Unmarshal(body, &wire))
	require.Len(t, wire.Messages, 3)
	var items []json.RawMessage
	require.NoError(t, json.Unmarshal(wire.Messages[1].Content, &items))
	require.Len(t, items, 4)
	for i, item := range final.ProviderState.Data.Items {
		assert.JSONEq(t, string(item), string(items[i]))
	}
	assert.Equal(t, "assistant", wire.Messages[1].Role)
	assert.JSONEq(
		t,
		`[{"type":"tool_result","tool_use_id":"toolu.original","content":"package main","cache_control":{"type":"ephemeral","ttl":"1h"}}]`,
		string(wire.Messages[2].Content),
	)
}

func TestSessionNativeCompaction(t *testing.T) {
	dir := t.TempDir()
	sess, err := NewSession(WithCwd(dir), WithSessionDir(dir), WithPersist(true))
	require.NoError(t, err)
	require.NoError(t, sess.AddUser("old"))
	state := &llm.ProviderState{Provider: llm.Anthropic, Data: llm.ProviderData{Items: []json.RawMessage{
		json.RawMessage(`{"type":"thinking","thinking":"","signature":"signed"}`),
	}}}
	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleAssistant, ProviderState: state}))
	keep := sess.LastID()
	require.NoError(t, sess.SetModel("before-compaction"))
	_, err = sess.AppendCompaction(session.Compaction{
		Summary: "summary", FirstKeptEntryID: keep,
	})
	require.NoError(t, err)
	require.NoError(t, sess.SetModel("after-compaction"))
	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleAssistant, ProviderState: state}))
	resumed, err := NewSession(WithResumePath(sess.File()))
	require.NoError(t, err)
	for _, current := range []*Session{sess, resumed} {
		messages := current.BuildContext()
		require.Len(t, messages, 3)
		assert.Contains(t, messages[0].Content, "summary")
		assert.Nil(t, messages[1].ProviderState, "retained provider state belongs to the replaced prefix")
		assert.Equal(t, state, messages[2].ProviderState, "new responses belong to the summary prefix")
		assert.Equal(t, messages, current.BuildContext(), "cached projection must agree")
		entries := current.PathEntries()
		assert.Equal(t, state, entries[1].(session.SessionMessageEntry).Message.ProviderState,
			"projection must leave persisted history intact")
	}
}
