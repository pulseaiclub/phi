package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools"
	"github.com/stretchr/testify/require"
)

// sseToolCallChunk encodes one SSE data line carrying a full tool-call delta.
func sseToolCallChunk(id, name, args string) string {
	payload, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"delta": map[string]any{
				"role":    "assistant",
				"content": "",
				"tool_calls": []any{map[string]any{
					"index":    0,
					"id":       id,
					"type":     "function",
					"function": map[string]any{"name": name, "arguments": args},
				}},
			},
		}},
	})
	if err != nil {
		panic(err)
	}
	return "data: " + string(payload) + "\n\n"
}

func sseTextChunk(text string) string {
	payload, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"delta": map[string]any{
				"role":    "assistant",
				"content": text,
			},
		}},
	})
	if err != nil {
		panic(err)
	}
	return "data: " + string(payload) + "\n\n"
}

// fakeToolSequenceServer returns tool calls for finalAfter tool requests, then
// returns a final text response. A negative finalAfter means tool calls forever.
func fakeToolSequenceServer(finalAfter int) (*httptest.Server, *atomic.Int32) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		request := requests.Add(1)
		if finalAfter < 0 || int(request) <= finalAfter {
			_, _ = fmt.Fprint(w, sseToolCallChunk(fmt.Sprintf("call_%d", request), "count", `{}`))
		} else {
			_, _ = fmt.Fprint(w, sseTextChunk("done"))
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	return server, &requests
}

func countingTool(runs *atomic.Int32) tools.Tool {
	return tools.Tool{
		Definition: llm.ToolDefinition{
			Name:        "count",
			Description: "count tool executions",
			Params:      &llm.FunctionParameters{Type: "object"},
		},
		Run: func(context.Context, json.RawMessage) (tools.Result, error) {
			runs.Add(1)
			return tools.Result{Content: "ok"}, nil
		},
	}
}

func newRoundTestEngine(t *testing.T, serverURL string, runs *atomic.Int32) *Engine {
	t.Helper()
	engine, err := NewEngine(EngineOpts{
		Model:       llm.ModelConfig{Name: "fake", BaseURL: serverURL, APIKey: "x"},
		SessionOpts: SessionOpts{Cwd: t.TempDir()},
		Gate:        permission.AllowAll{},
		Tools:       []tools.Tool{countingTool(runs)},
	})
	require.NoError(t, err)
	return engine
}

func TestLoopMaxRoundsAllowsFinalAnswerAfterLastToolRound(t *testing.T) {
	server, requests := fakeToolSequenceServer(2)
	defer server.Close()

	var runs atomic.Int32
	engine := newRoundTestEngine(t, server.URL, &runs)
	require.NoError(t, engine.SetMaxRounds(2))

	var lastErr error
	var finalText string
	for ev, err := range engine.Loop(context.Background(), "go", LoopOpts{}) {
		if err != nil {
			lastErr = err
			break
		}
		if update, ok := ev.(session.AssistantMessageUpdate); ok && update.Message.State == session.StateComplete {
			finalText = update.Message.FlatText()
		}
	}
	require.NoError(t, lastErr)
	require.Equal(t, int32(2), runs.Load())
	require.Equal(t, int32(3), requests.Load())
	require.Equal(t, "done", finalText)
}

func TestLoopMaxRoundsDoesNotExecuteExtraToolRound(t *testing.T) {
	server, requests := fakeToolSequenceServer(-1)
	defer server.Close()

	var runs atomic.Int32
	engine := newRoundTestEngine(t, server.URL, &runs)
	require.NoError(t, engine.SetMaxRounds(2))

	var lastErr error
	for ev, err := range engine.Loop(context.Background(), "go", LoopOpts{}) {
		_ = ev
		if err != nil {
			lastErr = err
			break
		}
	}
	require.Error(t, lastErr, "loop should stop when the model requests a third tool round")
	if !errors.Is(lastErr, ErrMaxRounds) {
		t.Fatalf("expected ErrMaxRounds to be wrapped, got %v", lastErr)
	}
	require.Equal(t, int32(2), runs.Load())
	require.Equal(t, int32(3), requests.Load())

	assistantToolRounds := 0
	for _, msg := range engine.session.BuildContext() {
		if msg.Role == llm.RoleAssistant && len(msg.ToolCalls) > 0 {
			assistantToolRounds++
		}
	}
	require.Equal(t, 2, assistantToolRounds)
}

func TestSetMaxRoundsRejectsNonPositive(t *testing.T) {
	engine, err := NewEngine(EngineOpts{
		Model:       llm.ModelConfig{Name: "fake", BaseURL: "http://unused", APIKey: "x"},
		SessionOpts: SessionOpts{Cwd: t.TempDir()},
		Gate:        permission.AllowAll{},
	})
	require.NoError(t, err)
	require.Error(t, engine.SetMaxRounds(0))
	require.Error(t, engine.SetMaxRounds(-1))
	require.NoError(t, engine.SetMaxRounds(1))
}
