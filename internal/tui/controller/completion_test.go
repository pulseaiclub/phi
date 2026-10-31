package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/llm/extmodel"
)

func TestGenerateCommitMessageBuiltin(t *testing.T) {
	requests := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&request)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- request
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(
			w,
			"data: {\"choices\":[{\"delta\":{\"content\":\"feat: add completion\\n<phi-commit-end>\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
		)
	}))
	defer server.Close()

	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("project rule"), 0o600))
	// No engine or session is needed to generate a completion.
	ctrl := &EngineController{cwd: cwd, modelCfg: llm.ModelConfig{
		Name: "test-model", APIKey: "test-key", BaseURL: server.URL,
	}}
	for _, name := range []string{"test-model", "switched-model"} {
		ctrl.modelMu.Lock()
		ctrl.modelCfg.Name = name
		if name == "switched-model" {
			ctrl.modelCfg.Preset = "deepseek-flash"
			ctrl.modelCfg.Think = llm.ThinkConfig{Enabled: true, Mode: llm.High}
		}
		ctrl.modelMu.Unlock()
		got, err := ctrl.GenerateCommitMessage(t.Context(), "commit rules", "summarize the staged diff")
		require.NoError(t, err)
		assert.Equal(t, "feat: add completion", got)
		request := <-requests
		assert.Equal(t, name, request["model"])
		assert.Equal(t, true, request["stream"])
		assert.NotContains(t, request, "tools")
		if name == "switched-model" {
			assert.Equal(t, map[string]any{"type": "enabled"}, request["thinking"])
		} else {
			assert.NotContains(t, request, "thinking")
		}
		messages := request["messages"].([]any)
		require.Len(t, messages, 2)
		system := messages[0].(map[string]any)
		assert.Equal(t, "system", system["role"])
		assert.Contains(t, system["content"], "commit rules")
		assert.Contains(t, system["content"], "project rule")
		assert.Equal(t, map[string]any{
			"role": "user", "content": "summarize the staged diff",
		}, messages[1])
	}
}

func TestGenerateCommitMessageProviderErrorAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
	}))
	defer server.Close()
	ctrl := &EngineController{cwd: t.TempDir(), modelCfg: llm.ModelConfig{BaseURL: server.URL}}
	got, err := ctrl.GenerateCommitMessage(t.Context(), "rules", "diff")
	require.ErrorContains(t, err, "invalid credentials")
	assert.Empty(t, got)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, err = ctrl.GenerateCommitMessage(ctx, "rules", "diff")
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, got)
}

func TestGenerateCommitMessageRejectsTruncatedStream(t *testing.T) {
	for _, ending := range []string{"", ",\"finish_reason\":\"length\""} {
		t.Run(ending, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(
					w,
					"data: {\"choices\":[{\"delta\":{\"content\":\"fix: partial subject\"}%s}]}\n\n",
					ending,
				)
			}))
			defer server.Close()
			ctrl := &EngineController{cwd: t.TempDir(), modelCfg: llm.ModelConfig{BaseURL: server.URL}}
			got, err := ctrl.GenerateCommitMessage(t.Context(), "rules", "diff")
			require.ErrorContains(t, err, "missing its end marker")
			assert.Empty(t, got)
		})
	}
}

func TestCompletionClientRoutesCurrentExtensions(t *testing.T) {
	runner := loadResolveModelRunner(t)
	ctrl := &EngineController{modelCfg: llm.ModelConfig{Name: "echo"}}
	ctrl.extRunner.Store(runner)
	assert.IsType(t, &extmodel.Client{}, newCompletionClient(ctrl.modelSnapshot(), ctrl.Extensions(), "rules"))
	ctrl.extRunner.Store(nil)
	assert.IsType(t, &llmclient.Client{}, newCompletionClient(ctrl.modelSnapshot(), ctrl.Extensions(), "rules"))
}

func TestCollectCompletion(t *testing.T) {
	delta := func(text string) llm.StreamEvent {
		return llm.StreamEvent{Type: llm.StreamEventTypeDelta, Delta: llm.StreamDelta{Content: text}}
	}
	done := func(text string) llm.StreamEvent {
		return llm.AssistantDone(text, "", nil, llm.Usage{})
	}
	cases := []struct {
		name   string
		events []llm.StreamEvent
		want   string
		err    string
	}{
		{
			name:   "delta and final",
			events: []llm.StreamEvent{delta("feat: "), delta("add"), done("feat: add")},
			want:   "feat: add",
		},
		{name: "final only", events: []llm.StreamEvent{done("fix: bug")}, want: "fix: bug"},
		{
			name:   "delta only",
			events: []llm.StreamEvent{delta("fix: bug"), {Type: llm.StreamEventTypeDone}},
			want:   "fix: bug",
		},
		{name: "final authoritative", events: []llm.StreamEvent{delta("fix: bu"), done("fix: bug")}, want: "fix: bug"},
		{
			name:   "shorter final",
			events: []llm.StreamEvent{delta("fix: bug and tests"), done("fix: bug")},
			want:   "fix: bug",
		},
		{name: "empty final", events: []llm.StreamEvent{delta("partial"), done("")}, err: "no content"},
		{
			name: "reasoning budget",
			events: []llm.StreamEvent{
				{Delta: llm.StreamDelta{ReasoningContent: strings.Repeat("x", maxCommitReasoningBytes+1)}},
			},
			err: "reasoning budget",
		},
		{name: "empty", events: []llm.StreamEvent{done("")}, err: "no content"},
		{name: "whitespace", events: []llm.StreamEvent{done(" \n")}, err: "no content"},
		{name: "missing terminal", events: []llm.StreamEvent{delta("partial")}, err: "without a final response"},
		{
			name:   "oversize delta",
			events: []llm.StreamEvent{delta(strings.Repeat("x", maxCommitMessageBytes+1))},
			err:    "exceeds",
		},
		{
			name:   "oversize final",
			events: []llm.StreamEvent{done(strings.Repeat("x", maxCommitMessageBytes+1))},
			err:    "exceeds",
		},
		{
			name:   "limit",
			events: []llm.StreamEvent{done(strings.Repeat("x", maxCommitMessageBytes))},
			want:   strings.Repeat("x", maxCommitMessageBytes),
		},
		{
			name:   "tool delta",
			events: []llm.StreamEvent{{Delta: llm.StreamDelta{ToolCalls: []llm.ToolCall{{}}}}},
			err:    "requested a tool",
		},
		{
			name:   "tool final",
			events: []llm.StreamEvent{llm.AssistantDone("text", "", []llm.ToolCall{{}}, llm.Usage{})},
			err:    "requested a tool",
		},
		{
			name:   "error event",
			events: []llm.StreamEvent{{Type: llm.StreamEventTypeError, Err: "provider failed"}},
			err:    "provider failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := collectCompletion(t.Context(), completionEvents(tc.events...))
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCollectCompletionPropagatesErrors(t *testing.T) {
	providerErr := errors.New("provider unavailable")
	_, err := collectCompletion(t.Context(), func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.StreamEvent{}, providerErr)
	})
	require.ErrorIs(t, err, providerErr)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err = collectCompletion(ctx, func(yield func(llm.StreamEvent, error) bool) {
		cancel()
		yield(llm.AssistantDone("partial", "", nil, llm.Usage{}), nil)
	})
	require.ErrorIs(t, err, context.Canceled)
}

func completionEvents(events ...llm.StreamEvent) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}
}
