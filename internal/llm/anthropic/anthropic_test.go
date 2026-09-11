package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

func TestBuildRequestSystemIsArray(t *testing.T) {
	cfg := llm.ModelConfig{Name: "claude-sonnet-4-20250514", APIKey: "k", BaseURL: "https://api.anthropic.com"}
	req := BuildRequest(cfg, "You are helpful.", []llm.Message{
		{Role: llm.RoleUser, Content: "Hi"},
	}, nil)

	body, err := json.Marshal(req)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &raw))

	systemRaw, ok := raw["system"]
	require.True(t, ok, "expected system field in request body")
	var asBlocks []sysBlock
	err = json.Unmarshal(systemRaw, &asBlocks)
	require.NoError(t, err, "system must be an array, got: %s", string(systemRaw))
	require.Len(t, asBlocks, 1)
	require.Equal(t, "text", asBlocks[0].Type)
	require.Contains(t, asBlocks[0].Text, "helpful")
	require.NotNil(t, asBlocks[0].CacheControl, "expected cache_control on system block")
}

func TestBuildRequestMergesToolResults(t *testing.T) {
	cfg := llm.ModelConfig{Name: "claude-sonnet-4-20250514", APIKey: "k", BaseURL: "https://api.anthropic.com"}
	req := BuildRequest(cfg, "", []llm.Message{
		{Role: llm.RoleUser, Content: "run tools"},
		{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{ID: "call_01", Function: llm.Function{Name: "read", Arguments: `{"path":"a.go"}`}},
				{ID: "call_02", Function: llm.Function{Name: "read", Arguments: `{"path":"b.go"}`}},
				{ID: "call_03", Function: llm.Function{Name: "read", Arguments: `{"path":"c.go"}`}},
			},
		},
		{Role: llm.RoleTool, ToolCallID: "call_01", Content: "a"},
		{Role: llm.RoleTool, ToolCallID: "call_02", Content: "b"},
		{Role: llm.RoleTool, ToolCallID: "call_03", Content: "c"},
	}, nil)

	require.Len(t, req.Messages, 3)

	results, ok := req.Messages[2].Content.([]anthropicContentBlock)
	require.True(t, ok, "expected tool results as content blocks, got %T", req.Messages[2].Content)
	require.Len(t, results, 3)
	for i, wantID := range []string{"call_01", "call_02", "call_03"} {
		require.Equal(t, "tool_result", results[i].Type, "block %d", i)
		require.Equal(t, wantID, results[i].ToolUseID, "block %d", i)
	}

	body, err := json.Marshal(req)
	require.NoError(t, err)
	sbody := string(body)
	require.Equal(t, 3, strings.Count(sbody, `"tool_result"`))
	require.Equal(t, 2, strings.Count(sbody, `"role":"user"`))
}

func TestBuildRequestUserImages(t *testing.T) {
	cfg := llm.ModelConfig{Name: "claude-sonnet-4-20250514", APIKey: "k", BaseURL: "https://api.anthropic.com"}
	req := BuildRequest(cfg, "", []llm.Message{
		{
			Role:    llm.RoleUser,
			Content: "what is this?",
			Images: []llm.Image{
				{Data: "QUJD", MimeType: "image/png"},
				{Data: "REVG", MimeType: "image/jpeg"},
			},
		},
	}, nil)

	require.Len(t, req.Messages, 1)
	blocks, ok := req.Messages[0].Content.([]anthropicContentBlock)
	require.True(t, ok, "expected content blocks, got %T", req.Messages[0].Content)
	require.Len(t, blocks, 3)

	assert.Equal(t, "text", blocks[0].Type)
	assert.Equal(t, "what is this?", blocks[0].Text)
	for i, want := range []struct {
		mediaType string
		data      string
	}{
		{"image/png", "QUJD"},
		{"image/jpeg", "REVG"},
	} {
		b := blocks[i+1]
		assert.Equal(t, "image", b.Type)
		require.NotNil(t, b.Source)
		assert.Equal(t, "base64", b.Source.Type)
		assert.Equal(t, want.mediaType, b.Source.MediaType)
		assert.Equal(t, want.data, b.Source.Data)
	}

	// cache_control must land on the text block, never on an image block.
	assert.NotNil(t, blocks[0].CacheControl, "cache_control must land on the text block")
	for _, b := range blocks[1:] {
		assert.Nil(t, b.CacheControl, "image block must not carry cache_control")
	}

	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"media_type"`)
	assert.Contains(t, string(body), `"image/png"`)
	assert.NotContains(t, string(body), `"images"`)
}

func TestBuildRequestToolsSchema(t *testing.T) {
	cfg := llm.ModelConfig{Name: "claude-sonnet-4-20250514", APIKey: "k", BaseURL: "https://api.anthropic.com"}
	tools := []llm.ToolDefinition{{
		Name:        "read",
		Description: "read a file",
		Params: &llm.FunctionParameters{
			Type:       "object",
			Properties: llm.Object{"path": llm.Object{"type": "string"}},
			Required:   []string{"path"},
		},
	}}

	req := BuildRequest(cfg, "", []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, tools)
	require.Len(t, req.Tools, 1)
	require.Equal(t, "read", req.Tools[0].Name)
	require.Contains(t, string(req.Tools[0].InputSchema), `"required"`)
	require.NotNil(t, req.Tools[0].CacheControl, "expected cache_control on last tool")
}

func TestProcessStreamTextAndUsage(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":12,"cache_read_input_tokens":900,"cache_creation_input_tokens":50}}}`,
		"",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		"",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`,
		"",
		`data: {"type":"message_delta","usage":{"output_tokens":7}}`,
		"",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	events := processForTest(sse)

	var text strings.Builder
	var done *llm.StreamEvent
	for _, ev := range events {
		require.NotEqual(t, llm.StreamEventTypeError, ev.Type, "stream error: %s", ev.Err)
		switch ev.Type {
		case llm.StreamEventTypeDelta:
			text.WriteString(ev.Delta.Content)
		case llm.StreamEventTypeDone:
			done = &ev
		}
	}

	require.Equal(t, "Hello world", text.String())
	require.NotNil(t, done, "expected done event")
	msg := done.Partial.Choices[0].Message
	require.Equal(t, "Hello world", msg.Content)
	require.Equal(t, 962, done.Partial.Usage.PromptTokens, "PromptTokens")
	require.Equal(t, 7, done.Partial.Usage.CompletionTokens, "CompletionTokens")
	require.Equal(t, 969, done.Partial.Usage.TotalTokens, "TotalTokens")
	require.Equal(t, 900, done.Partial.Usage.CachedTokens())
}

func TestProcessStreamToolUseAndThinking(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
		"",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"let me think"}}`,
		"",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_01","name":"read"}}`,
		"",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
		"",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"a.go\"}"}}`,
		"",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	events := processForTest(sse)

	var thinking, args string
	var done *llm.StreamEvent
	for _, ev := range events {
		require.NotEqual(t, llm.StreamEventTypeError, ev.Type, "stream error: %s", ev.Err)
		switch ev.Type {
		case llm.StreamEventTypeDelta:
			thinking += ev.Delta.ReasoningContent
			for _, tc := range ev.Delta.ToolCalls {
				args = tc.Function.Arguments
			}
		case llm.StreamEventTypeDone:
			done = &ev
		}
	}

	require.Equal(t, "let me think", thinking)
	require.NotNil(t, done, "expected done event")
	msg := done.Partial.Choices[0].Message
	require.Equal(t, "let me think", msg.ReasoningContent)
	require.Len(t, msg.ToolCalls, 1)
	tc := msg.ToolCalls[0]
	require.Equal(t, "toolu_01", tc.ID)
	require.Equal(t, "read", tc.Function.Name)
	// Fragments are concatenated, not remarshaled. JSONEq would hide whitespace/key-order drift.
	require.Equal(t, `{"path":"a.go"}`, tc.Function.Arguments) //nolint:testifylint // json-eq
	require.Equal(t, `{"path":"a.go"}`, args)                  //nolint:testifylint // json-eq
}

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "https://api.anthropic.com/v1"},
		{"https://api.anthropic.com", "https://api.anthropic.com/v1"},
		{"https://api.anthropic.com/", "https://api.anthropic.com/v1"},
		{"https://api.anthropic.com/v1", "https://api.anthropic.com/v1"},
		{"http://localhost:8080", "http://localhost:8080/v1"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, normalizeBaseURL(c.in), "normalizeBaseURL(%q)", c.in)
	}
}

// processForTest runs processStream and returns the yielded events.
func processForTest(sse string) []llm.StreamEvent {
	var events []llm.StreamEvent
	processStream(strings.NewReader(sse), func(ev llm.StreamEvent, err error) bool {
		if err != nil {
			events = append(events, llm.StreamEvent{Type: llm.StreamEventTypeError, Err: err.Error()})
			return false
		}
		events = append(events, ev)
		return true
	})
	return events
}
