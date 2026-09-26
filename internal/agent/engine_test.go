package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools"
)

// compactionSeed is one seeded turn's content, about 12k estimated tokens (4
// chars per token). Two of them exceed the default keepRecentTokens (20k), so
// force-compact finds an older prefix to summarize.
var compactionSeed = strings.Repeat("x", 12000*4)

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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

func newRoundTestEngine(t *testing.T, serverURL string, runs *atomic.Int32, maxRounds int) *Engine {
	t.Helper()
	sess, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	engine, err := NewEngine(
		llm.ModelConfig{Name: "fake", BaseURL: serverURL, APIKey: "x"},
		sess,
		WithGate(permission.AllowAll{}),
		WithTools([]tools.Tool{countingTool(runs)}),
		WithMaxRounds(maxRounds),
	)
	require.NoError(t, err)
	return engine
}

func TestLoopMaxRoundsAllowsFinalAnswerAfterLastToolRound(t *testing.T) {
	server, requests := fakeToolSequenceServer(2)
	defer server.Close()

	var runs atomic.Int32
	engine := newRoundTestEngine(t, server.URL, &runs, 2)

	var lastErr error
	var finalText string
	for ev, err := range engine.Loop(t.Context(), "go", LoopOpts{}) {
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
	engine := newRoundTestEngine(t, server.URL, &runs, 2)

	var lastErr error
	for ev, err := range engine.Loop(t.Context(), "go", LoopOpts{}) {
		_ = ev
		if err != nil {
			lastErr = err
			break
		}
	}
	require.Error(t, lastErr, "loop should stop when the model requests a third tool round")
	require.ErrorIs(t, lastErr, ErrMaxRounds)
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

func TestLoopContinueAskGrantsAnotherBudget(t *testing.T) {
	server, _ := fakeToolSequenceServer(-1)
	defer server.Close()

	var asks atomic.Int32
	sess, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	engine, err := NewEngine(
		llm.ModelConfig{Name: "fake", BaseURL: server.URL, APIKey: "x"},
		sess,
		WithGate(permission.AllowAll{}),
		WithMaxRounds(1),
		WithContinueAsk(func(context.Context, int) (bool, error) {
			// Approve once so the loop can start a second budget window, then stop.
			return asks.Add(1) == 1, nil
		}),
	)
	require.NoError(t, err)

	var lastErr error
	for ev, err := range engine.Loop(t.Context(), "go", LoopOpts{}) {
		_ = ev
		if err != nil {
			lastErr = err
			break
		}
	}
	require.Error(t, lastErr)
	require.ErrorIs(t, lastErr, ErrMaxRounds)
	require.Equal(t, int32(2), asks.Load(), "should ask once per exhausted budget")
}

func TestLoopContinueAskDeclineReturnsErrMaxRounds(t *testing.T) {
	server, _ := fakeToolSequenceServer(-1)
	defer server.Close()

	sess, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	engine, err := NewEngine(
		llm.ModelConfig{Name: "fake", BaseURL: server.URL, APIKey: "x"},
		sess,
		WithGate(permission.AllowAll{}),
		WithMaxRounds(1),
		WithContinueAsk(func(context.Context, int) (bool, error) {
			return false, nil
		}),
	)
	require.NoError(t, err)

	var lastErr error
	for ev, err := range engine.Loop(t.Context(), "go", LoopOpts{}) {
		_ = ev
		if err != nil {
			lastErr = err
			break
		}
	}
	require.ErrorIs(t, lastErr, ErrMaxRounds)
}

// overflowThenOKServer fails the first streaming chat with a context overflow,
// serves a non-stream compact summary, then streams a final text reply.
func overflowThenOKServer(streamHits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if stream, _ := body["stream"].(bool); stream {
			n := streamHits.Add(1)
			if n == 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"prompt is too long: 210000 > 200000 tokens"}}`))
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, sseTextChunk("recovered"))
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		// Compaction Compact() is non-streaming chat.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": "prior work summarized"},
			}},
		})
	}))
}

func TestLoopOverflowCompactsAndRetries(t *testing.T) {
	var streamHits atomic.Int32
	server := overflowThenOKServer(&streamHits)
	defer server.Close()

	sess, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	// Seed a history larger than force-compact's budget (default
	// keepRecentTokens is 20k). The cut weighs message size, not the reported
	// usage, so the content has to be big: the last two messages alone must
	// exceed the budget, leaving an older prefix to summarize.
	require.NoError(t, sess.Append(
		llm.Message{Role: llm.RoleUser, Content: compactionSeed, Usage: llm.Usage{TotalTokens: 12000}},
		llm.Message{Role: llm.RoleAssistant, Content: compactionSeed, Usage: llm.Usage{TotalTokens: 24000}},
		llm.Message{Role: llm.RoleUser, Content: compactionSeed, Usage: llm.Usage{TotalTokens: 36000}},
		llm.Message{Role: llm.RoleAssistant, Content: compactionSeed, Usage: llm.Usage{TotalTokens: 50000}},
	))

	engine, err := NewEngine(
		llm.ModelConfig{Name: "fake", BaseURL: server.URL, APIKey: "x", ContextWindow: 200_000},
		sess,
		WithGate(permission.AllowAll{}),
		WithTools([]tools.Tool{}),
	)
	require.NoError(t, err)

	var lastErr error
	var finalText string
	var sawCompact bool
	for ev, err := range engine.Loop(t.Context(), "continue", LoopOpts{}) {
		if err != nil {
			lastErr = err
			break
		}
		switch e := ev.(type) {
		case session.CompactionStarted:
			sawCompact = true
		case session.AssistantMessageUpdate:
			if e.Message.State == session.StateComplete {
				finalText = e.Message.FlatText()
			}
		}
	}
	require.NoError(t, lastErr)
	require.True(t, sawCompact, "expected overflow path to emit CompactionStarted")
	require.Equal(t, int32(2), streamHits.Load(), "overflow then one retry stream")
	require.Equal(t, "recovered", finalText)
}

func TestLoopOverflowFailsClosedAfterOneRetry(t *testing.T) {
	var streamHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if stream, _ := body["stream"].(bool); stream {
			streamHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"prompt is too long: 210000 > 200000 tokens"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": "summary"},
			}},
		})
	}))
	defer server.Close()

	sess, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	require.NoError(t, sess.Append(
		llm.Message{Role: llm.RoleUser, Content: compactionSeed, Usage: llm.Usage{TotalTokens: 12000}},
		llm.Message{Role: llm.RoleAssistant, Content: compactionSeed, Usage: llm.Usage{TotalTokens: 24000}},
		llm.Message{Role: llm.RoleUser, Content: compactionSeed, Usage: llm.Usage{TotalTokens: 36000}},
		llm.Message{Role: llm.RoleAssistant, Content: compactionSeed, Usage: llm.Usage{TotalTokens: 50000}},
	))

	engine, err := NewEngine(
		llm.ModelConfig{Name: "fake", BaseURL: server.URL, APIKey: "x", ContextWindow: 200_000},
		sess,
		WithGate(permission.AllowAll{}),
		WithTools([]tools.Tool{}),
	)
	require.NoError(t, err)

	var lastErr error
	for ev, err := range engine.Loop(t.Context(), "continue", LoopOpts{}) {
		_ = ev
		if err != nil {
			lastErr = err
			break
		}
	}
	require.Error(t, lastErr)
	require.True(t, llm.IsContextOverflow(lastErr))
	require.Equal(t, int32(2), streamHits.Load(), "one recovery attempt then fail")
}

// A summary that stopped at the output cap is incomplete. It must not become
// the session checkpoint: the loop surfaces the original overflow and the
// history stays where it was.
func TestLoopOverflowRejectsCappedSummary(t *testing.T) {
	var streamHits atomic.Int32
	var compactMaxTokens float64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if stream, _ := body["stream"].(bool); stream {
			streamHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"prompt is too long: 210000 > 200000 tokens"}}`))
			return
		}
		compactMaxTokens, _ = body["max_tokens"].(float64)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]any{"role": "assistant", "content": "truncated summary"},
				"finish_reason": "length",
			}},
		})
	}))
	defer server.Close()

	sess, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	require.NoError(t, sess.Append(
		llm.Message{Role: llm.RoleUser, Content: compactionSeed, Usage: llm.Usage{TotalTokens: 12000}},
		llm.Message{Role: llm.RoleAssistant, Content: compactionSeed, Usage: llm.Usage{TotalTokens: 24000}},
		llm.Message{Role: llm.RoleUser, Content: compactionSeed, Usage: llm.Usage{TotalTokens: 36000}},
		llm.Message{Role: llm.RoleAssistant, Content: compactionSeed, Usage: llm.Usage{TotalTokens: 50000}},
	))

	engine, err := NewEngine(
		llm.ModelConfig{Name: "fake", BaseURL: server.URL, APIKey: "x", ContextWindow: 200_000},
		sess,
		WithGate(permission.AllowAll{}),
		WithTools([]tools.Tool{}),
	)
	require.NoError(t, err)

	var lastErr error
	var sawFailure bool
	for ev, err := range engine.Loop(t.Context(), "continue", LoopOpts{}) {
		if err != nil {
			lastErr = err
			break
		}
		if e, ok := ev.(session.CompactionComplete); ok && e.Failed {
			sawFailure = true
		}
	}

	require.True(t, sawFailure, "a capped summary must report a failed compaction")
	require.Error(t, lastErr)
	require.True(t, llm.IsContextOverflow(lastErr), "the original overflow surfaces unchanged")
	require.Equal(t, int32(1), streamHits.Load(), "no retry on a failed compact")
	// Default reserveTokens is 16384, so the history summary is capped at 0.8x.
	require.InDelta(t, 13107, compactMaxTokens, 0.5)
	for _, entry := range sess.PathEntries() {
		require.NotEqual(t, session.EntryCompaction, entry.GetType(), "no checkpoint was persisted")
	}
}

func TestLoopNonOverflowErrorDoesNotCompact(t *testing.T) {
	var streamHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		streamHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided"}}`))
	}))
	defer server.Close()

	sess, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	engine, err := NewEngine(
		llm.ModelConfig{Name: "fake", BaseURL: server.URL, APIKey: "x"},
		sess,
		WithGate(permission.AllowAll{}),
		WithTools([]tools.Tool{}),
	)
	require.NoError(t, err)

	var lastErr error
	var sawCompact bool
	for ev, err := range engine.Loop(t.Context(), "go", LoopOpts{}) {
		if err != nil {
			lastErr = err
			break
		}
		if _, ok := ev.(session.CompactionStarted); ok {
			sawCompact = true
		}
	}
	require.Error(t, lastErr)
	require.False(t, llm.IsContextOverflow(lastErr))
	require.False(t, sawCompact)
	require.Equal(t, int32(1), streamHits.Load())
}

// midTurnCompactServer streams a tool call, then a final reply, and serves the
// non-streaming compaction summary. Streaming request bodies are recorded so a
// test can see what the model was sent after the mid-turn compaction.
func midTurnCompactServer(bodies *[]map[string]any, compactHits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if stream, _ := body["stream"].(bool); stream {
			*bodies = append(*bodies, body)
			w.Header().Set("Content-Type", "text/event-stream")
			if len(*bodies) == 1 {
				_, _ = fmt.Fprint(w, sseToolCallChunk("call_1", "count", `{}`))
			} else {
				_, _ = fmt.Fprint(w, sseTextChunk("done"))
			}
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		compactHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": "prior work summarized"},
			}},
		})
	}))
}

// sentContent concatenates the message contents of one request body.
func sentContent(body map[string]any) string {
	var b strings.Builder
	messages, _ := body["messages"].([]any)
	for _, raw := range messages {
		message, _ := raw.(map[string]any)
		content, _ := message["content"].(string)
		b.WriteString(content)
	}
	return b.String()
}

// bulkyTool answers with a result big enough to push the estimated context past
// the threshold on its own.
func bulkyTool() tools.Tool {
	return tools.Tool{
		Definition: llm.ToolDefinition{
			Name:        "count",
			Description: "answer with a large payload",
			Params:      &llm.FunctionParameters{Type: "object"},
		},
		Run: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Content: strings.Repeat("y", 12000)}, nil
		},
	}
}

// A turn that keeps calling tools can cross the window while it runs, which the
// turn-end check never sees. The threshold is checked before every request, so
// the summarized prefix is gone from the next one.
func TestLoopCompactsMidTurnBeforeNextRequest(t *testing.T) {
	var bodies []map[string]any
	var compactHits atomic.Int32
	server := midTurnCompactServer(&bodies, &compactHits)
	defer server.Close()

	sess, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	// Four seeded turns of ~12k estimated tokens each. The reported usage of the
	// last one (22k) sits below the threshold of a 40k window (23616), so nothing
	// compacts until the tool result lands on top of it.
	require.NoError(t, sess.Append(
		llm.Message{Role: llm.RoleUser, Content: strings.Repeat("a", 48000), Usage: llm.Usage{TotalTokens: 6000}},
		llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("b", 48000), Usage: llm.Usage{TotalTokens: 12000}},
		llm.Message{Role: llm.RoleUser, Content: strings.Repeat("c", 48000), Usage: llm.Usage{TotalTokens: 18000}},
		llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("d", 48000), Usage: llm.Usage{TotalTokens: 22000}},
	))

	engine, err := NewEngine(
		llm.ModelConfig{Name: "fake", BaseURL: server.URL, APIKey: "x", ContextWindow: 40_000},
		sess,
		WithGate(permission.AllowAll{}),
		WithTools([]tools.Tool{bulkyTool()}),
	)
	require.NoError(t, err)

	var lastErr error
	var sawCompact bool
	for ev, err := range engine.Loop(t.Context(), "continue", LoopOpts{}) {
		if err != nil {
			lastErr = err
			break
		}
		if _, ok := ev.(session.CompactionStarted); ok {
			sawCompact = true
		}
	}

	require.NoError(t, lastErr)
	require.True(t, sawCompact, "the second request should have been preceded by a compaction")
	require.Len(t, bodies, 2)
	require.Equal(t, int32(1), compactHits.Load(),
		"the follow-up attempt has nothing left to cut, so it must not summarize again")

	first := sentContent(bodies[0])
	require.Contains(t, first, strings.Repeat("a", 32))
	require.Contains(t, first, strings.Repeat("b", 32))

	second := sentContent(bodies[1])
	require.NotContains(t, second, strings.Repeat("a", 32), "the summarized prefix must be gone")
	require.NotContains(t, second, strings.Repeat("b", 32))
	require.Contains(t, second, strings.Repeat("c", 32))
	require.Contains(t, second, strings.Repeat("d", 32))
}
