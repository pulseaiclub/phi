package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/llm/anthropic"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools"
)

func TestSessionAnthropicResumeValidatesPrefix(t *testing.T) {
	for _, changed := range []string{"unchanged", "system", "tools"} {
		t.Run(changed, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			instruction := filepath.Join(dir, "AGENTS.md")
			require.NoError(t, os.WriteFile(instruction, []byte("Original project policy."), 0o600))
			type wireRequest struct {
				System   json.RawMessage   `json:"system"`
				Tools    json.RawMessage   `json:"tools"`
				Messages []json.RawMessage `json:"messages"`
			}
			requests := make(chan wireRequest, 1)
			var turns atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req wireRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				requests <- req
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(
					w,
					`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":"signature-%d"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":"answer"}}

data: {"type":"content_block_stop","index":1}

data: {"type":"message_stop"}

`,
					turns.Add(1),
				)
			}))
			defer srv.Close()
			cfg := llm.ModelConfig{
				API: llm.Anthropic, Name: "claude-test", BaseURL: srv.URL, Think: llm.ThinkConfig{Enabled: true},
			}
			defs := []tools.Tool{{Definition: llm.ToolDefinition{
				Name: "read", Description: "Original tool description", Params: &llm.FunctionParameters{Type: "object"},
			}}}
			sess, err := NewSession(WithCwd(dir), WithSessionDir(dir), WithPersist(true))
			require.NoError(t, err)
			respond := func(s *Session, input string) wireRequest {
				engine, err := NewEngine(cfg, s, WithTools(defs))
				require.NoError(t, err)
				for _, streamErr := range engine.Loop(t.Context(), input, LoopOpts{}) {
					require.NoError(t, streamErr)
				}
				return <-requests
			}
			before := respond(sess, "first")
			require.NotNil(t, sess.BuildContext()[1].Native)
			require.NotEmpty(t, sess.BuildContext()[1].Native.Prefix)
			if changed == "system" {
				require.NoError(t, os.WriteFile(instruction, []byte("Revised project policy."), 0o600))
			}
			if changed == "tools" {
				defs[0].Definition.Description = "Revised tool description"
			}
			resumed, err := NewSession(WithResumePath(sess.File()))
			require.NoError(t, err)
			after := respond(resumed, "second")
			require.Len(t, after.Messages, 3)
			switch changed {
			case "unchanged":
				assert.Equal(t, before.System, after.System)
				assert.Equal(t, before.Tools, after.Tools)
				assert.Contains(t, string(after.Messages[1]), "signature-1")
			case "system":
				require.NotEqual(t, before.System, after.System)
				assert.NotContains(t, string(after.Messages[1]), "signature-1")
			case "tools":
				require.NotEqual(t, before.Tools, after.Tools)
				assert.NotContains(t, string(after.Messages[1]), "signature-1")
			}
			assert.Contains(t, string(after.Messages[1]), "answer")
			resumed, err = NewSession(WithResumePath(resumed.File()))
			require.NoError(t, err)
			next := respond(resumed, "third")
			require.Len(t, next.Messages, 5)
			assert.Contains(t, string(next.Messages[3]), "signature-2", "fresh thinking survives another restart")
			assert.NotNil(t, resumed.BuildContext()[1].Native, "projection must not mutate saved branch history")
		})
	}
}

func TestSessionAnthropicThinkingRoundTrip(t *testing.T) {
	const stream = `data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":" plan ","signature":""}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"\nstep one"}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig/"}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"one=="}}

data: {"type":"content_block_stop","index":0}

data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"before"}}

data: {"type":"content_block_stop","index":1}

data: {"type":"content_block_start","index":2,"content_block":{"type":"redacted_thinking","data":"opaque/+=="}}

data: {"type":"content_block_stop","index":2}

data: {"type":"content_block_start","index":3,"content_block":{"type":"thinking","thinking":""}}

data: {"type":"content_block_delta","index":3,"delta":{"type":"signature_delta","signature":"empty-text-signature"}}

data: {"type":"content_block_stop","index":3}

data: {"type":"content_block_start","index":4,"content_block":{"type":"tool_use","id":"toolu_read","name":"read","input":{}}}

data: {"type":"content_block_delta","index":4,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}

data: {"type":"content_block_delta","index":4,"delta":{"type":"input_json_delta","partial_json":"\"a.go\"}"}}

data: {"type":"content_block_stop","index":4}

data: {"type":"content_block_start","index":5,"content_block":{"type":"text","text":""}}

data: {"type":"content_block_delta","index":5,"delta":{"type":"text_delta","text":"after"}}

data: {"type":"content_block_stop","index":5}

data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":12}}

data: {"type":"message_stop"}

`
	requests := make(chan []json.RawMessage, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []json.RawMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- req.Messages
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, stream)
	}))
	defer srv.Close()
	cfg := llm.ModelConfig{
		API: llm.Anthropic, Name: "claude-test", BaseURL: srv.URL,
		Think: llm.ThinkConfig{Enabled: true, Mode: llm.Medium},
	}
	client := llmclient.NewClient(cfg, llmclient.Hooks{}, nil, "")
	dir := t.TempDir()
	sess, err := NewSession(WithCwd(dir), WithSessionDir(dir), WithPersist(true))
	require.NoError(t, err)
	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleUser, Content: "read a.go"}))
	var final *llm.Message
	for event, streamErr := range client.Stream(t.Context(), sess.BuildContext()) {
		require.NoError(t, streamErr)
		if event.Final != nil {
			final = event.Final
		}
	}
	<-requests
	require.NotNil(t, final)
	require.NoError(t, sess.Append(*final))
	resumed, err := NewSession(WithResumePath(sess.File()))
	require.NoError(t, err)
	msgs := resumed.BuildContext()
	require.Len(t, msgs, 2)
	require.NotNil(t, final.Native)
	require.Equal(t, final.Native, msgs[1].Native, "native bytes must survive JSONL persistence")
	assert.Equal(t, "beforeafter", msgs[1].Content)
	assert.Equal(t, " plan \nstep one", msgs[1].ReasoningContent)
	stored, err := json.Marshal(msgs[1])
	require.NoError(t, err)
	assert.Contains(t, string(stored), `"native":`)
	fork, err := resumed.Fork()
	require.NoError(t, err)
	require.NoError(t, fork.Append(llm.Message{Role: llm.RoleUser, Content: "fork only"}))
	forkRestored, err := NewSession(WithResumePath(fork.File()))
	require.NoError(t, err)
	assert.Equal(t, final.Native, forkRestored.BuildContext()[1].Native)
	assert.Len(t, resumed.BuildContext(), 2, "fork writes must not change the parent context")
	require.NoError(
		t,
		resumed.Append(llm.Message{Role: llm.RoleTool, ToolCallID: "toolu_read", Content: "package main"}),
	)
	for _, streamErr := range client.Stream(t.Context(), resumed.BuildContext()) {
		require.NoError(t, streamErr)
	}
	wire := <-requests
	require.Len(t, wire, 3)
	const expected = `{"role":"assistant","content":[
		{"type":"thinking","thinking":" plan \nstep one","signature":"sig/one=="},
		{"type":"text","text":"before"},
		{"type":"redacted_thinking","data":"opaque/+=="},
		{"type":"thinking","thinking":"","signature":"empty-text-signature"},
		{"type":"tool_use","id":"toolu_read","name":"read","input":{"path":"a.go"}},
		{"type":"text","text":"after"}
	]}`
	assert.JSONEq(t, expected, string(wire[1]))
	var replay struct {
		Content []json.RawMessage `json:"content"`
	}
	require.NoError(t, json.Unmarshal(wire[1], &replay))
	assert.Equal(t, final.Native.Items, replay.Content)
	assert.Contains(t, string(wire[2]), `"tool_use_id":"toolu_read"`)
	assert.NotContains(t, string(wire[0])+string(wire[1])+string(wire[2]), `"native"`)
}

func TestSessionAnthropicNativeOnlyAndLegacy(t *testing.T) {
	for _, tt := range []struct {
		name  string
		block string
	}{
		{"thinking only", `{"type":"thinking","thinking":"display","signature":"signed"}`},
		{"signature only", `{"type":"thinking","thinking":"","signature":"signed"}`},
		{"redacted only", `{"type":"redacted_thinking","data":"opaque"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bodies := make(chan json.RawMessage, 2)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				bodies <- body
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":%s}\n\n"+
					"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n"+
					"data: {\"type\":\"message_stop\"}\n\n", tt.block)
			}))
			defer srv.Close()
			cfg := llm.ModelConfig{
				API:     llm.Anthropic,
				Name:    "claude-test",
				BaseURL: srv.URL,
				Think:   llm.ThinkConfig{Enabled: true, Mode: llm.Medium},
			}
			client := llmclient.NewClient(cfg, llmclient.Hooks{}, nil, "")
			dir := t.TempDir()
			sess, err := NewSession(WithCwd(dir), WithSessionDir(dir), WithPersist(true))
			require.NoError(t, err)
			require.NoError(t, sess.AddUser("think"))
			var final *llm.Message
			for ev, streamErr := range client.Stream(t.Context(), sess.BuildContext()) {
				require.NoError(t, streamErr)
				if ev.Final != nil {
					final = ev.Final
				}
			}
			require.NotNil(t, final)
			require.NotNil(t, final.Native)
			require.Empty(t, final.Content)
			require.Empty(t, final.ToolCalls)
			require.NoError(t, sess.Append(*final))
			<-bodies
			resumed, err := NewSession(WithResumePath(sess.File()))
			require.NoError(t, err)
			require.NoError(t, resumed.AddUser("continue"))
			req := anthropic.BuildRequest(cfg, "", resumed.BuildContext(), nil)
			require.Len(t, req.Messages, 3)
			wire, err := json.Marshal(req.Messages[1].Content)
			require.NoError(t, err)
			assert.JSONEq(t, "["+tt.block+"]", string(wire))

			// A source switch is judged after hooks: the thinking-only assistant
			// is filtered from the wire, and the saved state survives untouched.
			switched := cfg
			switched.Name = "other-model"
			req = anthropic.BuildRequest(switched, "", resumed.BuildContext(), nil)
			require.Len(t, req.Messages, 3, "BuildRequest keeps the candidate")
			for _, streamErr := range anthropic.Stream(t.Context(), srv.Client(), switched, &req) {
				require.NoError(t, streamErr)
			}
			var sent struct {
				Messages []json.RawMessage `json:"messages"`
			}
			require.NoError(t, json.Unmarshal(<-bodies, &sent))
			assert.Len(t, sent.Messages, 2, "a model mismatch filters an otherwise empty assistant")
			assert.NotNil(t, resumed.BuildContext()[1].Native, "filtering must not erase saved state")

			helper, err := NewSession()
			require.NoError(t, err)
			require.NoError(t, helper.AddFinalAssistant(final))
			require.Len(t, helper.BuildContext(), 1)
			assert.Equal(t, final.Native, helper.BuildContext()[0].Native)
		})
	}

	dir := t.TempDir()
	legacy, err := NewSession(WithCwd(dir), WithSessionDir(dir), WithPersist(true))
	require.NoError(t, err)
	require.NoError(t, legacy.Append(
		llm.Message{Role: llm.RoleUser, Content: "hello"},
		llm.Message{Role: llm.RoleAssistant, Content: "old answer", ReasoningContent: "display only"},
	))
	resumed, err := NewSession(WithResumePath(legacy.File()))
	require.NoError(t, err)
	req := anthropic.BuildRequest(llm.ModelConfig{Name: "claude-test"}, "", resumed.BuildContext(), nil)
	require.Len(t, req.Messages, 2)
	assert.Equal(t, "old answer", req.Messages[1].Content)
	assert.Nil(t, resumed.BuildContext()[1].Native)
}

func TestSessionAnthropicCompactionDropsOnlyOldNative(t *testing.T) {
	requests := make(chan json.RawMessage, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- body
		source := "old"
		if strings.Contains(string(body), "first summary") {
			source = "new"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(
			w,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"display","signature":"%s-signature"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"%s-redacted"}}

data: {"type":"content_block_stop","index":1}

data: {"type":"content_block_start","index":2,"content_block":{"type":"text","text":"answer"}}

data: {"type":"content_block_stop","index":2}

data: {"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"call|special","name":"read","input":{"path":"a.go"}}}

data: {"type":"content_block_stop","index":3}

data: {"type":"message_stop"}

`,
			source,
			source,
		)
	}))
	defer srv.Close()
	cfg := llm.ModelConfig{
		API:     llm.Anthropic,
		Name:    "claude-test",
		BaseURL: srv.URL,
		Think:   llm.ThinkConfig{Enabled: true, Mode: llm.Medium},
	}
	client := llmclient.NewClient(cfg, llmclient.Hooks{}, nil, "")
	dir := t.TempDir()
	sess, err := NewSession(WithCwd(dir), WithSessionDir(dir), WithPersist(true))
	require.NoError(t, err)
	require.NoError(t, sess.AddUser("summarized away"))
	require.NoError(t, sess.AddUser("keep this turn"))
	keptID := sess.LastID()
	respond := func(s *Session) *llm.Message {
		var final *llm.Message
		for ev, streamErr := range client.Stream(t.Context(), s.BuildContext()) {
			require.NoError(t, streamErr)
			if ev.Final != nil {
				final = ev.Final
			}
		}
		require.NotNil(t, final)
		require.NotNil(t, final.Native)
		return final
	}
	old := respond(sess)
	<-requests
	require.NoError(t, sess.Append(*old))
	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleTool, ToolCallID: "call|special", Content: "old result"}))
	branch, err := sess.Fork()
	require.NoError(t, err)
	require.NoError(t, sess.AppendCompaction(session.Compaction{Summary: "first summary", FirstKeptEntryID: keptID}))
	sess, err = NewSession(WithResumePath(sess.File()))
	require.NoError(t, err)
	msgs := sess.BuildContext()
	require.Len(t, msgs, 4)
	assert.Contains(t, msgs[0].Content, "first summary")
	assert.Nil(t, msgs[2].Native)
	assert.Equal(t, "answer", msgs[2].Content)
	assert.Equal(t, "display", msgs[2].ReasoningContent)
	require.Len(t, msgs[2].ToolCalls, 1)
	assert.Equal(t, old.ToolCalls, msgs[2].ToolCalls)
	assert.NotNil(t, branch.BuildContext()[2].Native, "a branch before compaction keeps its original prefix")
	assert.Nil(t, sess.BuildContext()[2].Native, "the cached projection must also omit stale native state")

	fresh := respond(sess)
	wire := string(<-requests)
	assert.NotContains(t, wire, "old-signature")
	assert.NotContains(t, wire, "old-redacted")
	assert.Contains(t, wire, `"text":"answer"`)
	assert.Contains(t, wire, `"type":"tool_use"`)
	assert.Contains(t, wire, `"type":"tool_result"`)
	require.NoError(t, sess.Append(*fresh))
	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleTool, ToolCallID: "call|special", Content: "new result"}))
	sess, err = NewSession(WithResumePath(sess.File()))
	require.NoError(t, err)
	msgs = sess.BuildContext()
	require.Len(t, msgs, 6)
	assert.Nil(t, msgs[2].Native)
	assert.Equal(t, fresh.Native, msgs[4].Native, "thinking generated after compaction must remain replayable")
	req := anthropic.BuildRequest(cfg, "", msgs, nil)
	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "old-signature")
	assert.Contains(t, string(body), "new-signature")
	assert.Contains(t, string(body), "new-redacted")
	assert.Contains(t, string(body), `"tool_use_id":"call|special"`)

	require.NoError(t, sess.AppendCompaction(session.Compaction{Summary: "second summary", FirstKeptEntryID: keptID}))
	sess, err = NewSession(WithResumePath(sess.File()))
	require.NoError(t, err)
	for _, msg := range sess.BuildContext() {
		assert.Nil(t, msg.Native, "a later compaction invalidates all thinking from before its checkpoint")
	}
	var saved []*llm.NativeState
	for _, entry := range sess.manager.GetBranch(sess.LastID()) {
		if m, ok := entry.(session.SessionMessageEntry); ok && m.Message.Native != nil {
			saved = append(saved, m.Message.Native)
		}
	}
	require.Len(t, saved, 2)
	assert.Equal(t, fresh.Native, saved[0])
	assert.Equal(t, old.Native, saved[1], "context projection must not mutate the saved branch history")
}

// fixedAliasModel rewrites every request to one model, the way an alias hook
// configured by an embedder would.
type fixedAliasModel string

func (model fixedAliasModel) Before(
	_ context.Context, req *anthropic.AnthropicRequest, _ llm.ModelConfig,
) error {
	req.Model = string(model)
	return nil
}

func TestSessionAnthropicAliasHookSurvivesRestore(t *testing.T) {
	type wireRequest struct {
		Model    string            `json:"model"`
		Messages []json.RawMessage `json:"messages"`
	}
	requests := make(chan wireRequest, 2)
	var turns atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req wireRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- req
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(
			w,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":"signature-%d"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":"answer"}}

data: {"type":"content_block_stop","index":1}

data: {"type":"message_stop"}

`,
			turns.Add(1),
		)
	}))
	defer srv.Close()
	cfg := llm.ModelConfig{
		API: llm.Anthropic, Name: "alias-a", BaseURL: srv.URL,
		Think: llm.ThinkConfig{Enabled: true, Mode: llm.Medium},
	}
	hooks := llmclient.Hooks{Anthropic: fixedAliasModel("model-b")}
	dir := t.TempDir()
	sess, err := NewSession(WithCwd(dir), WithSessionDir(dir), WithPersist(true))
	require.NoError(t, err)
	respond := func(s *Session, input string) wireRequest {
		engine, err := NewEngine(cfg, s, WithHooks(hooks))
		require.NoError(t, err)
		for _, loopErr := range engine.Loop(t.Context(), input, LoopOpts{}) {
			require.NoError(t, loopErr)
		}
		return <-requests
	}

	// The alias reaches the wire, and the captured state records the model
	// that actually served it.
	first := respond(sess, "first")
	assert.Equal(t, "model-b", first.Model)
	require.NotNil(t, sess.BuildContext()[1].Native)
	assert.Equal(t, "model-b", sess.BuildContext()[1].Native.Model)
	assert.NotEmpty(t, sess.BuildContext()[1].Native.Prefix)

	// After save and restore, the rewritten request still matches the state's
	// model, so the signed history replays.
	resumed, err := NewSession(WithResumePath(sess.File()))
	require.NoError(t, err)
	second := respond(resumed, "second")
	require.Len(t, second.Messages, 3)
	assert.Contains(t, string(second.Messages[1]), "signature-1",
		"restored history keeps its signature under the alias hook")
	assert.Contains(t, string(second.Messages[1]), "answer")
}
