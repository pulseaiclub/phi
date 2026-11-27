package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/core"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/llm/anthropic"
	"github.com/pulseaiclub/phi/internal/llm/openai"
)

func TestClientStreamAnthropicEndToEnd(t *testing.T) {
	var gotPath, gotKey, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("X-Api-Key")
		gotVersion = r.Header.Get("Anthropic-Version")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"message_start","message":{"usage":{"input_tokens":3}}}`,
			"",
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
			"",
			`data: {"type":"message_delta","usage":{"output_tokens":2}}`,
			"",
			`data: {"type":"message_stop"}`,
			"",
		}, "\n")))
	}))
	defer srv.Close()

	client := NewClient(
		core.ModelConfig{Name: "claude-sonnet-4-20250514", BaseURL: srv.URL, APIKey: "sk-test", API: core.Anthropic},
		Hooks{},
		nil,
		"be brief",
	)
	events := collectEvents(client.Stream(t.Context(), []core.Message{{Role: core.RoleUser, Content: "hello"}}))

	require.Equal(t, "/v1/messages", gotPath)
	require.Equal(t, "sk-test", gotKey)
	require.NotEmpty(t, gotVersion, "expected Anthropic-Version header")
	var text strings.Builder
	var done *core.StreamEvent
	for _, ev := range events {
		require.Empty(t, ev.Err, "stream error")
		switch ev.Type {
		case core.StreamEventTypeDelta:
			text.WriteString(ev.Delta.Content)
		case core.StreamEventTypeDone:
			done = &ev
		}
	}
	require.Equal(t, "hi", text.String())
	require.NotNil(t, done, "unexpected stream result")
	require.Equal(t, "hi", done.Final.Content)
	require.Equal(t, 5, done.Final.Usage.TotalTokens)
}

func TestClientStreamOpenAIEndToEnd(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"choices":[{"delta":{"role":"assistant","content":"he"}}]}`,
			"",
			`data: {"choices":[{"delta":{"content":"llo"}}]}`,
			"",
			`data: {"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer srv.Close()

	client := NewClient(core.ModelConfig{Name: "gpt-4o", BaseURL: srv.URL, APIKey: "sk-test"}, Hooks{}, nil, "")
	events := collectEvents(client.Stream(t.Context(), []core.Message{{Role: core.RoleUser, Content: "hello"}}))

	require.Equal(t, "/chat/completions", gotPath)
	var text strings.Builder
	var done *core.StreamEvent
	for _, ev := range events {
		require.Empty(t, ev.Err, "stream error")
		switch ev.Type {
		case core.StreamEventTypeDelta:
			text.WriteString(ev.Delta.Content)
		case core.StreamEventTypeDone:
			done = &ev
		}
	}
	require.Equal(t, "hello", text.String())
	require.NotNil(t, done, "unexpected stream result")
	require.Equal(t, "hello", done.Final.Content)
	require.Equal(t, 6, done.Final.Usage.TotalTokens)
}

func TestClientStreamOpenAIResponsesEndToEnd(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.output_text.delta","delta":"hi"}`,
			"",
			`data: {"type":"response.completed","response":{"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}}`,
			"",
		}, "\n")))
	}))
	defer srv.Close()

	client := NewClient(
		core.ModelConfig{Name: "gpt-5", BaseURL: srv.URL, APIKey: "sk-test", API: core.OpenAIResponses},
		Hooks{},
		nil,
		"",
	)
	events := collectEvents(client.Stream(t.Context(), []core.Message{{Role: core.RoleUser, Content: "hello"}}))

	require.Equal(t, "/responses", gotPath)
	var text strings.Builder
	var done *core.StreamEvent
	for _, ev := range events {
		require.Empty(t, ev.Err, "stream error")
		switch ev.Type {
		case core.StreamEventTypeDelta:
			text.WriteString(ev.Delta.Content)
		case core.StreamEventTypeDone:
			done = &ev
		}
	}
	require.Equal(t, "hi", text.String())
	require.NotNil(t, done)
	require.Equal(t, "hi", done.Final.Content)
	require.Equal(t, 4, done.Final.Usage.TotalTokens)
}

func TestClientCompactAnthropic(t *testing.T) {
	var gotPath string
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"summary here"}],"stop_reason":"end_turn"}`))
	}))
	defer srv.Close()

	client := NewClient(
		core.ModelConfig{Name: "claude-sonnet-4-20250514", BaseURL: srv.URL, APIKey: "sk-test", API: core.Anthropic},
		Hooks{},
		nil,
		"",
	)
	res, err := client.Compact(t.Context(), llm.CompactRequest{Prompt: "summarize", MaxTokens: 13107})
	require.NoError(t, err)
	require.Equal(t, "/v1/messages", gotPath)
	require.Equal(t, "summary here", res.Text)
	require.False(t, res.Truncated)
	require.Equal(t, 13107, jsonField(t, body, "max_tokens"), "the summary cap must reach the provider")
}

func TestClientCompactOpenAI(t *testing.T) {
	var gotPath string
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(
			[]byte(`{"choices":[{"message":{"role":"assistant","content":"summary here"},"finish_reason":"stop"}]}`),
		)
	}))
	defer srv.Close()

	client := NewClient(core.ModelConfig{Name: "gpt-4o", BaseURL: srv.URL, APIKey: "sk-test"}, Hooks{}, nil, "")
	res, err := client.Compact(t.Context(), llm.CompactRequest{Prompt: "summarize", MaxTokens: 13107})
	require.NoError(t, err)
	require.Equal(t, "/chat/completions", gotPath)
	require.Equal(t, "summary here", res.Text)
	require.False(t, res.Truncated)
	require.Equal(t, 13107, jsonField(t, body, "max_tokens"))
}

// A provider that stopped at the cap returns partial text, and the caller must
// be able to tell it apart from a finished summary.
func TestClientCompactReportsCappedOutput(t *testing.T) {
	cases := []struct {
		name string
		api  core.RouterType
		body string
	}{
		{
			name: "anthropic",
			api:  core.Anthropic,
			body: `{"content":[{"type":"text","text":"partial"}],"stop_reason":"max_tokens"}`,
		},
		{
			name: "openai",
			api:  core.OpenAI,
			body: `{"choices":[{"message":{"role":"assistant","content":"partial"},"finish_reason":"length"}]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			client := NewClient(
				core.ModelConfig{Name: "m", BaseURL: srv.URL, APIKey: "sk-test", API: tc.api},
				Hooks{},
				nil,
				"",
			)
			res, err := client.Compact(t.Context(), llm.CompactRequest{Prompt: "summarize", MaxTokens: 100})
			require.NoError(t, err)
			require.True(t, res.Truncated, "capped output must be reported as truncated")
			require.Equal(t, "partial", res.Text)
		})
	}
}

// jsonField reads one numeric field out of a captured request body.
func jsonField(t *testing.T, body []byte, field string) int {
	t.Helper()
	var raw map[string]any
	require.NoError(t, json.Unmarshal(body, &raw))
	n, ok := raw[field].(float64)
	require.True(t, ok, "field %q missing from the request body", field)
	return int(n)
}

type openAIExtraThinking struct{}

func (openAIExtraThinking) Before(_ context.Context, req *openai.Request, _ core.ModelConfig) error {
	req.Thinking = &openai.ThinkingConfig{Type: "enabled"}
	return nil
}

func TestClientStreamOpenAIRunsInterceptor(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	client := NewClient(
		core.ModelConfig{Name: "deepseek-flash", BaseURL: srv.URL, APIKey: "sk-test"},
		Hooks{OpenAI: openAIExtraThinking{}},
		nil,
		"",
	)
	for _, err := range client.Stream(t.Context(), []core.Message{{Role: core.RoleUser, Content: "hi"}}) {
		require.NoError(t, err)
	}
	require.NotContains(t, string(body), `"extra_body"`)
	require.Contains(t, string(body), `"thinking"`)
}

type rejectHook struct{}

func (rejectHook) Before(context.Context, *openai.Request, core.ModelConfig) error {
	return errors.New("blocked by hook")
}

func TestClientStreamOpenAIInterceptorError(t *testing.T) {
	client := NewClient(
		core.ModelConfig{Name: "gpt-4o", BaseURL: "http://127.0.0.1:9", APIKey: "sk-test"},
		Hooks{OpenAI: rejectHook{}},
		nil,
		"",
	)
	events := collectEvents(client.Stream(t.Context(), []core.Message{{Role: core.RoleUser, Content: "hi"}}))
	require.Len(t, events, 1)
	require.Equal(t, core.StreamEventTypeError, events[0].Type)
	require.Contains(t, events[0].Err, "blocked by hook")
}

// collectEvents drains an iter.Seq2 into a slice.
func collectEvents(seq func(func(core.StreamEvent, error) bool)) []core.StreamEvent {
	var events []core.StreamEvent
	for ev, err := range seq {
		if err != nil {
			events = append(events, core.StreamEvent{Type: core.StreamEventTypeError, Err: err.Error()})
			continue
		}
		events = append(events, ev)
	}
	return events
}

type anthropicMaxTokensCutter struct{}

func (anthropicMaxTokensCutter) Before(_ context.Context, req *anthropic.AnthropicRequest, _ core.ModelConfig) error {
	req.MaxTokens = 4096
	return nil
}

type anthropicBudgetRemover struct{}

func (anthropicBudgetRemover) Before(_ context.Context, req *anthropic.AnthropicRequest, _ core.ModelConfig) error {
	req.Thinking.BudgetTokens = nil
	return nil
}

func TestClientStreamAnthropicRejectsMissingBudgetAfterHook(t *testing.T) {
	client := NewClient(core.ModelConfig{
		Name: "claude-sonnet-4-20250514", BaseURL: "http://127.0.0.1:9", API: core.Anthropic,
		Think: core.ThinkConfig{Enabled: true, Mode: core.Low},
	}, Hooks{Anthropic: anthropicBudgetRemover{}}, nil, "")
	events := collectEvents(client.Stream(t.Context(), []core.Message{{Role: core.RoleUser, Content: "hi"}}))
	require.Len(t, events, 1)
	require.Equal(t, core.StreamEventTypeError, events[0].Type)
	require.Contains(t, events[0].Err, "thinking type enabled requires budget_tokens")
}

// A hook that shrinks max_tokens below the thinking budget must fail before
// any HTTP call, with the specific invariant message instead of the API's
// generic 400.
func TestClientStreamAnthropicValidatesThinkingAfterHook(t *testing.T) {
	client := NewClient(core.ModelConfig{
		Name: "claude-sonnet-4-20250514", BaseURL: "http://127.0.0.1:9", API: core.Anthropic,
		Think: core.ThinkConfig{Enabled: true, Mode: core.Medium},
	}, Hooks{Anthropic: anthropicMaxTokensCutter{}}, nil, "")
	events := collectEvents(client.Stream(t.Context(), []core.Message{{Role: core.RoleUser, Content: "hi"}}))
	require.Len(t, events, 1)
	require.Equal(t, core.StreamEventTypeError, events[0].Type)
	require.Contains(t, events[0].Err, "thinking budget_tokens 8192 must be less than max_tokens 4096")
}
