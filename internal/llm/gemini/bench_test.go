package gemini

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/pulseaiclub/phi/internal/llm"
)

var benchSink int

// benchToolCallMessages is a turn whose assistant message carries the tool calls
// a coding agent actually issues: many calls with small argument objects.
func benchToolCallMessages(calls int) []llm.Message {
	const args = `{"path":"internal/llm/gemini/client.go","pattern":"toolArgs","limit":200}`
	toolCalls := make([]llm.ToolCall, 0, calls)
	for i := range calls {
		toolCalls = append(toolCalls, llm.ToolCall{
			Index:    i,
			ID:       fmt.Sprintf("call_%02d", i),
			Type:     "function",
			Function: llm.Function{Name: "read", Arguments: args},
		})
	}
	return []llm.Message{
		{Role: llm.RoleUser, Content: "look around"},
		{Role: llm.RoleAssistant, ToolCalls: toolCalls},
	}
}

func BenchmarkBuildRequestToolCalls(b *testing.B) {
	msgs := benchToolCallMessages(20)

	b.ReportAllocs()
	for b.Loop() {
		req := BuildRequest("", msgs, nil)
		body, err := json.Marshal(req)
		if err != nil {
			b.Fatal(err)
		}
		benchSink = len(body)
	}
}
