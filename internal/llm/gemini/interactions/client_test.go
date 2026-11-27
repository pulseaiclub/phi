package interactions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

func TestStreamExpiredInteraction(t *testing.T) {
	jsonError := func(message string) string {
		return fmt.Sprintf(`{"error":{"message":%q}}`, message)
	}
	sseError := func(code, message string) string {
		return fmt.Sprintf("data: "+`{"event_type":"error","error":{"code":%q,"message":%q}}`+"\n\n", code, message)
	}
	const next = `{"id":"part","status":"requires_continuation","continuation_token":"opaque"}`
	const poll = `{"id":"job","status":"in_progress"}`
	const created = "data: " + `{"event_type":"interaction.created","interaction":{"id":"new"}}` + "\n\n"
	const output = "data: " + `{"event_type":"step.start","index":0,"step":{"type":"model_output","content":[{"type":"text","text":"partial"}]}}` + "\n\n"
	missing := sseError("not_found", "Interaction not found.")
	for _, tc := range []struct {
		label  string
		status int
		body   string
		setup  string
		replay bool
	}{
		{"expired", 404, jsonError("Interaction with id 'saved' not found"), "", true},
		{"expired parameter", 400, jsonError("previous_interaction_id has expired"), "", true},
		{"missing interaction", 404, jsonError("Interaction not found"), "", true},
		{"resource details", 404, `{"error":{"details":[{"@type":"type.googleapis.com/google.rpc.ResourceInfo","resourceName":"projects/p/interactions/saved"}]}}`, "", true},
		{"missing model", 404, jsonError("model not found"), "", false},
		{"different interaction", 404, jsonError("Interaction with id 'other' not found"), "", false},
		{"authentication", 401, jsonError("previous interaction not found"), "", false},
		{"authorization", 403, jsonError("previous interaction not found"), "", false},
		{"first stream event", 200, missing, "", true},
		{"error response with stream body", 400, sseError("invalid_request", "previous interaction has expired"), "", true},
		{"unrelated stream failure", 200, sseError("not_found", "Failed to get completed interaction: Result not found."), "", false},
		{"stream authorization", 200, sseError("permission_denied", "Interaction not found"), "", false},
		{"early disconnect", 200, ": heartbeat\n\n", "", false},
		{"accepted interaction", 200, created + missing, "", false},
		{"partial output", 200, output + missing, "", false},
		{"continuation", 404, jsonError("Interaction not found"), next, false},
		{"poll", 404, jsonError("Interaction not found"), poll, false},
	} {
		t.Run(tc.label, func(t *testing.T) {
			var inputs []request
			var methods []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body request
				if r.Method == http.MethodPost {
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
				}
				inputs = append(inputs, body)
				methods = append(methods, r.Method)
				response, status := `{"id":"new","status":"completed","output_text":"done"}`, 200
				switch {
				case tc.setup != "" && len(inputs) == 1:
					response = tc.setup
				case len(inputs) == 1 || (tc.setup != "" && len(inputs) == 2):
					response, status = tc.body, tc.status
				}
				if strings.HasPrefix(response, "{") {
					w.Header().Set("Content-Type", "application/json")
				} else {
					w.Header().Set("Content-Type", "text/event-stream")
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, response)
			}))
			defer server.Close()

			cfg := llm.ModelConfig{API: llm.GeminiInteractions, Name: "gemini-test", BaseURL: server.URL}
			if tc.setup == poll {
				cfg.RequestParameters = llm.RequestParameters{"background": true}
			}
			history := []llm.Message{{Role: llm.RoleUser, Content: "original question"}}
			first, err := buildRequest(cfg, "system", history, nil, true, 0)
			require.NoError(t, err)
			assistant := llm.Message{Role: llm.RoleAssistant, Content: "answer"}
			items := []json.RawMessage{
				json.RawMessage(`{"type":"thought","signature":"saved-signature"}`),
				json.RawMessage(`{"type":"model_output","content":[{"type":"text","text":"answer"}]}`),
			}
			assistant.ProviderState = first.providerState("saved", items, assistant)
			history = append(history, assistant, llm.Message{Role: llm.RoleUser, Content: "follow-up"})
			var final *llm.Message
			var streamErr error
			for event, err := range Stream(t.Context(), server.Client(), cfg, "system", history, nil) {
				if err != nil {
					streamErr = err
				}
				if event.Final != nil {
					final = event.Final
				}
			}
			require.NotEmpty(t, inputs)
			require.Equal(t, "saved", inputs[0].PreviousInteractionID)
			require.Len(t, inputs[0].Input, 1)
			if !tc.replay {
				require.Error(t, streamErr)
				switch tc.setup {
				case next:
					require.Equal(t, []string{"POST", "POST"}, methods)
					require.Equal(t, "opaque", inputs[1].ContinuationToken)
				case poll:
					require.Equal(t, []string{"POST", "GET"}, methods)
				default:
					require.Equal(t, []string{"POST"}, methods)
				}
				if tc.status >= 400 {
					var apiErr *llm.APIError
					require.ErrorAs(t, streamErr, &apiErr)
					require.Equal(t, tc.status, apiErr.StatusCode)
				}
				return
			}
			require.NoError(t, streamErr)
			require.Len(t, inputs, 2)
			require.True(t, inputs[1].Store)
			require.Empty(t, inputs[1].PreviousInteractionID)
			require.Len(t, inputs[1].Input, 4)
			require.JSONEq(t, string(items[0]), string(inputs[1].Input[1]))
			require.NotNil(t, final)
			require.Equal(t, "new", final.ProviderState.Data.InteractionID)
		})
	}
}

func TestContinuationRequests(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store bool
	}{{"stored", true}, {"stateless", false}} {
		t.Run(tc.name, func(t *testing.T) {
			var requests []map[string]json.RawMessage
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				requests = append(requests, body)
				w.Header().Set("Content-Type", "application/json")
				if len(requests) == 1 {
					_, _ = io.WriteString(
						w,
						`{"id":"part-1","status":"requires_continuation","continuation_token":"cursor","usage":{"total_tokens":150000},"steps":[{"type":"model_output","content":[{"type":"text","text":"first "}]}]}`,
					)
					return
				}
				_, _ = io.WriteString(
					w,
					`{"id":"part-2","status":"completed","usage":{"total_tokens":210000},"steps":[{"type":"model_output","content":[{"type":"text","text":"second"}]}]}`,
				)
			}))
			defer server.Close()
			cfg := llm.ModelConfig{
				API: llm.GeminiInteractions, Name: "gemini", BaseURL: server.URL,
				RequestParameters: llm.RequestParameters{"store": tc.store},
			}
			var finals []*llm.Message
			for ev, err := range Stream(t.Context(), server.Client(), cfg, "instructions", []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil) {
				require.NoError(t, err)
				if ev.Final != nil {
					finals = append(finals, ev.Final)
				}
			}
			require.Len(t, finals, 1)
			require.Equal(t, "first second", finals[0].Content)
			require.Equal(t, 360_000, finals[0].Usage.TotalTokens)
			require.Equal(t, 210_000, finals[0].Usage.ContextTokens())
			require.Len(t, requests, 2)
			require.JSONEq(t, `"cursor"`, string(requests[1]["continuation_token"]))
			require.JSONEq(t, `"instructions"`, string(requests[1]["system_instruction"]))
			if tc.store {
				require.NotContains(t, requests[1], "input")
				require.JSONEq(t, `"part-1"`, string(requests[1]["previous_interaction_id"]))
				require.Equal(t, "part-2", finals[0].ProviderState.Data.InteractionID)
			} else {
				require.JSONEq(t, string(requests[0]["input"]), string(requests[1]["input"]))
				require.NotContains(t, requests[1], "previous_interaction_id")
				require.Empty(t, finals[0].ProviderState.Data.InteractionID)
			}
		})
	}
}

func TestBackgroundUsage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage string
		want  int
	}{
		{"initial input reported", `"initial_input_tokens":1000,`, 201_000},
		{"estimate original prompt", "", 200_002},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"job","status":"completed","output_text":"done","usage":{`+tc.usage+
					`"total_input_tokens":397216,"total_output_tokens":150000,"total_thought_tokens":50000,"total_tokens":597216}}`)
			}))
			defer server.Close()
			cfg := llm.ModelConfig{
				API: llm.GeminiInteractions, BaseURL: server.URL, Name: "gemini",
				RequestParameters: llm.RequestParameters{"background": true},
			}
			history := []llm.Message{{Role: llm.RoleUser, Content: "abcd"}}
			var final *llm.Message
			for event, err := range Stream(t.Context(), server.Client(), cfg, "test", history, nil) {
				require.NoError(t, err)
				if event.Final != nil {
					final = event.Final
				}
			}
			require.NotNil(t, final)
			require.Equal(t, 597_216, final.Usage.TotalTokens)
			require.Equal(t, tc.want, final.Usage.ContextTokens())
		})
	}
}

func TestBackgroundResumesFromLastEvent(t *testing.T) {
	var posts, gets int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if r.Method == http.MethodPost {
			posts++
			_, _ = io.WriteString(
				w,
				`data: {"event_type":"interaction.created","event_id":"e1","interaction":{"id":"job","status":"in_progress"}}

data: {"event_type":"step.start","event_id":"e2","index":0,"step":{"type":"model_output"}}

data: {"event_type":"step.delta","event_id":"e3","index":0,"delta":{"type":"text","text":"one"}}

`,
			)
			return
		}
		if r.URL.Path != "/v1beta/interactions/job" || r.URL.Query().Get("last_event_id") != "e3" ||
			r.URL.Query().Get("stream") != "true" || r.URL.Query().Get("tenant") != "one" ||
			r.URL.Query().Get("include_input") != "false" {
			http.Error(w, "incorrect resume cursor", http.StatusBadRequest)
			return
		}
		gets++
		_, _ = io.WriteString(
			w,
			`data: {"event_type":"step.delta","event_id":"e4","index":0,"delta":{"type":"text","text":" two"}}

data: {"event_type":"step.stop","event_id":"e5","index":0}

data: {"event_type":"interaction.completed","event_id":"e6","interaction":{"id":"job","status":"completed"}}

`,
		)
	}))
	defer server.Close()
	cfg := llm.ModelConfig{
		API: llm.GeminiInteractions, Name: "gemini", BaseURL: server.URL + "?tenant=one",
		RequestParameters: llm.RequestParameters{"background": true},
	}
	var final *llm.Message
	for ev, err := range Stream(t.Context(), server.Client(), cfg, "", []llm.Message{{Role: llm.RoleUser, Content: "go"}}, nil) {
		require.NoError(t, err)
		if ev.Final != nil {
			final = ev.Final
		}
	}
	require.NotNil(t, final)
	require.Equal(t, "one two", final.Content)
	require.Equal(t, 1, posts)
	require.Equal(t, 1, gets)
}

func TestCompactHonorsOutputCap(t *testing.T) {
	var body map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(
			w,
			`{"id":"summary","status":"incomplete","steps":[{"type":"model_output","content":[{"type":"text","text":"partial"}]}]}`,
		)
	}))
	defer server.Close()
	cfg := llm.ModelConfig{API: llm.GeminiInteractions, Name: "gemini", BaseURL: server.URL}
	result, err := Compact(t.Context(), server.Client(), cfg, llm.CompactRequest{Prompt: "summarize", MaxTokens: 123})
	require.NoError(t, err)
	require.Equal(t, llm.CompactResult{Text: "partial", Truncated: true}, result)
	require.JSONEq(t, "false", string(body["stream"]))
	require.Contains(t, string(body["generation_config"]), `"max_output_tokens":123`)
	require.NotContains(t, body, "previous_interaction_id")
}

func TestBackgroundCreateDoesNotRetryGatewayFailure(t *testing.T) {
	var posts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		posts++
		http.Error(w, "gateway lost upstream response", http.StatusBadGateway)
	}))
	defer server.Close()
	cfg := llm.ModelConfig{
		API: llm.GeminiInteractions, Name: "gemini", BaseURL: server.URL,
		RequestParameters: llm.RequestParameters{"background": true},
	}
	var streamErr error
	for _, err := range Stream(t.Context(), server.Client(), cfg, "", []llm.Message{{Role: llm.RoleUser, Content: "go"}}, nil) {
		streamErr = err
	}
	require.Error(t, streamErr)
	require.Equal(t, 1, posts)
}

func TestForegroundResumeRequiresStoredStreamAndCursor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		store   bool
		cursor  string
		resumes bool
	}{
		{"stored with cursor", true, "e1", true},
		{"stored without cursor", true, "", false},
		{"stateless with cursor", false, "e1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gets int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if r.Method == http.MethodPost {
					_, _ = io.WriteString(w, `data: {"event_type":"interaction.created","event_id":"`+tc.cursor+
						`","interaction":{"id":"fg","status":"in_progress"}}`+"\n\n")
					return
				}
				gets++
				if r.URL.Path != "/v1beta/interactions/fg" || r.URL.Query().Get("stream") != "true" ||
					r.URL.Query().Get("last_event_id") != tc.cursor {
					http.Error(w, "unexpected cursor or interaction", http.StatusBadRequest)
					return
				}
				if gets == 1 {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				_, _ = io.WriteString(
					w,
					`data: {"event_type":"interaction.completed","interaction":{"id":"fg","status":"completed","output_text":"ok"}}`+"\n\n",
				)
			}))
			defer server.Close()
			cfg := llm.ModelConfig{
				API: llm.GeminiInteractions, Name: "gemini", BaseURL: server.URL,
				RequestParameters: llm.RequestParameters{"store": tc.store},
			}
			var final *llm.Message
			var streamErr error
			for ev, err := range Stream(t.Context(), server.Client(), cfg, "", []llm.Message{{Role: llm.RoleUser, Content: "go"}}, nil) {
				final, streamErr = ev.Final, err
			}
			if tc.resumes {
				require.NoError(t, streamErr)
				require.NotNil(t, final)
				require.Equal(t, "ok", final.Content)
				require.Equal(t, 2, gets)
			} else {
				require.ErrorIs(t, streamErr, io.ErrUnexpectedEOF)
				require.Zero(t, gets)
			}
		})
	}
}

func TestCancelBackgroundInteractionOnStop(t *testing.T) {
	for _, tc := range []struct {
		name        string
		completed   bool
		stopReading bool
		wantCancel  bool
	}{
		{"cancel during stream", false, false, true},
		{"consumer stops", false, true, true},
		{"completed before cancellation", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cancels := make(chan bool, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/cancel") {
					cancels <- r.Method == http.MethodPost && r.URL.Path == "/v1beta/interactions/job/cancel" &&
						r.URL.Query().Get("tenant") == "one" && r.Header.Get("X-Goog-Api-Key") == "key" &&
						r.Header.Get("Api-Revision") != ""
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.completed {
					_, _ = io.WriteString(
						w,
						`data: {"event_type":"interaction.completed","interaction":{"id":"job","status":"completed","output_text":"done"}}`+"\n\n",
					)
					return
				}
				_, _ = io.WriteString(
					w,
					`data: {"event_type":"interaction.created","interaction":{"id":"job","status":"in_progress"}}

data: {"event_type":"step.start","event_id":"e1","index":0,"step":{"type":"model_output","content":[{"type":"text","text":"partial"}]}}

`,
				)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cfg := llm.ModelConfig{
				API: llm.GeminiInteractions, Name: "gemini", APIKey: "key", BaseURL: server.URL + "?tenant=one",
				RequestParameters: llm.RequestParameters{"background": true},
			}
			var streamErr error
			for ev, err := range Stream(ctx, server.Client(), cfg, "", []llm.Message{{Role: llm.RoleUser, Content: "go"}}, nil) {
				streamErr = err
				if ev.Delta.Content != "" {
					if tc.stopReading {
						break
					}
					cancel()
				}
			}
			if !tc.stopReading && !tc.completed {
				require.ErrorIs(t, streamErr, context.Canceled)
			}
			if tc.wantCancel {
				require.Len(t, cancels, 1)
				require.True(t, <-cancels)
			} else {
				require.Empty(t, cancels)
			}
		})
	}
}
