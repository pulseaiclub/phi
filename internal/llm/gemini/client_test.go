package gemini

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

func TestGetStreamURL(t *testing.T) {
	testcases := []struct {
		name    string
		apiKey  string
		model   string
		baseURL string
		expect  string
	}{
		{
			name:    "with_api_key",
			model:   "gemini-2.5",
			apiKey:  "sk-xxxx123456789",
			baseURL: "https://generativelanguage.googleapis.com/v1beta",
			expect:  "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5:streamGenerateContent?alt=sse&key=sk-xxxx123456789",
		},
		{
			name:    "without_api_key",
			model:   "gemini-2.5",
			baseURL: "https://aiplatform.googleapis.com",
			expect:  "https://aiplatform.googleapis.com/models/gemini-2.5:streamGenerateContent?alt=sse",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			url := getURL(tc.model, tc.baseURL, tc.apiKey, true)
			assert.Equal(t, tc.expect, url)
		})
	}
}

func TestGetURLNonStreaming(t *testing.T) {
	url := getURL("gemini-2.5", "https://generativelanguage.googleapis.com/v1beta", "key123", false)
	assert.Equal(
		t,
		"https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5:generateContent?key=key123",
		url,
	)
}

func TestBuildRequestSystemInstructionWireName(t *testing.T) {
	req := BuildRequest("be brief", []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil)
	body, err := json.Marshal(req)
	require.NoError(t, err)
	// Gemini REST API expects camelCase; snake_case would be ignored or rejected.
	assert.Contains(t, string(body), `"systemInstruction"`)
	assert.NotContains(t, string(body), "system_instruction")
}

func TestBuildRequestMergesToolResults(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "read", Function: llm.Function{Name: "read"}}}},
		{Role: llm.RoleTool, ToolCallID: "read", Content: "a"},
		{Role: llm.RoleTool, ToolCallID: "write", Content: "b"},
	}
	req := BuildRequest("", messages, nil)
	require.Len(t, req.Contents, 3)
	last := req.Contents[2]
	assert.Equal(t, "user", last.Role)
	require.Len(t, last.Parts, 2)
	assert.Equal(t, "a", last.Parts[0].FunctionResponse.Response.(map[string]any)["output"])
	assert.Equal(t, "b", last.Parts[1].FunctionResponse.Response.(map[string]any)["output"])
}

func TestBuildRequestToolCallArgsKeptVerbatim(t *testing.T) {
	// 2^53+1 is the smallest integer a float64 round trip corrupts, and the key
	// order has to survive too: the API compares the object it is given.
	const args = `{"b":1,"a":9007199254740993}`
	req := BuildRequest("", []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			Index:    0,
			ID:       "call_1",
			Type:     "function",
			Function: llm.Function{Name: "write", Arguments: args},
		}}},
	}, nil)

	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"args":`+args)
}

func TestBuildRequestToolCallArgsDropped(t *testing.T) {
	testcases := map[string]string{
		"empty":         "",
		"blank":         "   ",
		"truncated":     `{"path":"a.go"`,
		"not an object": `["a.go"]`,
	}
	for name, arguments := range testcases {
		t.Run(name, func(t *testing.T) {
			req := BuildRequest("", []llm.Message{
				{Role: llm.RoleUser, Content: "hi"},
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
					ID:       "call_1",
					Function: llm.Function{Name: "write", Arguments: arguments},
				}}},
			}, nil)

			body, err := json.Marshal(req)
			require.NoError(t, err)
			assert.NotContains(t, string(body), `"args"`)
		})
	}
}

func TestBuildRequestUserContent(t *testing.T) {
	req := BuildRequest("", []llm.Message{{
		Role:    llm.RoleUser,
		Content: "what is this",
		Images:  []llm.Image{{MimeType: "image/png", Data: "aGVsbG8="}},
	}}, nil)
	require.Len(t, req.Contents, 1)
	parts := req.Contents[0].Parts
	require.Len(t, parts, 2)
	assert.Equal(t, "what is this", parts[0].Text)
	require.NotNil(t, parts[1].InlineData)
	assert.Equal(t, "image/png", parts[1].InlineData.MIMEType)
	assert.Equal(t, "aGVsbG8=", parts[1].InlineData.Data)
}

func TestApplyBudgetThinking(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		var req GeminiRequest
		req.ApplyBudgetThinking(llm.ThinkConfig{})
		require.NotNil(t, req.ThinkingConfig)
		body, err := json.Marshal(req.ThinkingConfig)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"thinkingBudget":0`)
	})
	t.Run("enabled", func(t *testing.T) {
		testcases := []struct {
			mode llm.ThinkMode
			want string
		}{
			{mode: llm.Low, want: `"thinkingBudget":2048`},
			{mode: llm.Medium, want: `"thinkingBudget":8192`},
			{mode: llm.High, want: `"thinkingBudget":16384`},
		}
		for _, tc := range testcases {
			t.Run(string(tc.mode), func(t *testing.T) {
				var req GeminiRequest
				req.ApplyBudgetThinking(llm.ThinkConfig{Enabled: true, Mode: tc.mode})
				body, err := json.Marshal(req.ThinkingConfig)
				require.NoError(t, err)
				assert.Contains(t, string(body), tc.want)
			})
		}
	})
}

func TestApplyLevelThinking(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		testcases := []struct {
			name string
			off  string
			want string
		}{
			{name: "default_minimal", want: `"thinkingLevel":"MINIMAL"`},
			{name: "custom_low", off: "LOW", want: `"thinkingLevel":"LOW"`},
		}
		for _, tc := range testcases {
			t.Run(tc.name, func(t *testing.T) {
				var req GeminiRequest
				req.ApplyLevelThinking(llm.ThinkConfig{}, tc.off)
				body, err := json.Marshal(req.ThinkingConfig)
				require.NoError(t, err)
				assert.Contains(t, string(body), tc.want)
			})
		}
	})
	t.Run("enabled", func(t *testing.T) {
		testcases := []struct {
			mode llm.ThinkMode
			want string
		}{
			{mode: llm.Medium, want: `"thinkingLevel":"MEDIUM"`},
			{mode: llm.High, want: `"thinkingLevel":"HIGH"`},
		}
		for _, tc := range testcases {
			t.Run(string(tc.mode), func(t *testing.T) {
				var req GeminiRequest
				req.ApplyLevelThinking(llm.ThinkConfig{Enabled: true, Mode: tc.mode}, "MINIMAL")
				body, err := json.Marshal(req.ThinkingConfig)
				require.NoError(t, err)
				assert.Contains(t, string(body), tc.want)
			})
		}
	})
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

func TestProcessStreamTextAndUsage(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"Hello"}]}}]}`,
		"",
		`data: {"candidates":[{"content":{"parts":[{"text":" world"}]}}]}`,
		"",
		`data: {"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":7,"cachedContentTokenCount":5,"thoughtsTokenCount":3,"totalTokenCount":22}}`,
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

	require.NotNil(t, done, "expected done event")
	msg := done.Final
	require.NotNil(t, msg)
	assert.Equal(t, "Hello world", text.String())
	assert.Equal(t, "Hello world", msg.Content)
	assert.Equal(t, 7, msg.Usage.PromptTokens, "uncached input (12 - 5 cached)")
	assert.Equal(t, 5, msg.Usage.CachedTokens())
	assert.Equal(t, 10, msg.Usage.CompletionTokens)
	assert.Equal(t, 22, msg.Usage.ContextTokens(), "cached content stays in the context total")
}

func TestProcessStreamThinking(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"thought":true,"text":"let me think"}]}}]}`,
		"",
		`data: {"candidates":[{"content":{"parts":[{"text":"answer"}]}}]}`,
		"",
	}, "\n")

	events := processForTest(sse)

	var text, reasoning strings.Builder
	var done *llm.StreamEvent
	for _, ev := range events {
		require.NotEqual(t, llm.StreamEventTypeError, ev.Type, "stream error: %s", ev.Err)
		switch ev.Type {
		case llm.StreamEventTypeDelta:
			text.WriteString(ev.Delta.Content)
			reasoning.WriteString(ev.Delta.ReasoningContent)
		case llm.StreamEventTypeDone:
			done = &ev
		}
	}

	require.NotNil(t, done, "expected done event")
	msg := done.Final
	assert.Equal(t, "answer", text.String())
	assert.Equal(t, "let me think", reasoning.String())
	assert.Equal(t, "answer", msg.Content)
	assert.Equal(t, "let me think", msg.ReasoningContent)
}

func TestCompactUsesNonStreamingEndpoint(t *testing.T) {
	var gotPath string
	var gotGenerationConfig map[string]int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var sent struct {
			GenerationConfig map[string]int `json:"generationConfig"`
		}
		_ = json.NewDecoder(r.Body).Decode(&sent)
		gotGenerationConfig = sent.GenerationConfig
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"text":"summary"}]},"finishReason":"STOP"}]}`)
	}))
	defer srv.Close()

	body := BuildRequest("", []llm.Message{{Role: llm.RoleUser, Content: "summarize"}}, nil)
	body.SetMaxOutputTokens(13107)
	res, err := CompactRequest(
		t.Context(),
		srv.Client(),
		llm.ModelConfig{Name: "gemini-2.5-flash", BaseURL: srv.URL, APIKey: "k"},
		&body,
	)
	require.NoError(t, err)
	assert.Equal(t, "summary", res.Text)
	assert.False(t, res.Truncated)
	assert.Equal(t, "/models/gemini-2.5-flash:generateContent", gotPath)
	assert.Equal(t, map[string]int{"maxOutputTokens": 13107}, gotGenerationConfig)
}

// MAX_TOKENS means the model stopped at the cap: the text is a prefix.
func TestCompactRequestReportsTruncatedCandidate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"text":"partial"}]},"finishReason":"MAX_TOKENS"}]}`)
	}))
	defer srv.Close()

	res, err := CompactRequest(
		t.Context(),
		srv.Client(),
		llm.ModelConfig{Name: "gemini-2.5-flash", BaseURL: srv.URL, APIKey: "k"},
		&GeminiRequest{Contents: []content{{Role: "user", Parts: []part{{Text: "x"}}}}},
	)
	require.NoError(t, err)
	assert.Equal(t, "partial", res.Text)
	assert.True(t, res.Truncated)
}

func TestCompactFormatsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(
			w,
			`{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT"}}`,
		)
	}))
	defer srv.Close()

	_, err := CompactRequest(
		t.Context(),
		srv.Client(),
		llm.ModelConfig{Name: "gemini-2.5-flash", BaseURL: srv.URL, APIKey: "bad"},
		&GeminiRequest{Contents: []content{{Role: "user", Parts: []part{{Text: "summarize"}}}}},
	)
	require.Error(t, err)
	assert.Equal(t, "gemini API error (400): API key not valid. Please pass a valid API key.", err.Error())
}

func TestStreamFormatsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(
			w,
			`{"error":{"code":401,"message":"API key not valid. Please pass a valid API key.","status":"UNAUTHENTICATED"}}`,
		)
	}))
	defer srv.Close()

	req := BuildRequest("", []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil)
	var gotErr error
	for _, err := range Stream(
		t.Context(),
		srv.Client(),
		llm.ModelConfig{Name: "gemini-2.5-flash", BaseURL: srv.URL, APIKey: "bad"},
		&req,
	) {
		if err != nil {
			gotErr = err
			break
		}
	}
	require.Error(t, gotErr)
	assert.Equal(t, "gemini API error (401): API key not valid. Please pass a valid API key.", gotErr.Error())
}
