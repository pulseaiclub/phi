package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/session"
)

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
	require.NoError(t, sess.AppendCompaction(session.Compaction{
		Summary: "summary", FirstKeptEntryID: keep,
	}))
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
