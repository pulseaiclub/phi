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

	"github.com/pulseaiclub/phi/internal/core"
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
	req := BuildRequest("be brief", []core.Message{{Role: core.RoleUser, Content: "hi"}}, nil)
	body, err := json.Marshal(req)
	require.NoError(t, err)
	// Gemini REST API expects camelCase; snake_case would be ignored or rejected.
	assert.Contains(t, string(body), `"systemInstruction"`)
	assert.NotContains(t, string(body), "system_instruction")
}

func TestBuildRequestMergesToolResults(t *testing.T) {
	messages := []core.Message{
		{Role: core.RoleUser, Content: "hi"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "read", Function: core.Function{Name: "read"}}}},
		{Role: core.RoleTool, ToolCallID: "read", Content: "a"},
		{Role: core.RoleTool, ToolCallID: "write", Content: "b"},
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
	req := BuildRequest("", []core.Message{
		{Role: core.RoleUser, Content: "hi"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
			Index:    0,
			ID:       "call_1",
			Type:     "function",
			Function: core.Function{Name: "write", Arguments: args},
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
			req := BuildRequest("", []core.Message{
				{Role: core.RoleUser, Content: "hi"},
				{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
					ID:       "call_1",
					Function: core.Function{Name: "write", Arguments: arguments},
				}}},
			}, nil)

			body, err := json.Marshal(req)
			require.NoError(t, err)
			assert.NotContains(t, string(body), `"args"`)
		})
	}
}

func TestBuildRequestUserContent(t *testing.T) {
	req := BuildRequest("", []core.Message{{
		Role:    core.RoleUser,
		Content: "what is this",
		Images:  []core.Image{{MimeType: "image/png", Data: "aGVsbG8="}},
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
		req.ApplyBudgetThinking(core.ThinkConfig{})
		require.NotNil(t, req.GenerationConfig)
		require.NotNil(t, req.GenerationConfig.ThinkingConfig)
		body, err := json.Marshal(req.GenerationConfig.ThinkingConfig)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"thinkingBudget":0`)
	})
	t.Run("enabled", func(t *testing.T) {
		testcases := []struct {
			mode core.ThinkMode
			want string
		}{
			{mode: core.Low, want: `"thinkingBudget":2048`},
			{mode: core.Medium, want: `"thinkingBudget":8192`},
			{mode: core.High, want: `"thinkingBudget":16384`},
		}
		for _, tc := range testcases {
			t.Run(string(tc.mode), func(t *testing.T) {
				var req GeminiRequest
				req.ApplyBudgetThinking(core.ThinkConfig{Enabled: true, Mode: tc.mode})
				require.NotNil(t, req.GenerationConfig)
				body, err := json.Marshal(req.GenerationConfig.ThinkingConfig)
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
				req.ApplyLevelThinking(core.ThinkConfig{}, tc.off, false)
				require.NotNil(t, req.GenerationConfig)
				body, err := json.Marshal(req.GenerationConfig.ThinkingConfig)
				require.NoError(t, err)
				assert.Contains(t, string(body), tc.want)
			})
		}
	})
	t.Run("enabled", func(t *testing.T) {
		testcases := []struct {
			name       string
			mode       core.ThinkMode
			off        string
			allowXHigh bool
			want       string
		}{
			{name: "medium", mode: core.Medium, off: "MINIMAL", want: `"thinkingLevel":"MEDIUM"`},
			{name: "high", mode: core.High, off: "MINIMAL", want: `"thinkingLevel":"HIGH"`},
			{name: "minimal_clamped_to_low", mode: core.Minimal, off: "LOW", want: `"thinkingLevel":"LOW"`},
			{
				name:       "xhigh_capped_to_high",
				mode:       core.XHigh,
				off:        "LOW",
				allowXHigh: false,
				want:       `"thinkingLevel":"HIGH"`,
			},
			{name: "xhigh_allowed", mode: core.XHigh, off: "LOW", allowXHigh: true, want: `"thinkingLevel":"XHIGH"`},
		}
		for _, tc := range testcases {
			t.Run(tc.name, func(t *testing.T) {
				var req GeminiRequest
				req.ApplyLevelThinking(core.ThinkConfig{Enabled: true, Mode: tc.mode}, tc.off, tc.allowXHigh)
				require.NotNil(t, req.GenerationConfig)
				body, err := json.Marshal(req.GenerationConfig.ThinkingConfig)
				require.NoError(t, err)
				assert.Contains(t, string(body), tc.want)
			})
		}
	})
}

// processForTest runs processStream and returns the yielded events.
func processForTest(sse string) []core.StreamEvent {
	var events []core.StreamEvent
	processStream(strings.NewReader(sse), func(ev core.StreamEvent, err error) bool {
		if err != nil {
			events = append(events, core.StreamEvent{Type: core.StreamEventTypeError, Err: err.Error()})
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
	var done *core.StreamEvent
	for _, ev := range events {
		require.NotEqual(t, core.StreamEventTypeError, ev.Type, "stream error: %s", ev.Err)
		switch ev.Type {
		case core.StreamEventTypeDelta:
			text.WriteString(ev.Delta.Content)
		case core.StreamEventTypeDone:
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
	var done *core.StreamEvent
	for _, ev := range events {
		require.NotEqual(t, core.StreamEventTypeError, ev.Type, "stream error: %s", ev.Err)
		switch ev.Type {
		case core.StreamEventTypeDelta:
			text.WriteString(ev.Delta.Content)
			reasoning.WriteString(ev.Delta.ReasoningContent)
		case core.StreamEventTypeDone:
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

	body := BuildRequest("", []core.Message{{Role: core.RoleUser, Content: "summarize"}}, nil)
	body.SetMaxOutputTokens(13107)
	res, err := CompactRequest(
		t.Context(),
		srv.Client(),
		core.ModelConfig{Name: "gemini-2.5-flash", BaseURL: srv.URL, APIKey: "k"},
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
		core.ModelConfig{Name: "gemini-2.5-flash", BaseURL: srv.URL, APIKey: "k"},
		&GeminiRequest{Contents: []content{{Role: "user", Parts: []part{{Text: "x"}}}}},
	)
	require.NoError(t, err)
	assert.Equal(t, "partial", res.Text)
	assert.True(t, res.Truncated)
}

func TestCompactRequestSkipsThoughtParts(t *testing.T) {
	for _, tc := range []struct {
		name          string
		body          string
		want          string
		wantTruncated bool
		wantErr       string
	}{
		{
			name: "strips thought parts before summary text",
			body: `{"candidates":[{"content":{"parts":[{"thought":true,"text":"scratchpad"},{"text":"clean summary"}]},"finishReason":"STOP"}]}`,
			want: "clean summary",
		},
		{
			name:          "preserves truncation when max tokens expires inside thoughts",
			body:          `{"candidates":[{"content":{"parts":[{"thought":true,"text":"only thoughts"}]},"finishReason":"MAX_TOKENS"}]}`,
			wantTruncated: true,
		},
		{
			name:    "rejects non-truncated thought-only response as empty",
			body:    `{"candidates":[{"content":{"parts":[{"thought":true,"text":"only thoughts"}]},"finishReason":"STOP"}]}`,
			wantErr: "empty response",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			res, err := CompactRequest(
				t.Context(),
				srv.Client(),
				core.ModelConfig{Name: "gemini-3-flash", BaseURL: srv.URL, APIKey: "k"},
				&GeminiRequest{Contents: []content{{Role: "user", Parts: []part{{Text: "x"}}}}},
			)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, res.Text)
			assert.Equal(t, tc.wantTruncated, res.Truncated)
		})
	}
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
		core.ModelConfig{Name: "gemini-2.5-flash", BaseURL: srv.URL, APIKey: "bad"},
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

	req := BuildRequest("", []core.Message{{Role: core.RoleUser, Content: "hi"}}, nil)
	var gotErr error
	for _, err := range Stream(
		t.Context(),
		srv.Client(),
		core.ModelConfig{Name: "gemini-2.5-flash", BaseURL: srv.URL, APIKey: "bad"},
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

func TestProcessStreamKeepsThoughtSignaturePerCall(t *testing.T) {
	// Gemini signs only the first call of a parallel batch; the second must stay unsigned.
	sse := `data: {"candidates":[{"content":{"parts":[` +
		`{"functionCall":{"name":"read","args":{"path":"a.go"}},"thoughtSignature":"sig-a"},` +
		`{"functionCall":{"name":"read","args":{"path":"b.go"}}}]}}]}` + "\n\n"

	var deltas []core.ToolCall
	var done *core.StreamEvent
	for _, ev := range processForTest(sse) {
		require.NotEqual(t, core.StreamEventTypeError, ev.Type, "stream error: %s", ev.Err)
		switch ev.Type {
		case core.StreamEventTypeDelta:
			deltas = append(deltas, ev.Delta.ToolCalls...)
		case core.StreamEventTypeDone:
			done = &ev
		}
	}

	require.Len(t, deltas, 2)
	assert.Equal(t, "sig-a", deltas[0].ThoughtSignature)
	assert.Empty(t, deltas[1].ThoughtSignature)
	require.NotNil(t, done)
	require.Len(t, done.Final.ToolCalls, 2)
	assert.Equal(t, "sig-a", done.Final.ToolCalls[0].ThoughtSignature)
	assert.Empty(t, done.Final.ToolCalls[1].ThoughtSignature)
}

func TestBuildRequestReplaysThoughtSignature(t *testing.T) {
	req := BuildRequest("", []core.Message{
		{Role: core.RoleUser, Content: "hi"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
			{
				ID: "read", Type: "function", ThoughtSignature: "sig-a",
				Function: core.Function{Name: "read", Arguments: `{"path":"a.go"}`},
			},
			{
				Index: 1, ID: "read", Type: "function",
				Function: core.Function{Name: "read", Arguments: `{"path":"b.go"}`},
			},
		}},
	}, nil)

	require.Len(t, req.Contents, 2)
	parts := req.Contents[1].Parts
	require.Len(t, parts, 2)
	require.NotNil(t, parts[0].FunctionCall)
	assert.Equal(t, "sig-a", parts[0].ThoughtSignature, "signature stays on its own functionCall part")
	assert.Empty(t, parts[1].ThoughtSignature, "signature must not spread to the unsigned call")

	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.Contains(t, string(body),
		`{"functionCall":{"name":"read","args":{"path":"a.go"}},"thoughtSignature":"sig-a"}`)
	assert.Equal(t, 1, strings.Count(string(body), `"thoughtSignature"`))
}

func unsignedCall(path string) core.ToolCall {
	return core.ToolCall{
		ID: "read", Type: "function",
		Function: core.Function{Name: "read", Arguments: `{"path":"` + path + `"}`},
	}
}

func toolResult(out string) core.Message {
	return core.Message{Role: core.RoleTool, ToolCallID: "read", Content: out}
}

func TestBuildRequestSignsUnsignedCurrentTurn(t *testing.T) {
	// History from another provider: nothing in the turn is signed.
	req := BuildRequest("", []core.Message{
		{Role: core.RoleUser, Content: "hi"},
		{Role: core.RoleAssistant, Content: "reading", ToolCalls: []core.ToolCall{
			unsignedCall("a.go"), unsignedCall("b.go"),
		}},
		toolResult("a"), toolResult("b"),
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{unsignedCall("c.go")}},
	}, nil)

	require.Len(t, req.Contents, 4)
	step1 := req.Contents[1].Parts
	require.Len(t, step1, 3)
	assert.Empty(t, step1[0].ThoughtSignature, "text part is never signed")
	assert.Equal(t, skipThoughtSignature, step1[1].ThoughtSignature, "first call of the step")
	assert.Empty(t, step1[2].ThoughtSignature, "later parallel calls stay bare")
	assert.Equal(t, skipThoughtSignature, req.Contents[3].Parts[0].ThoughtSignature, "each step's first call")
}

func TestBuildRequestKeepsRealSignatureOnParallelStep(t *testing.T) {
	signed := unsignedCall("a.go")
	signed.ThoughtSignature = "sig-a"
	calls := []core.ToolCall{signed, unsignedCall("b.go")}
	req := BuildRequest("", []core.Message{
		{Role: core.RoleUser, Content: "hi"},
		{Role: core.RoleAssistant, ToolCalls: calls},
	}, nil)

	parts := req.Contents[1].Parts
	assert.Equal(t, "sig-a", parts[0].ThoughtSignature)
	assert.Empty(t, parts[1].ThoughtSignature)
	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.NotContains(t, string(body), skipThoughtSignature)
	assert.Empty(t, calls[1].ThoughtSignature, "the session's tool calls are not modified")
}

func TestBuildRequestLeavesPreviousTurnsBare(t *testing.T) {
	req := BuildRequest("", []core.Message{
		{Role: core.RoleUser, Content: "first"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{unsignedCall("a.go")}},
		toolResult("a"),
		{Role: core.RoleAssistant, Content: "done"},
		{Role: core.RoleUser, Content: "second"},
	}, nil)

	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "thoughtSignature")
}

func TestThoughtSignatureSurvivesMessageReplay(t *testing.T) {
	sse := `data: {"candidates":[{"content":{"parts":[` +
		`{"functionCall":{"name":"read","args":{"path":"a.go"}},"thoughtSignature":"sig-a"}]}}]}` + "\n\n"
	var final *core.Message
	for _, ev := range processForTest(sse) {
		if ev.Type == core.StreamEventTypeDone {
			final = ev.Final
		}
	}
	require.NotNil(t, final)

	req := BuildRequest("", []core.Message{{Role: core.RoleUser, Content: "hi"}, *final}, nil)
	require.Len(t, req.Contents, 2)
	require.Len(t, req.Contents[1].Parts, 1)
	assert.Equal(t, "sig-a", req.Contents[1].Parts[0].ThoughtSignature)
}
