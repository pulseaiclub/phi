package interactions

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

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
						`{"id":"part-1","status":"requires_continuation","continuation_token":"cursor","steps":[{"type":"model_output","content":[{"type":"text","text":"first "}]}]}`,
					)
					return
				}
				_, _ = io.WriteString(
					w,
					`{"id":"part-2","status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"second"}]}]}`,
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
