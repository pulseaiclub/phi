package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/llm/anthropic"
)

// fixedModel rewrites every request to one model, the way an alias hook does.
type fixedModel string

func (model fixedModel) Before(_ context.Context, req *anthropic.AnthropicRequest, _ llm.ModelConfig) error {
	req.Model = string(model)
	return nil
}

// clearThinking drops the thinking parameter while leaving replayed history
// intact — the "no explicit thinking config" case, which is not the same as
// an explicitly disabled request.
type clearThinking struct{}

func (clearThinking) Before(_ context.Context, req *anthropic.AnthropicRequest, _ llm.ModelConfig) error {
	req.Thinking = nil
	return nil
}

// disableThinking expresses the off the way a provider-specific hook would:
// thinking.type "disabled" with no budget, leaving the parameter present.
type disableThinking struct{}

func (disableThinking) Before(_ context.Context, req *anthropic.AnthropicRequest, _ llm.ModelConfig) error {
	if req.Thinking != nil {
		req.Thinking.Type = "disabled"
		req.Thinking.BudgetTokens = nil
	}
	return nil
}

type thinkingServer struct {
	srv    *httptest.Server
	bodies chan json.RawMessage
	turns  atomic.Int32
}

// newThinkingServer answers every turn with one signed thinking block, a
// redacted thinking block, a text block and a tool_use block. thinking=false
// serves the same stream without the two thinking blocks.
func newThinkingServer(thinking bool) *thinkingServer {
	ts := &thinkingServer{bodies: make(chan json.RawMessage, 8)}
	ts.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ts.bodies <- body
		n := ts.turns.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		head := ""
		if thinking {
			head = fmt.Sprintf(
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":"signature-%d"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"encrypted-%d"}}

data: {"type":"content_block_stop","index":1}

`,
				n,
				n,
			)
		}
		_, _ = fmt.Fprint(w, head+
			`data: {"type":"content_block_start","index":2,"content_block":{"type":"text","text":"answer"}}

data: {"type":"content_block_stop","index":2}

data: {"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_read","name":"read","input":{"path":"a.go"}}}

data: {"type":"content_block_stop","index":3}

data: {"type":"message_stop"}

`)
	}))
	return ts
}

func (ts *thinkingServer) body() string {
	return string(<-ts.bodies)
}

func finalOf(t *testing.T, events []llm.StreamEvent) *llm.Message {
	t.Helper()
	require.NotEmpty(t, events)
	final := events[len(events)-1].Final
	require.NotNil(t, final)
	return final
}

func TestClientAnthropicModelRewriteCarriesProvenance(t *testing.T) {
	ts := newThinkingServer(true)
	defer ts.srv.Close()
	cfg := llm.ModelConfig{
		API:     llm.Anthropic,
		Name:    "alias-a",
		BaseURL: ts.srv.URL,
		Think:   llm.ThinkConfig{Enabled: true, Mode: llm.Medium},
	}
	alias := NewClient(cfg, Hooks{Anthropic: fixedModel("model-b")}, nil, "")
	messages := []llm.Message{{Role: llm.RoleUser, Content: "think"}}

	// The rewrite is not blocked, and the captured state records the model that
	// actually served the request, with a valid prefix.
	final := finalOf(t, collectEvents(alias.Stream(t.Context(), messages)))
	require.NotNil(t, final.Native)
	assert.Equal(t, "model-b", final.Native.Model)
	assert.NotEmpty(t, final.Native.Prefix)
	var sent struct {
		Model string `json:"model"`
	}
	require.NoError(t, json.Unmarshal([]byte(ts.body()), &sent))
	assert.Equal(t, "model-b", sent.Model)

	// The next request under the same alias matches the state's model against
	// its own rewritten model, so the signed history replays.
	messages = append(messages, *final,
		llm.Message{Role: llm.RoleTool, ToolCallID: "toolu_read", Content: "ok"})
	finalOf(t, collectEvents(alias.Stream(t.Context(), messages)))
	wire := ts.body()
	assert.Contains(t, wire, "signature-1", "the first round's signature must survive the alias")
	assert.Contains(t, wire, `"tool_use_id":"toolu_read"`)
	require.NoError(t, json.Unmarshal([]byte(wire), &sent))
	assert.Equal(t, "model-b", sent.Model)
}

func TestClientAnthropicModelSwitchDropsOnlyThinking(t *testing.T) {
	ts := newThinkingServer(true)
	defer ts.srv.Close()
	cfg := llm.ModelConfig{
		API:     llm.Anthropic,
		Name:    "alias-a",
		BaseURL: ts.srv.URL,
		Think:   llm.ThinkConfig{Enabled: true, Mode: llm.Medium},
	}
	alias := NewClient(cfg, Hooks{Anthropic: fixedModel("model-b")}, nil, "")
	messages := []llm.Message{{Role: llm.RoleUser, Content: "think"}}
	final := finalOf(t, collectEvents(alias.Stream(t.Context(), messages)))
	_ = ts.body()
	messages = append(messages, *final,
		llm.Message{Role: llm.RoleTool, ToolCallID: "toolu_read", Content: "ok"})

	// Repointing the alias at another model filters the thinking it cannot
	// match but keeps text, tool calls, and their pairing.
	repointed := NewClient(cfg, Hooks{Anthropic: fixedModel("model-c")}, nil, "")
	final = finalOf(t, collectEvents(repointed.Stream(t.Context(), messages)))
	wire := ts.body()
	assert.NotContains(t, wire, "signature-")
	assert.Contains(t, wire, `"text":"answer"`)
	assert.Contains(t, wire, `"id":"toolu_read"`)
	assert.Contains(t, wire, `"tool_use_id":"toolu_read"`)
	var sent struct {
		Model string `json:"model"`
	}
	require.NoError(t, json.Unmarshal([]byte(wire), &sent))
	assert.Equal(t, "model-c", sent.Model)
	// New provenance follows the model that served this request.
	require.NotNil(t, final.Native)
	assert.Equal(t, "model-c", final.Native.Model)
}

func TestClientAnthropicClearedThinkingKeepsHistory(t *testing.T) {
	ts := newThinkingServer(true)
	defer ts.srv.Close()
	cfg := llm.ModelConfig{
		API:     llm.Anthropic,
		Name:    "alias-a",
		BaseURL: ts.srv.URL,
		Think:   llm.ThinkConfig{Enabled: true, Mode: llm.Medium},
	}
	hooks := Hooks{Anthropic: clearThinking{}}
	client := NewClient(cfg, hooks, nil, "")
	messages := []llm.Message{{Role: llm.RoleUser, Content: "think"}}
	final := finalOf(t, collectEvents(client.Stream(t.Context(), messages)))
	_ = ts.body()
	messages = append(messages, *final,
		llm.Message{Role: llm.RoleTool, ToolCallID: "toolu_read", Content: "ok"})

	finalOf(t, collectEvents(client.Stream(t.Context(), messages)))
	wire := ts.body()
	// A hook clearing the parameter leaves "no explicit thinking config",
	// which is not an explicit off: the replayed history stays on the wire.
	assert.Contains(t, wire, "signature-1")
	var sent struct {
		Thinking json.RawMessage `json:"thinking"`
	}
	require.NoError(t, json.Unmarshal([]byte(wire), &sent))
	assert.Nil(t, sent.Thinking, "the cleared parameter must stay cleared")
}

func TestClientAnthropicDisabledByTypeFiltersHistory(t *testing.T) {
	ts := newThinkingServer(true)
	defer ts.srv.Close()
	cfg := llm.ModelConfig{
		API:     llm.Anthropic,
		Name:    "alias-a",
		BaseURL: ts.srv.URL,
		Think:   llm.ThinkConfig{Enabled: true, Mode: llm.Medium},
	}
	messages := []llm.Message{{Role: llm.RoleUser, Content: "think"}}
	client := NewClient(cfg, Hooks{}, nil, "")
	final := finalOf(t, collectEvents(client.Stream(t.Context(), messages)))
	require.NotNil(t, final.Native)
	_ = ts.body()
	messages = append(messages, *final,
		llm.Message{Role: llm.RoleTool, ToolCallID: "toolu_read", Content: "ok"})
	saved, err := json.Marshal(messages)
	require.NoError(t, err)

	// Same model, same endpoint; only the hook turns thinking off by type.
	disabled := NewClient(cfg, Hooks{Anthropic: disableThinking{}}, nil, "")
	finalOf(t, collectEvents(disabled.Stream(t.Context(), messages)))
	wire := ts.body()

	var sent struct {
		Thinking struct {
			Type         string `json:"type"`
			BudgetTokens *int   `json:"budget_tokens"`
		} `json:"thinking"`
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal([]byte(wire), &sent))
	assert.Equal(t, "disabled", sent.Thinking.Type, "the hook's disabled type must reach the wire")
	assert.Nil(t, sent.Thinking.BudgetTokens, "a disabled request carries no budget_tokens")

	// The replayed assistant turn keeps text and tool calls, loses both
	// thinking block kinds, and stays paired with its tool result.
	require.Len(t, sent.Messages, 3)
	var blocks []struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		ID        string          `json:"id"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
	}
	require.NoError(t, json.Unmarshal(sent.Messages[1].Content, &blocks))
	require.Len(t, blocks, 2)
	assert.Equal(t, "text", blocks[0].Type)
	assert.Equal(t, "answer", blocks[0].Text)
	assert.Equal(t, "tool_use", blocks[1].Type)
	assert.Equal(t, "toolu_read", blocks[1].ID)
	assert.JSONEq(t, `{"path":"a.go"}`, string(blocks[1].Input))
	var result []struct {
		ToolUseID string `json:"tool_use_id"`
	}
	require.NoError(t, json.Unmarshal(sent.Messages[2].Content, &result))
	require.Len(t, result, 1)
	assert.Equal(t, blocks[1].ID, result[0].ToolUseID)
	assert.NotContains(t, wire, `"type":"thinking"`)
	assert.NotContains(t, wire, `"type":"redacted_thinking"`)

	after, err := json.Marshal(messages)
	require.NoError(t, err)
	assert.JSONEq(t, string(saved), string(after), "wire filtering must not edit input history")
}

func TestClientAnthropicDisabledThinkingSendsPlainRequests(t *testing.T) {
	ts := newThinkingServer(false)
	defer ts.srv.Close()
	cfg := llm.ModelConfig{
		API:     llm.Anthropic,
		Name:    "alias-a",
		BaseURL: ts.srv.URL,
		Think:   llm.ThinkConfig{Enabled: false},
	}
	// No native history and thinking disabled: the rewrite must not block the
	// request in any way.
	alias := NewClient(cfg, Hooks{Anthropic: fixedModel("model-b")}, nil, "")
	final := finalOf(t, collectEvents(alias.Stream(
		t.Context(), []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
	)))
	assert.Nil(t, final.Native)
	var sent struct {
		Model string `json:"model"`
	}
	require.NoError(t, json.Unmarshal([]byte(ts.body()), &sent))
	assert.Equal(t, "model-b", sent.Model)
}
