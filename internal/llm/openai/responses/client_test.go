package responses

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/core"
)

func TestBuildRequestBasic(t *testing.T) {
	cfg := core.ModelConfig{Name: "gpt-5", Think: core.ThinkConfig{Enabled: true, Mode: core.Medium}}
	req := BuildRequest(cfg, "sys", []core.Message{
		{Role: core.RoleUser, Content: "hi"},
		{
			Role:    core.RoleAssistant,
			Content: "ok",
			ToolCalls: []core.ToolCall{{
				ID:       "call_1|fc_1",
				Type:     "function",
				Function: core.Function{Name: "bash", Arguments: `{"cmd":"ls"}`},
			}},
		},
		{Role: core.RoleTool, ToolCallID: "call_1|fc_1", Content: "a\nb"},
	}, []core.ToolDefinition{{Name: "bash", Description: "run", Params: &core.FunctionParameters{Type: "object"}}})

	require.NotNil(t, req.Reasoning)
	assert.Equal(t, "medium", req.Reasoning.Effort)
	assert.False(t, req.Store)
	assert.True(t, req.Stream)

	require.GreaterOrEqual(t, len(req.Input), 4)
	assert.Equal(t, "developer", req.Input[0].Role)
	assert.Equal(t, "sys", req.Input[0].Content)

	userParts, ok := req.Input[1].Content.([]contentPart)
	require.True(t, ok)
	require.Len(t, userParts, 1)
	assert.Equal(t, "input_text", userParts[0].Type)

	assert.Equal(t, "message", req.Input[2].Type)
	assert.Equal(t, "function_call", req.Input[3].Type)
	assert.Equal(t, "call_1", req.Input[3].CallID)
	assert.Equal(t, "function_call_output", req.Input[4].Type)
	assert.Equal(t, "call_1", req.Input[4].CallID)

	require.Len(t, req.Tools, 1)
	assert.Equal(t, "bash", req.Tools[0].Name)
	assert.Equal(t, "function", req.Tools[0].Type)
}

func TestBuildRequestNilToolParameters(t *testing.T) {
	req := BuildRequest(core.ModelConfig{Name: "gpt-5"}, "", nil, []core.ToolDefinition{{Name: "read"}})
	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.NotContains(t, string(body), `"parameters"`)
}

func TestBuildRequestImages(t *testing.T) {
	req := BuildRequest(core.ModelConfig{Name: "gpt-5"}, "", []core.Message{{
		Role:    core.RoleUser,
		Content: "what?",
		Images:  []core.Image{{Data: "QUJD", MimeType: "image/png"}},
	}}, nil)

	parts, ok := req.Input[0].Content.([]contentPart)
	require.True(t, ok)
	require.Len(t, parts, 2)
	assert.Equal(t, "input_image", parts[1].Type)
	assert.Equal(t, "data:image/png;base64,QUJD", parts[1].ImageURL)

	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"input_image"`)
	assert.NotContains(t, string(body), `"images"`)
}

func TestStreamTextAndTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/responses", r.URL.Path)
		assert.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.output_text.delta","delta":"he"}`,
			"",
			`data: {"type":"response.output_text.delta","delta":"llo"}`,
			"",
			`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"bash","arguments":""}}`,
			"",
			`data: {"type":"response.function_call_arguments.delta","delta":"{\"cmd\""}`,
			"",
			`data: {"type":"response.function_call_arguments.done","arguments":"{\"cmd\":\"ls\"}"}`,
			"",
			`data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cached_tokens":2}}}}`,
			"",
		}, "\n")))
	}))
	defer srv.Close()

	cfg := core.ModelConfig{Name: "gpt-5", BaseURL: srv.URL, APIKey: "sk-test"}
	req := BuildRequest(cfg, "", []core.Message{{Role: core.RoleUser, Content: "hi"}}, nil)

	var text strings.Builder
	var done *core.StreamEvent
	for ev, err := range Stream(t.Context(), srv.Client(), cfg, req) {
		require.NoError(t, err)
		switch ev.Type {
		case core.StreamEventTypeDelta:
			text.WriteString(ev.Delta.Content)
		case core.StreamEventTypeDone:
			done = &ev
		}
	}

	require.NotNil(t, done)
	assert.Equal(t, "hello", text.String())
	msg := done.Final
	require.NotNil(t, msg)
	assert.Equal(t, "hello", msg.Content)
	require.Len(t, msg.ToolCalls, 1)
	assert.Equal(t, "call_1|fc_1", msg.ToolCalls[0].ID)
	assert.Equal(t, "bash", msg.ToolCalls[0].Function.Name)
	assert.JSONEq(t, `{"cmd":"ls"}`, msg.ToolCalls[0].Function.Arguments)
	assert.Equal(t, 8, msg.Usage.PromptTokens, "uncached input (10 - 2 cached)")
	assert.Equal(t, 2, msg.Usage.CachedTokens())
	assert.Equal(t, 15, msg.Usage.TotalTokens)
	assert.Equal(t, 15, msg.Usage.ContextTokens())
}

func TestCompactRequest(t *testing.T) {
	var gotMaxOutputTokens int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/responses", r.URL.Path)
		var sent struct {
			MaxOutputTokens int `json:"max_output_tokens"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&sent))
		gotMaxOutputTokens = sent.MaxOutputTokens
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(
			[]byte(
				`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"summary"}]}]}`,
			),
		)
	}))
	defer srv.Close()

	cfg := core.ModelConfig{Name: "gpt-5", BaseURL: srv.URL, APIKey: "sk"}
	res, err := CompactRequest(t.Context(), srv.Client(), cfg, NewCompactRequest(cfg, "sum", 13107))
	require.NoError(t, err)
	assert.Equal(t, "summary", res.Text)
	assert.False(t, res.Truncated)
	assert.Equal(t, 13107, gotMaxOutputTokens)
}

// status incomplete with reason max_output_tokens means the answer is a prefix.
func TestCompactRequestReportsIncompleteOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},` +
			`"output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`))
	}))
	defer srv.Close()

	cfg := core.ModelConfig{Name: "gpt-5", BaseURL: srv.URL, APIKey: "sk"}
	res, err := CompactRequest(t.Context(), srv.Client(), cfg, NewCompactRequest(cfg, "sum", 10))
	require.NoError(t, err)
	assert.Equal(t, "partial", res.Text)
	assert.True(t, res.Truncated)
}

// Any other incomplete reason (content_filter, …) is not an output cap.
func TestCompactRequestIgnoresOtherIncompleteReasons(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"incomplete","incomplete_details":{"reason":"content_filter"},` +
			`"output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`))
	}))
	defer srv.Close()

	cfg := core.ModelConfig{Name: "gpt-5", BaseURL: srv.URL, APIKey: "sk"}
	res, err := CompactRequest(t.Context(), srv.Client(), cfg, NewCompactRequest(cfg, "sum", 10))
	require.NoError(t, err)
	assert.False(t, res.Truncated)
}
