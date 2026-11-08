package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
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

func countingTool(runs *atomic.Int32) tooldef.Tool {
	return tooldef.Tool{
		Definition: llm.ToolDefinition{
			Name:        "count",
			Description: "count tool executions",
			Params:      &llm.FunctionParameters{Type: "object"},
		},
		Run: func(context.Context, json.RawMessage) (tooldef.Result, error) {
			runs.Add(1)
			return tooldef.Result{Content: "ok"}, nil
		},
	}
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

// The summary request is capped by the model's own output limit, clamped to
// the window, so a reasoning model has room for both thinking and the summary.
func TestRunCompactUsesModelOutputLimit(t *testing.T) {
	for _, tc := range []struct {
		name          string
		maxTokens     int
		contextWindow int
		wantAtMost    float64
		wantExact     bool
	}{
		{name: "model limit reaches the request", maxTokens: 128_000, contextWindow: 1_000_000, wantAtMost: 128_000, wantExact: true},
		{name: "small window clamps it", maxTokens: 128_000, contextWindow: 60_000, wantAtMost: 60_000 - 4096},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var compactMaxTokens float64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				compactMaxTokens, _ = body["max_tokens"].(float64)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []any{map[string]any{
						"message":       map[string]any{"role": "assistant", "content": "prior work summarized"},
						"finish_reason": "stop",
					}},
				})
			}))
			defer server.Close()

			sess, err := NewSession(WithCwd(t.TempDir()))
			require.NoError(t, err)
			require.NoError(t, sess.Append(
				llm.Message{Role: llm.RoleUser, Content: compactionSeed},
				llm.Message{Role: llm.RoleAssistant, Content: compactionSeed},
				llm.Message{Role: llm.RoleUser, Content: compactionSeed},
			))
			engine, err := NewEngine(
				llm.ModelConfig{
					Name: "fake", BaseURL: server.URL, APIKey: "x",
					ContextWindow: tc.contextWindow, MaxTokens: tc.maxTokens,
				},
				sess,
				WithGate(permission.AllowAll{}),
				WithTools([]tooldef.Tool{}),
			)
			require.NoError(t, err)

			did, err := engine.runCompact(t.Context(), func(session.Event, error) bool { return true }, 0, true)
			require.NoError(t, err)
			require.True(t, did)
			if tc.wantExact {
				require.InDelta(t, tc.wantAtMost, compactMaxTokens, 0.5)
				return
			}
			require.Positive(t, compactMaxTokens)
			require.Less(t, compactMaxTokens, tc.wantAtMost, "the prompt's share of the window is taken off")
		})
	}
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
		WithTools([]tooldef.Tool{}),
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
		WithTools([]tooldef.Tool{}),
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
		WithTools([]tooldef.Tool{}),
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
	require.True(t, llm.IsContextOverflow(lastErr), "the original overflow still classifies as overflow")
	require.ErrorContains(t, lastErr, "token cap", "the error says why recovery failed")
	require.Equal(t, int32(1), streamHits.Load(), "no retry on a failed compact")
	// No preset and no max_tokens: the summary uses the default output cap.
	require.InDelta(t, 16384, compactMaxTokens, 0.5)
	for _, entry := range sess.PathEntries() {
		require.NotEqual(t, session.EntryCompaction, entry.GetType(), "no checkpoint was persisted")
	}
}

// A blank summary is as useless as a capped one. It must not become the
// session checkpoint: the loop surfaces the original overflow and the history
// stays where it was.
func TestLoopOverflowRejectsBlankSummary(t *testing.T) {
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
				"message":       map[string]any{"role": "assistant", "content": "  \n"},
				"finish_reason": "stop",
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
		WithTools([]tooldef.Tool{}),
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

	require.True(t, sawFailure, "a blank summary must report a failed compaction")
	require.Error(t, lastErr)
	require.True(t, llm.IsContextOverflow(lastErr), "the original overflow still classifies as overflow")
	require.ErrorContains(t, lastErr, "empty summary", "the error says why recovery failed")
	require.Equal(t, int32(1), streamHits.Load(), "no retry on a failed compact")
	require.InDelta(t, 16384, compactMaxTokens, 0.5)
	for _, entry := range sess.PathEntries() {
		require.NotEqual(t, session.EntryCompaction, entry.GetType(), "no checkpoint was persisted")
	}
}

// stubChatClient supplies deterministic streams and records the context sent to
// each request. Compact is unused by these loop tests.
type stubChatClient struct {
	streams []func(context.Context, []llm.Message) iter.Seq2[llm.StreamEvent, error]
	calls   [][]llm.Message
}

func (c *stubChatClient) Stream(ctx context.Context, messages []llm.Message) iter.Seq2[llm.StreamEvent, error] {
	c.calls = append(c.calls, append([]llm.Message(nil), messages...))
	stream := c.streams[0]
	c.streams = c.streams[1:]
	return stream(ctx, messages)
}

func (*stubChatClient) Compact(context.Context, llm.CompactRequest) (llm.CompactResult, error) {
	return llm.CompactResult{}, errors.New("stubChatClient: Compact is not used")
}

func stubMessageStream(message llm.Message) func(context.Context, []llm.Message) iter.Seq2[llm.StreamEvent, error] {
	return func(_ context.Context, _ []llm.Message) iter.Seq2[llm.StreamEvent, error] {
		return func(yield func(llm.StreamEvent, error) bool) {
			yield(llm.StreamEvent{Type: llm.StreamEventTypeDone, Final: &message}, nil)
		}
	}
}

func stubErrorStream(err error) func(context.Context, []llm.Message) iter.Seq2[llm.StreamEvent, error] {
	return func(_ context.Context, _ []llm.Message) iter.Seq2[llm.StreamEvent, error] {
		return func(yield func(llm.StreamEvent, error) bool) {
			yield(llm.StreamEvent{}, err)
		}
	}
}

func runLoop(t *testing.T, engine *Engine, ctx context.Context, prompt string, opts LoopOpts) error {
	t.Helper()
	for _, err := range engine.Loop(ctx, prompt, opts) {
		if err != nil {
			return err
		}
	}
	return nil
}

func TestLoopResumeDoesNotAppendUserMessage(t *testing.T) {
	sess, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	client := &stubChatClient{streams: []func(context.Context, []llm.Message) iter.Seq2[llm.StreamEvent, error]{
		stubErrorStream(errors.New("first attempt failed")),
		stubMessageStream(llm.Message{Role: llm.RoleAssistant, Content: "recovered"}),
	}}
	engine, err := NewEngine(llm.ModelConfig{Name: "stub"}, sess, WithTools([]tooldef.Tool{}))
	require.NoError(t, err)
	engine.client = client

	var appended int
	require.ErrorContains(t, runLoop(t, engine, t.Context(), "try this", LoopOpts{
		OnPromptAppended: func() { appended++ },
	}), "first attempt failed")
	require.Equal(t, 1, appended)
	require.NoError(t, runLoop(t, engine, t.Context(), "try this", LoopOpts{
		Resume:           true,
		OnPromptAppended: func() { appended++ },
	}))
	require.Equal(t, 1, appended, "resume must not call the append callback")

	messages := sess.BuildContext()
	var users int
	for _, message := range messages {
		if message.Role == llm.RoleUser {
			users++
		}
	}
	require.Equal(t, 1, users)
	require.Equal(t, "try this", messages[0].Content)
	require.Len(t, client.calls, 2)
	require.Equal(t, messages[:1], client.calls[1])
}

func TestLoopResumeReusesCompletedToolResult(t *testing.T) {
	sess, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	var runs atomic.Int32
	client := &stubChatClient{streams: []func(context.Context, []llm.Message) iter.Seq2[llm.StreamEvent, error]{
		stubMessageStream(llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{ID: "call_1", Type: "function", Function: llm.Function{Name: "count", Arguments: `{}`}},
			},
		}),
		stubErrorStream(errors.New("second attempt failed")),
		stubMessageStream(llm.Message{Role: llm.RoleAssistant, Content: "recovered"}),
	}}
	engine, err := NewEngine(
		llm.ModelConfig{Name: "stub"}, sess,
		WithGate(permission.AllowAll{}),
		WithTools([]tooldef.Tool{countingTool(&runs)}),
	)
	require.NoError(t, err)
	engine.client = client

	require.ErrorContains(t, runLoop(t, engine, t.Context(), "run it", LoopOpts{}), "second attempt failed")
	require.NoError(t, runLoop(t, engine, t.Context(), "run it", LoopOpts{Resume: true}))
	require.Equal(t, int32(1), runs.Load(), "resume must not replay the completed tool call")

	messages := sess.BuildContext()
	require.Len(t, messages, 4)
	require.Equal(t, llm.RoleUser, messages[0].Role)
	require.Equal(t, llm.RoleAssistant, messages[1].Role)
	require.Equal(t, llm.RoleTool, messages[2].Role)
	require.Equal(t, "ok", messages[2].Content)
	require.Equal(t, llm.RoleAssistant, messages[3].Role)
	require.Len(t, client.calls, 3)
	require.Equal(t, messages[:3], client.calls[2])
}

func TestLoopCanceledBeforeStartDoesNotAppendOrCallback(t *testing.T) {
	sess, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	client := &stubChatClient{streams: []func(context.Context, []llm.Message) iter.Seq2[llm.StreamEvent, error]{
		stubMessageStream(llm.Message{Role: llm.RoleAssistant, Content: "unreachable"}),
	}}
	engine, err := NewEngine(llm.ModelConfig{Name: "stub"}, sess, WithTools([]tooldef.Tool{}))
	require.NoError(t, err)
	engine.client = client
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	called := false
	require.NoError(t, runLoop(t, engine, ctx, "do not start", LoopOpts{
		OnPromptAppended: func() { called = true },
	}))
	require.False(t, called)
	require.Empty(t, sess.BuildContext())
	require.Empty(t, client.calls)
}

func TestLoopStopsAfterConsumerCancelsDuringPartialStreamError(t *testing.T) {
	sess, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := &stubChatClient{streams: []func(context.Context, []llm.Message) iter.Seq2[llm.StreamEvent, error]{
		func(_ context.Context, _ []llm.Message) iter.Seq2[llm.StreamEvent, error] {
			return func(yield func(llm.StreamEvent, error) bool) {
				if !yield(llm.StreamEvent{
					Type:  llm.StreamEventTypeDelta,
					Delta: llm.StreamDelta{ReasoningContent: "thinking"},
				}, nil) {
					return
				}
				cancel()
				yield(llm.StreamEvent{}, errors.New("provider stopped"))
			}
		},
	}}
	engine, err := NewEngine(llm.ModelConfig{Name: "stub"}, sess, WithTools([]tooldef.Tool{}))
	require.NoError(t, err)
	engine.client = client

	var events int
	engine.Loop(ctx, "stop this", LoopOpts{})(func(event session.Event, err error) bool {
		events++
		assert.NoError(t, err)
		if ctx.Err() != nil {
			update, ok := event.(session.AssistantMessageUpdate)
			assert.True(t, ok)
			assert.Equal(t, session.StateError, update.Message.State)
			return false
		}
		return true
	})
	require.Equal(t, 2, events, "consumer must not be called again after returning false")
}

// fakeCompactClient drives runCompact without a server; Stream is unused.
type fakeCompactClient struct {
	compact func(ctx context.Context) error
}

func (fakeCompactClient) Stream(context.Context, []llm.Message) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.StreamEvent{}, errors.New("fakeCompactClient: Stream is not used here"))
	}
}

func (c fakeCompactClient) Compact(ctx context.Context, _ llm.CompactRequest) (llm.CompactResult, error) {
	return llm.CompactResult{Text: "summary"}, c.compact(ctx)
}

// runCompactWith force-compacts a seeded session whose summary call runs
// during, and reports whether a Failed event was emitted.
func runCompactWith(
	t *testing.T, ctx context.Context, during func(sess *Session),
) (sess *Session, did, failed bool, err error) {
	t.Helper()
	sess, err = NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	require.NoError(t, sess.Append(
		llm.Message{Role: llm.RoleUser, Content: compactionSeed},
		llm.Message{Role: llm.RoleAssistant, Content: compactionSeed},
		llm.Message{Role: llm.RoleUser, Content: compactionSeed},
	))
	engine, err := NewEngine(
		llm.ModelConfig{Name: "fake", BaseURL: "http://unused.test", ContextWindow: 200_000},
		sess,
		WithGate(permission.AllowAll{}),
		WithTools([]tooldef.Tool{}),
	)
	require.NoError(t, err)
	engine.client = fakeCompactClient{compact: func(context.Context) error {
		during(sess)
		return nil
	}}
	did, err = engine.runCompact(ctx, func(ev session.Event, _ error) bool {
		if c, ok := ev.(session.CompactionComplete); ok && c.Failed {
			failed = true
		}
		return true
	}, 0, true)
	return sess, did, failed, err
}

func requireNoCompactionEntry(t *testing.T, sess *Session) {
	t.Helper()
	for _, entry := range sess.PathEntries() {
		require.NotEqual(t, session.EntryCompaction, entry.GetType(), "no checkpoint was persisted")
	}
}

func TestRunCompactDropsSummaryAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	sess, did, failed, err := runCompactWith(t, ctx, func(*Session) { cancel() })
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, did)
	require.True(t, failed)
	requireNoCompactionEntry(t, sess)
}

func TestRunCompactDropsSummaryWhenSessionAdvanced(t *testing.T) {
	sess, did, failed, err := runCompactWith(t, t.Context(), func(sess *Session) {
		require.NoError(t, sess.Append(llm.Message{Role: llm.RoleUser, Content: "raced"}))
	})
	require.ErrorContains(t, err, "session advanced")
	require.False(t, did)
	require.True(t, failed)
	requireNoCompactionEntry(t, sess)
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
		WithTools([]tooldef.Tool{}),
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
func bulkyTool() tooldef.Tool {
	return tooldef.Tool{
		Definition: llm.ToolDefinition{
			Name:        "count",
			Description: "answer with a large payload",
			Params:      &llm.FunctionParameters{Type: "object"},
		},
		Run: func(context.Context, json.RawMessage) (tooldef.Result, error) {
			return tooldef.Result{Content: strings.Repeat("y", 12000)}, nil
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
		WithTools([]tooldef.Tool{bulkyTool()}),
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
