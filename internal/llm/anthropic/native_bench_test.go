package anthropic

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

type nativeBenchmarkTransport struct{ sse string }

func (t nativeBenchmarkTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(t.sse)), Request: r,
	}, nil
}

func BenchmarkNativeSignedContinuation(b *testing.B) {
	for _, mode := range []string{"mixed", "native_history"} {
		b.Run(mode, func(b *testing.B) {
			cfg := llm.ModelConfig{Name: "claude", API: llm.Anthropic, Think: llm.ThinkConfig{Enabled: true}}
			client := &http.Client{}
			var messages []llm.Message
			rounds := 1
			fixture := nativeSSE
			if mode == "native_history" {
				rounds = 50
				fixture = strings.Replace(nativeSSE, `"text":"before"`, `"text":"`+strings.Repeat("T", 4096)+`"`, 1)
			} else {
				messages = make([]llm.Message, 100)
				for i := range messages {
					role := llm.RoleUser
					if i%2 == 1 {
						role = llm.RoleAssistant
					}
					messages[i] = llm.Message{Role: role, Content: strings.Repeat("synthetic text. ", 137)}
				}
			}
			messages = append(messages, llm.Message{Role: llm.RoleUser, Content: "read"})
			var final *llm.Message
			for round := range rounds {
				client.Transport = nativeBenchmarkTransport{
					sse: strings.ReplaceAll(fixture, "toolu.native", fmt.Sprintf("toolu.%d", round)),
				}
				req := BuildRequest(cfg, "system", messages, nil)
				final = nil
				for ev, err := range Stream(b.Context(), client, cfg, &req) {
					require.NoError(b, err)
					if ev.Final != nil {
						final = ev.Final
					}
				}
				require.NotNil(b, final)
				require.NotNil(b, final.ProviderState)
				messages = append(messages, *final, llm.Message{
					Role: llm.RoleTool, ToolCallID: final.ToolCalls[0].ID, Content: "file",
				})
			}
			client.Transport = nativeBenchmarkTransport{sse: nativeSSE}
			req := BuildRequest(cfg, "system", messages, nil)
			require.Equal(b, final.ProviderState.Data.Items, req.Messages[len(req.Messages)-2].Content)
			body, err := json.Marshal(req)
			require.NoError(b, err)
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for b.Loop() {
				req := BuildRequest(cfg, "system", messages, nil)
				for ev, err := range Stream(b.Context(), client, cfg, &req) {
					require.NoError(b, err)
					if ev.Final != nil {
						final = ev.Final
					}
				}
			}
		})
	}
}
