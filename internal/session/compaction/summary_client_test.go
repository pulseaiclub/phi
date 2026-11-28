package compaction

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/core"
	"github.com/pulseaiclub/phi/internal/llm/client"
)

// compactRespBody renders a finished non-streaming answer carrying text for
// one protocol, mirroring the fixtures in internal/llm/client.
func compactRespBody(api core.RouterType, text string) string {
	switch api {
	case core.Anthropic:
		return `{"content":[{"type":"text","text":"` + text + `"}],"stop_reason":"end_turn"}`
	case core.OpenAIResponses:
		return `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"` + text + `"}]}]}`
	case core.Gemini:
		return `{"candidates":[{"content":{"parts":[{"text":"` + text + `"}]},"finishReason":"STOP"}]}`
	default:
		return `{"choices":[{"message":{"role":"assistant","content":"` + text + `"},"finish_reason":"stop"}]}`
	}
}

// The blank-summary guard sits in the compaction layer, behind every adapter.
// A provider that answers with whitespace must fail closed everywhere. One
// that answers with an empty string keeps its own empty-response error, except
// on chat completions, which passes empty text through to this layer.
func TestCompactBlankSummaryAcrossProtocols(t *testing.T) {
	cases := []struct {
		name          string
		api           core.RouterType
		model         string
		emptyTextWant string // expected error when the model returns ""
		blankWant     string // expected error when the model returns "   "
	}{
		{
			name:          "anthropic",
			api:           core.Anthropic,
			model:         "claude-sonnet-4-20250514",
			emptyTextWant: "anthropic API error: empty response",
			blankWant:     "Summarization failed: model returned an empty summary",
		},
		{
			name:          "chat-completions",
			api:           core.OpenAI,
			model:         "gpt-4o",
			emptyTextWant: "Summarization failed: model returned an empty summary",
			blankWant:     "Summarization failed: model returned an empty summary",
		},
		{
			name:          "responses",
			api:           core.OpenAIResponses,
			model:         "gpt-5",
			emptyTextWant: "LLM API error: empty Responses output",
			blankWant:     "Summarization failed: model returned an empty summary",
		},
		{
			name:          "gemini",
			api:           core.Gemini,
			model:         "gemini-2.5-flash",
			emptyTextWant: "gemini API error: empty response",
			blankWant:     "Summarization failed: model returned an empty summary",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, scenario := range []struct {
				name string
				text string
				want string
			}{
				{"empty string", "", tc.emptyTextWant},
				{"whitespace only", "   ", tc.blankWant},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(compactRespBody(tc.api, scenario.text)))
					}))
					defer srv.Close()

					cli := client.NewClient(
						core.ModelConfig{Name: tc.model, BaseURL: srv.URL, APIKey: "sk-test", API: tc.api},
						client.Hooks{},
						nil,
						"",
					)
					prep := CompactionPreparation{
						MessagesToSummarize: []core.Message{{Role: core.RoleUser, Content: "history to summarize"}},
						ReserveTokens:       16384,
					}

					_, err := Compact(t.Context(), prep, cli)

					require.Error(t, err)
					assert.Contains(t, err.Error(), scenario.want)
				})
			}
		})
	}
}
