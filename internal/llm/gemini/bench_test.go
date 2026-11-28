package gemini

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/pulseaiclub/phi/internal/core"
)

var benchSink int

// benchToolCallMessages is a turn whose assistant message carries the tool calls
// a coding agent actually issues: many calls with small argument objects.
func benchToolCallMessages(calls int) []core.Message {
	const args = `{"path":"internal/llm/gemini/client.go","pattern":"toolArgs","limit":200}`
	toolCalls := make([]core.ToolCall, 0, calls)
	for i := range calls {
		toolCalls = append(toolCalls, core.ToolCall{
			Index:    i,
			ID:       fmt.Sprintf("call_%02d", i),
			Type:     "function",
			Function: core.Function{Name: "read", Arguments: args},
		})
	}
	return []core.Message{
		{Role: core.RoleUser, Content: "look around"},
		{Role: core.RoleAssistant, ToolCalls: toolCalls},
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
