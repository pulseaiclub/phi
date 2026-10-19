package anthropic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/util"
)

var benchSink int

// benchConversation builds a long agent session: each turn is a user message,
// an assistant message with two tool calls, the two tool results, and the
// assistant's wrap-up text.
func benchConversation(turns, toolResultBytes int) []llm.Message {
	result := strings.Repeat("line of tool output\n", toolResultBytes/21+1)
	msgs := make([]llm.Message, 0, turns*5)
	for i := range turns {
		msgs = append(msgs,
			llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("please look at item %d", i)},
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
				{
					Index:    0,
					ID:       fmt.Sprintf("toolu_%02d_a", i),
					Type:     "function",
					Function: llm.Function{Name: "read", Arguments: `{"path":"internal/llm/client/client.go"}`},
				},
				{
					Index:    1,
					ID:       fmt.Sprintf("toolu_%02d_b", i),
					Type:     "function",
					Function: llm.Function{Name: "grep", Arguments: `{"pattern":"BuildRequest","path":"internal"}`},
				},
			}},
			llm.Message{Role: llm.RoleTool, ToolCallID: fmt.Sprintf("toolu_%02d_a", i), Content: result},
			llm.Message{Role: llm.RoleTool, ToolCallID: fmt.Sprintf("toolu_%02d_b", i), Content: result},
			llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("item %d is fine", i)},
		)
	}
	return msgs
}

func benchTools(n int) []llm.ToolDefinition {
	tools := make([]llm.ToolDefinition, 0, n)
	for i := range n {
		tools = append(tools, llm.ToolDefinition{
			Name:        fmt.Sprintf("tool_%02d", i),
			Description: strings.Repeat("does a thing. ", 20),
			Params: &llm.FunctionParameters{
				Type: "object",
				Properties: llm.Object{
					"path":    llm.Object{"type": "string", "description": "the path"},
					"pattern": llm.Object{"type": "string", "description": "the pattern"},
					"limit":   llm.Object{"type": "integer"},
				},
				Required: []string{"path"},
			},
		})
	}
	return tools
}

func BenchmarkBuildRequest(b *testing.B) {
	cfg := llm.ModelConfig{Name: "claude-sonnet-4-20250514", APIKey: "k", BaseURL: "https://api.anthropic.com"}
	system := strings.Repeat("You are a coding agent working in a Go repo.\n", 100)
	tools := benchTools(20)
	msgs := benchConversation(20, 16*1024)

	b.ReportAllocs()
	b.SetBytes(int64(totalContentBytes(msgs)))
	for b.Loop() {
		req := BuildRequest(cfg, system, msgs, tools)
		body, err := json.Marshal(req)
		if err != nil {
			b.Fatal(err)
		}
		benchSink = len(body)
	}
}

func totalContentBytes(msgs []llm.Message) int {
	total := 0
	for _, m := range msgs {
		total += len(m.Content)
	}
	return total
}

// benchStream builds an Anthropic SSE body: message_start, n text deltas,
// message_stop.
func benchStream(n int) []byte {
	var sb strings.Builder
	sb.WriteString("event: message_start\n")
	sb.WriteString(
		`data: {"type":"message_start","message":{"usage":{"input_tokens":12000,"cache_read_input_tokens":9000,"cache_creation_input_tokens":120}}}`,
	)
	sb.WriteString("\n\n")
	for i := range n {
		fmt.Fprintf(
			&sb,
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"chunk-%04d \"}}\n\n",
			i,
		)
	}
	sb.WriteString("event: message_stop\n")
	sb.WriteString(`data: {"type":"message_stop"}`)
	sb.WriteString("\n\n")
	return []byte(sb.String())
}

func BenchmarkProcessStream(b *testing.B) {
	body := benchStream(4000)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	for b.Loop() {
		events := 0
		processStream(bytes.NewReader(body), func(ev llm.StreamEvent, err error) bool {
			if err != nil {
				b.Fatal(err)
			}
			events += len(ev.Delta.Content) + len(ev.Delta.ReasoningContent)
			events++
			return true
		})
		benchSink = events
	}
}

// BenchmarkStreamLocal times one whole client-side turn against a loopback
// server, i.e. everything the client does except the internet: build the body,
// marshal it, POST it, and parse the stream back.
func BenchmarkStreamLocal(b *testing.B) {
	cfg := llm.ModelConfig{Name: "claude-sonnet-4-20250514", APIKey: "k"}
	system := strings.Repeat("You are a coding agent working in a Go repo.\n", 100)
	tools := benchTools(20)
	msgs := benchConversation(20, 16*1024)
	body := benchStream(4000)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", util.ContentEventStream)
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	cfg.BaseURL = srv.URL

	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	for b.Loop() {
		req := BuildRequest(cfg, system, msgs, tools)
		for ev, err := range Stream(b.Context(), srv.Client(), cfg, &req) {
			if err != nil {
				b.Fatal(err)
			}
			benchSink += len(ev.Delta.Content)
		}
	}
}
