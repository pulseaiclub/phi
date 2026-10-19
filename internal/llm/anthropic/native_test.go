package anthropic

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

var nativeSSE = strings.Join([]string{
	`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":" start\n","signature":"sig/"}}`,
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"next"}}`,
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"opaque"}}`,
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"+=="}}`,
	`data: {"type":"content_block_stop","index":0}`,
	`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":"before"}}`,
	`data: {"type":"content_block_stop","index":1}`,
	`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu.native","name":"read","input":{}}}`,
	`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.go\",\"cache_control\":9007199254740993}"}}`,
	`data: {"type":"content_block_stop","index":2}`,
	`data: {"type":"content_block_start","index":3,"content_block":{"type":"redacted_thinking","data":"encrypted+==","extra":"keep"}}`,
	`data: {"type":"content_block_stop","index":3}`,
	`data: {"type":"content_block_start","index":4,"content_block":{"type":"text","text":"after"}}`,
	`data: {"type":"content_block_stop","index":4}`,
	`data: {"type":"message_delta","usage":{"output_tokens":12}}`,
	`data: {"type":"message_stop"}`,
}, "\n\n") + "\n\n"

func nativeResponse(t *testing.T, cfg llm.ModelConfig, req AnthropicRequest) llm.Message {
	t.Helper()
	events := processForTest(nativeSSE)
	require.NotEmpty(t, events)
	done := events[len(events)-1]
	require.Equal(t, llm.StreamEventTypeDone, done.Type)
	require.NotNil(t, done.Final.ProviderState)
	state := nativeState(&req, cfg)
	require.NotNil(t, state)
	state.Data.Items = done.Final.ProviderState.Data.Items
	done.Final.ProviderState = state
	return *done.Final
}

func TestNativeContentOrder(t *testing.T) {
	cfg := llm.ModelConfig{Name: "claude", Think: llm.ThinkConfig{Enabled: true}}
	user := llm.Message{Role: llm.RoleUser, Content: "read"}
	msg := nativeResponse(t, cfg, BuildRequest(cfg, "system", []llm.Message{user}, nil))
	assert.Equal(t, " start\nnext", msg.ReasoningContent)
	assert.Equal(t, "beforeafter", msg.Content)
	require.Len(t, msg.ProviderState.Data.Items, 5)
	for i, want := range []string{
		`{"type":"thinking","thinking":" start\nnext","signature":"sig/opaque+=="}`,
		`{"type":"text","text":"before"}`,
		`{"type":"tool_use","id":"toolu.native","name":"read","input":{"path":"a.go","cache_control":9007199254740993}}`,
		`{"type":"redacted_thinking","data":"encrypted+==","extra":"keep"}`,
		`{"type":"text","text":"after"}`,
	} {
		assert.JSONEq(t, want, string(msg.ProviderState.Data.Items[i]))
	}
	var tool struct {
		Input struct {
			CacheControl json.Number `json:"cache_control"`
		} `json:"input"`
	}
	require.NoError(t, json.Unmarshal(msg.ProviderState.Data.Items[2], &tool))
	assert.Equal(t, json.Number("9007199254740993"), tool.Input.CacheControl)
	result := llm.Message{Role: llm.RoleTool, ToolCallID: msg.ToolCalls[0].ID, Content: "file"}
	req := BuildRequest(cfg, "system", []llm.Message{user, msg, result}, nil)
	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"tool_use_id":"toolu.native"`)
	assert.Contains(t, string(body), `"signature":"sig/opaque+=="`)
	assert.NotContains(t, string(body), `"provider_state"`)
	assert.Equal(t, msg.ProviderState.Data.Items, req.Messages[1].Content)
	// Hooks may edit replayed bytes; the saved state must remain intact.
	req.Messages[1].Content.([]json.RawMessage)[0][0] = '['
	var block struct {
		Signature string `json:"signature"`
	}
	require.NoError(t, json.Unmarshal(msg.ProviderState.Data.Items[0], &block))
	assert.Equal(t, "sig/opaque+==", block.Signature)
}

func TestNativePrefixFallback(t *testing.T) {
	cfg := llm.ModelConfig{Name: "claude", Think: llm.ThinkConfig{Enabled: true}}
	tools := []llm.ToolDefinition{{Name: "read", Description: "read a file"}}
	user := llm.Message{Role: llm.RoleUser, Content: "read"}
	msg := nativeResponse(t, cfg, BuildRequest(cfg, "system", []llm.Message{user}, tools))
	// Fingerprint from the eager implementation: saved sessions must still replay.
	assert.Equal(t, "c189bdda1b23f631ec5f22d570231ec608be9405b230cce26749836f24731d43", msg.ProviderState.Data.Prefix)
	result := llm.Message{Role: llm.RoleTool, ToolCallID: msg.ToolCalls[0].ID, Content: "file"}
	cases := []struct {
		name string
		keep bool
	}{
		{"unchanged", true},
		{"system", false},
		{"tools", false},
		{"history", false},
		{"model", false},
		{"endpoint", false},
		{"normalized endpoint", true},
		{"thinking off", false},
		{"budget", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config, system := cfg, "system"
			messages := []llm.Message{user, msg, result}
			defs := append([]llm.ToolDefinition(nil), tools...)
			switch tc.name {
			case "system":
				system = "new"
			case "tools":
				defs[0].Description = "changed"
			case "history":
				messages[0].Content = "changed"
			case "model":
				config.Name = "other"
			case "endpoint":
				config.BaseURL = "https://proxy.test"
			case "normalized endpoint":
				config.BaseURL = "https://api.anthropic.com/v1/"
			case "thinking off":
				config.Think.Enabled = false
			case "budget":
				config.Think.Mode = llm.High
			}
			req := BuildRequest(config, system, messages, defs)
			body, err := json.Marshal(req)
			require.NoError(t, err)
			if tc.keep {
				assert.Contains(t, string(body), `"signature"`)
				assert.Contains(t, string(body), `"redacted_thinking"`)
			} else {
				assert.NotContains(t, string(body), `"signature"`)
				assert.NotContains(t, string(body), `"redacted_thinking"`)
				blocks := req.Messages[1].Content.([]anthropicContentBlock)
				assert.Equal(t, "beforeafter", blocks[0].Text)
				assert.Equal(t, "read", blocks[1].Name)
				assert.JSONEq(t, msg.ToolCalls[0].Function.Arguments, string(blocks[1].Input))
				results := req.Messages[2].Content.([]anthropicContentBlock)
				assert.Equal(t, blocks[1].ID, results[0].ToolUseID)
			}
		})
	}
}

func TestNativeTextPrefixCompatibility(t *testing.T) {
	for _, text := range []string{"", "plain", "quotes: \"\\\n\t", "<>&", "中文 🌍\u2028\u2029", string([]byte{0xff, 0xfe})} {
		for _, role := range []string{"user", "assistant"} {
			fast, legacy := sha256.New(), sha256.New()
			require.True(t, writePrefix(fast, anthropicMessage{Role: role, Content: text}))
			// Older fingerprints normalized strings to typed text blocks before encoding.
			require.True(t, writePrefix(legacy, anthropicMessage{
				Role: role, Content: []anthropicContentBlock{{Type: "text", Text: text}},
			}))
			assert.Equal(t, legacy.Sum(nil), fast.Sum(nil), "role=%s text=%q", role, text)
		}
	}
}

func TestNativePrefixDoesNotLeaveGap(t *testing.T) {
	cfg := llm.ModelConfig{Name: "claude", Think: llm.ThinkConfig{Enabled: true}}
	messages := []llm.Message{{Role: llm.RoleUser, Content: "first"}}
	for range 3 {
		msg := nativeResponse(t, cfg, BuildRequest(cfg, "", messages, nil))
		messages = append(
			messages,
			msg,
			llm.Message{Role: llm.RoleTool, ToolCallID: msg.ToolCalls[0].ID, Content: "ok"},
		)
	}
	messages[3].ProviderState = nil
	req := BuildRequest(cfg, "", messages, nil)
	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(body), `"signature"`), "the later thinking must be stripped with the gap")

	// A new response belongs to the degraded prefix and can be replayed normally.
	msg := nativeResponse(t, cfg, req)
	messages = append(messages, msg, llm.Message{Role: llm.RoleTool, ToolCallID: msg.ToolCalls[0].ID, Content: "new"})
	req = BuildRequest(cfg, "", messages, nil)
	body, err = json.Marshal(req)
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(string(body), `"signature"`))
}

func TestIncompleteNativeFallsBack(t *testing.T) {
	for _, tc := range []struct{ name, sse string }{
		{"missing message stop", strings.ReplaceAll(nativeSSE, `data: {"type":"message_stop"}`, "")},
		{"missing signature", strings.ReplaceAll(nativeSSE, "signature", "unknown")},
		{"missing block stop", strings.ReplaceAll(nativeSSE, `data: {"type":"content_block_stop","index":4}`, "")},
		{"wrong order", strings.ReplaceAll(nativeSSE, `"index":3`, `"index":8`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := processForTest(tc.sse)
			require.NotEmpty(t, events)
			done := events[len(events)-1]
			require.Equal(t, llm.StreamEventTypeDone, done.Type)
			require.NotNil(t, done.Final)
			assert.Nil(t, done.Final.ProviderState)
			assert.Equal(t, "beforeafter", done.Final.Content)
			assert.Equal(t, " start\nnext", done.Final.ReasoningContent)
			require.Len(t, done.Final.ToolCalls, 1)
			assert.Equal(t, "read", done.Final.ToolCalls[0].Function.Name)
		})
	}
}

func TestNativeOnlyAndLegacyHistory(t *testing.T) {
	cfg := llm.ModelConfig{Name: "claude", Think: llm.ThinkConfig{Enabled: true}}
	user := llm.Message{Role: llm.RoleUser, Content: "think"}
	state := nativeResponse(t, cfg, BuildRequest(cfg, "", []llm.Message{user}, nil)).ProviderState
	state.Data.Items = []json.RawMessage{json.RawMessage(`{"type":"thinking","thinking":"","signature":"signed"}`)}
	msg := llm.Message{Role: llm.RoleAssistant, ProviderState: state}
	req := BuildRequest(cfg, "", []llm.Message{user, msg}, nil)
	require.Len(t, req.Messages, 2)
	assert.Equal(t, state.Data.Items, req.Messages[1].Content)
	msg.ProviderState = nil
	msg.ReasoningContent = "legacy display text"
	req = BuildRequest(cfg, "", []llm.Message{user, msg}, nil)
	assert.Len(t, req.Messages, 1, "legacy thinking has no signed wire content")
}

func TestNativeStableRequestRewrite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, nativeSSE)
	}))
	defer server.Close()
	cfg := llm.ModelConfig{Name: "alias", BaseURL: server.URL, Think: llm.ThinkConfig{Enabled: true}}
	tools := []llm.ToolDefinition{{Name: "read", Params: &llm.FunctionParameters{Type: "object"}}}
	for _, change := range []string{"unchanged", "model", "system", "tools", "schema", "history", "image", "tool input", "consumer mutation"} {
		t.Run(change, func(t *testing.T) {
			messages := []llm.Message{
				{Role: llm.RoleUser, Content: "read", Images: []llm.Image{{MimeType: "image/png", Data: "Zmlyc3Q="}}},
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "legacy", Function: llm.Function{
					Name: "read", Arguments: `{"path":"a.go"}`,
				}}}},
				{Role: llm.RoleTool, ToolCallID: "legacy", Content: "file"},
				{Role: llm.RoleUser, Content: "next"},
			}
			var previous *llm.Message
			for range 3 {
				req := BuildRequest(cfg, "system", messages, tools)
				cached := req.native
				if previous != nil {
					require.NotNil(t, cached)
					assert.Equal(t, previous.ProviderState.Data.Items, req.Messages[len(req.Messages)-2].Content)
				}
				// Apply the same hook after each build, including in-place slice mutations.
				switch change {
				case "model":
					req.Model = "actual-model"
				case "system":
					req.System[0].Text = "hook-system"
				case "tools":
					req.Tools[0].Description = "hook-description"
				case "schema":
					index := bytes.Index(req.Tools[0].InputSchema, []byte("object"))
					require.NotEqual(t, -1, index)
					copy(req.Tools[0].InputSchema[index:], "string")
				case "history":
					req.Messages[0].Content = "hook-user"
				case "image":
					req.Messages[0].Content.([]anthropicContentBlock)[1].Source.Data = "aG9va2Vk"
				case "tool input":
					copy(req.Messages[1].Content.([]anthropicContentBlock)[0].Input, `{"path":"b.go"}`)
				}
				previous = nil
				for ev, err := range Stream(t.Context(), server.Client(), cfg, &req) {
					require.NoError(t, err)
					if change == "consumer mutation" {
						// Consumers may retain and mutate the request after it has been sent.
						req.Model = "later-model"
						req.System[0].Text = "later-system"
						req.Messages[0].Content = "later-user"
						req.Tools[0].InputSchema[0] = '!'
					}
					if ev.Final != nil {
						previous = ev.Final
					}
				}
				require.NotNil(t, previous)
				require.NotNil(t, previous.ProviderState)
				assert.Equal(t, cfg.Name, previous.ProviderState.Model)
				if cached != nil {
					assert.Equal(t, cached.Data.Prefix, previous.ProviderState.Data.Prefix)
					assert.Empty(t, cached.Data.Items, "streaming must not mutate the cached prefix")
				}
				messages = append(messages, *previous, llm.Message{
					Role: llm.RoleTool, ToolCallID: previous.ToolCalls[0].ID, Content: "file",
				})
			}
		})
	}
}

func TestInvalidNativeCaptureReleasesBuffers(t *testing.T) {
	for _, tc := range []struct {
		name       string
		invalidate func(*nativeCapture)
	}{
		{"unsigned", func(c *nativeCapture) { c.stop(1, "") }},
		{"wrong start", func(c *nativeCapture) { c.start(3, nil) }},
		{"wrong delta", func(c *nativeCapture) { c.delta(2, "thinking_delta", "lost") }},
		{"wrong stop", func(c *nativeCapture) { c.stop(2, "") }},
		{"wrong delta type", func(c *nativeCapture) { c.delta(1, "text_delta", "lost") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var capture nativeCapture
			capture.start(0, json.RawMessage(`{"type":"text","text":"prefix"}`))
			capture.stop(0, "")
			capture.start(1, json.RawMessage(`{"type":"thinking","thinking":"unsigned"}`))
			if tc.name != "unsigned" {
				capture.delta(1, "signature_delta", "signed")
			}
			tc.invalidate(&capture)
			require.True(t, capture.invalid)
			assert.Nil(t, capture.items)
			assert.Nil(t, capture.block)
			assert.Zero(t, capture.text.Cap())
			assert.Zero(t, capture.signature.Cap())
			capture.start(2, json.RawMessage(`{"type":"text","text":"tail"}`))
			capture.delta(2, "text_delta", "more")
			capture.stop(2, "")
			assert.True(t, capture.invalid)
			assert.Nil(t, capture.items)
			assert.Nil(t, capture.block)
			assert.Zero(t, capture.text.Cap())
			assert.Zero(t, capture.signature.Cap())
		})
	}
}
