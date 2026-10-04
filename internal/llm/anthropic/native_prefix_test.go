package anthropic

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

func TestNativePrefixChecksWireHistory(t *testing.T) {
	for _, tt := range []struct {
		name       string
		change     func(*AnthropicRequest)
		keepFirst  bool
		keepSecond bool
		// noFreshReplay marks rows where the request stays thinking-disabled,
		// so freshly captured thinking cannot replay on the following send.
		noFreshReplay bool
	}{
		{"unchanged hooked request", func(*AnthropicRequest) {}, true, true, false},
		{"system", func(r *AnthropicRequest) { r.System[0].Text = "new policy" }, false, false, false},
		{"tool description", func(r *AnthropicRequest) { r.Tools[0].Description = "new description" }, false, false, false},
		{"tool schema", func(r *AnthropicRequest) { r.Tools[0].InputSchema = json.RawMessage(`{"type":"object"}`) }, false, false, false},
		{"schema JSON formatting", func(r *AnthropicRequest) {
			r.Tools[0].InputSchema = json.RawMessage(`{ "properties": { "counter": {"type": "integer"} }, "type": "object" }`)
		}, true, true, false},
		{"user text", func(r *AnthropicRequest) { r.Messages[0].Content.([]anthropicContentBlock)[0].Text = "changed" }, false, false, false},
		{"image", func(r *AnthropicRequest) {
			r.Messages[0].Content.([]anthropicContentBlock)[1].Source.Data = "changed-image"
		}, false, false, false},
		{"tool result", func(r *AnthropicRequest) {
			r.Messages[2].Content.([]anthropicContentBlock)[0].Content = "changed result"
		}, true, false, false},
		{"large integer tool input", func(r *AnthropicRequest) {
			r.Messages[1].Content.([]json.RawMessage)[3] = json.RawMessage(`{"type":"tool_use","id":"call|special","name":"read","input":{"counter":9007199254740993,"cache_control":"value"}}`)
		}, false, false, false},
		{"cache_control inside tool input", func(r *AnthropicRequest) {
			r.Messages[1].Content.([]json.RawMessage)[3] = json.RawMessage(`{"type":"tool_use","id":"call|special","name":"read","input":{"counter":9007199254740992,"cache_control":"changed"}}`)
		}, false, false, false},
		{"cache markers and output budget", func(r *AnthropicRequest) {
			r.System[0].CacheControl = nil
			r.Tools[0].CacheControl = nil
			r.Messages[0].Content.([]anthropicContentBlock)[0].CacheControl = &cacheControl{Type: "ephemeral"}
			r.Messages[4].Content.([]anthropicContentBlock)[0].CacheControl = nil
			r.MaxTokens++
		}, true, true, false},
		{"missing saved prefix", func(r *AnthropicRequest) {
			state := *r.nativeMessages[1]
			state.Prefix = ""
			r.nativeMessages[1] = &state
		}, false, false, false},
		{"replayed state model", func(r *AnthropicRequest) {
			state := *r.nativeMessages[1]
			state.Model = "other-model"
			r.nativeMessages[1] = &state
		}, false, false, false},
		{"thinking budget", func(r *AnthropicRequest) {
			budget := 4096
			r.Thinking.BudgetTokens = &budget
		}, true, true, false},
		{"cleared thinking parameter", func(r *AnthropicRequest) { r.Thinking = nil }, true, true, false},
		{"explicitly disabled thinking", func(r *AnthropicRequest) {
			r.Thinking = nil
			r.thinkingRequested = false
		}, false, false, true},
		{"hook disables thinking by type", func(r *AnthropicRequest) {
			r.Thinking.Type = "disabled"
			r.Thinking.BudgetTokens = nil
		}, false, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bodies := make(chan json.RawMessage, 1)
			var turns atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				bodies <- body
				n := turns.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, thinkingStream(
					fmt.Sprintf(
						`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"display","signature":"signature-%d"}}`,
						n,
					),
					`{"type":"content_block_stop","index":0}`,
					fmt.Sprintf(
						`{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"encrypted-%d"}}`,
						n,
					),
					`{"type":"content_block_stop","index":1}`,
					`{"type":"content_block_start","index":2,"content_block":{"type":"text","text":"before"}}`,
					`{"type":"content_block_stop","index":2}`,
					`{"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"call|special","name":"read","input":{"counter":9007199254740992,"cache_control":"value"}}}`,
					`{"type":"content_block_stop","index":3}`,
					`{"type":"content_block_start","index":4,"content_block":{"type":"text","text":"after"}}`,
					`{"type":"content_block_stop","index":4}`,
					`{"type":"message_stop"}`,
				))
			}))
			defer srv.Close()
			cfg := llm.ModelConfig{
				API:     llm.Anthropic,
				Name:    "claude-test",
				BaseURL: srv.URL,
				Think:   llm.ThinkConfig{Enabled: true, Mode: llm.Medium},
			}
			defs := []llm.ToolDefinition{{Name: "read", Params: &llm.FunctionParameters{Type: "object"}}}
			messages := []llm.Message{
				{Role: llm.RoleUser, Content: "question", Images: []llm.Image{{MimeType: "image/png", Data: "image"}}},
			}
			send := func(change func(*AnthropicRequest)) string {
				req := BuildRequest(cfg, "configured policy", messages, defs)
				// A stable hook changes the configured prefix on every request.
				req.System[0].Text = "wire policy"
				req.Tools[0].Description = "wire description"
				req.Tools[0].InputSchema = json.RawMessage(
					`{"type":"object","properties":{"counter":{"type":"integer"}}}`,
				)
				if change != nil {
					change(&req)
				}
				var final *llm.Message
				for ev, err := range Stream(t.Context(), srv.Client(), cfg, &req) {
					require.NoError(t, err)
					if ev.Final != nil {
						final = ev.Final
					}
				}
				require.NotNil(t, final)
				require.NotNil(t, final.Native)
				require.NotEmpty(t, final.Native.Prefix)
				messages = append(
					messages,
					*final,
					llm.Message{Role: llm.RoleTool, ToolCallID: "call|special", Content: "result"},
				)
				return string(<-bodies)
			}
			send(nil)
			assert.Contains(t, send(nil), "signature-1", "ordinary tool continuation keeps its signed thinking")
			saved, err := json.Marshal(messages)
			require.NoError(t, err)
			wire := send(tt.change)
			for i, kept := range []bool{tt.keepFirst, tt.keepSecond} {
				for _, prefix := range []string{"signature", "encrypted"} {
					value := fmt.Sprintf("%s-%d", prefix, i+1)
					if kept {
						assert.Contains(t, wire, value)
					} else {
						assert.NotContains(t, wire, value)
					}
				}
			}
			var body struct {
				Messages []struct {
					Content []struct {
						Type      string `json:"type"`
						Text      string `json:"text"`
						ID        string `json:"id"`
						ToolUseID string `json:"tool_use_id"`
					} `json:"content"`
				} `json:"messages"`
			}
			require.NoError(t, json.Unmarshal([]byte(wire), &body))
			require.Len(t, body.Messages, 5)
			for i, kept := range []bool{tt.keepFirst, tt.keepSecond} {
				if kept {
					continue
				}
				blocks := body.Messages[1+2*i].Content
				require.Len(t, blocks, 3)
				assert.Equal(t, "before", blocks[0].Text)
				assert.Equal(t, "tool_use", blocks[1].Type)
				assert.Equal(t, "after", blocks[2].Text)
				assert.Equal(t, blocks[1].ID, body.Messages[2+2*i].Content[0].ToolUseID)
			}
			after, err := json.Marshal(messages[:5])
			require.NoError(t, err)
			assert.JSONEq(t, string(saved), string(after), "request filtering must not edit session history")
			assert.NotContains(t, wire, `"prefix"`)
			next := send(tt.change)
			if tt.noFreshReplay {
				assert.NotContains(t, next, "signature", "a disabled request filters all thinking")
			} else {
				assert.Contains(
					t,
					next,
					"signature-3",
					"thinking generated against the edited prefix remains valid",
				)
			}
		})
	}
}

func TestNativePrefixDropsEmptyAssistant(t *testing.T) {
	cfg := llm.ModelConfig{Name: "claude-test", Think: llm.ThinkConfig{Enabled: true}}
	state := &llm.NativeState{
		Version: nativeStateVersion, API: llm.Anthropic, Model: cfg.Name,
		Endpoint: endpointFingerprint(cfg.BaseURL), Prefix: "old-prefix",
		Items: []json.RawMessage{json.RawMessage(`{"type":"thinking","thinking":"","signature":"old-signature"}`)},
	}
	req := BuildRequest(cfg, "new policy", []llm.Message{
		{Role: llm.RoleUser, Content: "first"},
		{Role: llm.RoleAssistant, Native: state},
		{Role: llm.RoleUser, Content: "second"},
	}, nil)
	_, err := req.prepareNative()
	require.NoError(t, err)
	require.Len(t, req.Messages, 2)
	assert.Equal(t, "first", req.Messages[0].Content)
	assert.Equal(t, "second", req.Messages[1].Content.([]anthropicContentBlock)[0].Text)
	assert.Equal(t, "user", req.Messages[0].Role)
	assert.Equal(t, "user", req.Messages[1].Role)
}
