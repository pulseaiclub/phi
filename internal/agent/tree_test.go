package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/llm/anthropic"
	"github.com/pulseaiclub/phi/internal/llm/openai"
	"github.com/pulseaiclub/phi/internal/session"
)

func TestTreeInvalidatesContextAndReeditsUsers(t *testing.T) {
	s, err := NewSession()
	require.NoError(t, err)
	require.NoError(t, s.AddUser("first"))
	u := s.LastID()
	require.NoError(t, s.Append(llm.Message{Role: llm.RoleAssistant, Content: "answer"}))
	a := s.LastID()
	require.NoError(t, s.AddUser("second"))
	u2 := s.LastID()
	require.NoError(t, s.Append(llm.Message{Role: llm.RoleAssistant, Content: "second answer"}))
	assert.Len(t, s.BuildContext(), 4)
	plan, err := s.TreeSnapshot().Plan(u2)
	require.NoError(t, err)
	require.NoError(t, s.Navigate(plan, nil))
	assert.Equal(t, a, s.LastID())
	assert.Equal(t, "second", plan.EditorText)
	assert.Len(t, s.BuildContext(), 2)
	plan, err = s.TreeSnapshot().Plan(u)
	require.NoError(t, err)
	require.NoError(t, s.Navigate(plan, nil))
	assert.Empty(t, s.LastID())
	assert.Empty(t, s.BuildContext())
	require.NoError(t, s.AddUser("fresh root"))
	assert.Len(t, s.BuildContext(), 1)
	assert.Len(t, s.TreeSnapshot().Entries, 5)
}

func TestTreeToolContextIsValidForBothProviders(t *testing.T) {
	for _, resultCount := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(resultCount), func(t *testing.T) {
			s, err := NewSession()
			require.NoError(t, err)
			require.NoError(t, s.AddUser("tools"))
			calls := []llm.ToolCall{
				{ID: "one", Type: "function", Function: llm.Function{Name: "write", Arguments: `{}`}},
				{ID: "two", Type: "function", Function: llm.Function{Name: "read", Arguments: `{}`}},
			}
			require.NoError(t, s.Append(llm.Message{Role: llm.RoleAssistant, ToolCalls: calls}))
			for i := range resultCount {
				require.NoError(
					t,
					s.Append(llm.Message{Role: llm.RoleTool, ToolCallID: calls[i].ID, Content: "actual result"}),
				)
			}
			require.NoError(t, s.AddUser("continue on branch"))
			messages := s.BuildContext()
			require.Len(t, messages, 5)
			assert.Equal(t, "one", messages[2].ToolCallID)
			assert.Equal(t, "two", messages[3].ToolCallID)
			for i := range 2 {
				if i < resultCount {
					assert.Equal(t, "actual result", messages[i+2].Content)
				} else {
					assert.Contains(t, messages[i+2].Content, "not re-executed")
				}
			}
			cfg := llm.ModelConfig{Name: "test", ContextWindow: 10000}
			for _, request := range []any{openai.BuildRequest(cfg, "system", messages, nil), anthropic.BuildRequest(cfg, "system", messages, nil)} {
				body, err := json.Marshal(request)
				require.NoError(t, err)
				assert.Contains(t, string(body), "one")
				assert.Contains(t, string(body), "two")
				assert.Contains(t, string(body), "continue on branch")
			}
		})
	}
}

func TestTreeSummaryPersistsUsageAndFileOperations(t *testing.T) {
	var prompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []llm.Message `json:"messages"`
			Tools    []any         `json:"tools"`
		}
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		assert.Empty(t, request.Tools)
		for _, m := range request.Messages {
			prompt += m.Content
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, sseTextChunk("carry these decisions"))
		_, _ = fmt.Fprint(
			w,
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7,\"total_tokens\":18}}\n\ndata: [DONE]\n\n",
		)
	}))
	defer server.Close()
	s, err := NewSession()
	require.NoError(t, err)
	require.NoError(t, s.AddUser("original question"))
	u := s.LastID()
	require.NoError(
		t,
		s.Append(
			llm.Message{
				Role:    llm.RoleAssistant,
				Content: "decision",
				ToolCalls: []llm.ToolCall{
					{ID: "edit", Function: llm.Function{Name: "write", Arguments: `{"path":"main.go"}`}},
				},
			},
		),
	)
	from := s.LastID()
	engine, err := NewEngine(llm.ModelConfig{Name: "test", BaseURL: server.URL, APIKey: "key"}, s)
	require.NoError(t, err)
	plan, err := engine.NavigateTree(t.Context(), u, TreeOptions{Summarize: true, Instructions: "focus on decisions"})
	require.NoError(t, err)
	assert.Equal(t, "original question", plan.EditorText)
	assert.Contains(t, prompt, "focus on decisions")
	assert.Contains(t, prompt, "original question")
	path := s.PathEntries()
	require.Len(t, path, 1)
	summary := path[0].(session.BranchSummaryEntry)
	assert.Nil(t, summary.ParentID)
	assert.Equal(t, from, summary.FromID)
	assert.Equal(t, []string{"main.go"}, summary.ModifiedFiles)
	assert.Equal(t, 18, summary.Usage.TotalTokens)
	assert.Contains(t, s.BuildContext()[0].Content, "carry these decisions")
}

func TestTreeSummaryFailureAndCancellationKeepPosition(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelFirst), func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) }),
			)
			defer server.Close()
			s, err := NewSession()
			require.NoError(t, err)
			require.NoError(t, s.AddUser("question"))
			u := s.LastID()
			require.NoError(t, s.Append(llm.Message{Role: llm.RoleAssistant, Content: "answer"}))
			before := s.TreeSnapshot()
			engine, err := NewEngine(llm.ModelConfig{Name: "test", BaseURL: server.URL, APIKey: "key"}, s)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if cancelFirst {
				cancel()
			}
			_, err = engine.NavigateTree(ctx, u, TreeOptions{Summarize: true})
			require.Error(t, err)
			assert.Equal(t, before, s.TreeSnapshot())
			assert.Equal(t, "answer", s.BuildContext()[1].Content)
		})
	}
}

func TestTreeContextIncludesSummaryAndCustomHistoryOnly(t *testing.T) {
	s, err := NewSession()
	require.NoError(t, err)
	for _, kind := range []string{"bash", "model", "error", "cancelled", "custom"} {
		require.NoError(t, s.AppendHistory(session.HistoryEntry{Kind: kind, Text: kind + " content"}))
	}
	messages := s.BuildContext()
	require.Len(t, messages, 1)
	assert.Equal(t, "custom content", messages[0].Content)
	plan, err := s.TreeSnapshot().Plan(s.TreeSnapshot().Entries[3].GetID())
	require.NoError(t, err)
	require.NoError(t, s.Navigate(plan, &session.BranchSummary{Summary: "context survived"}))
	assert.Contains(t, s.BuildContext()[0].Content, "context survived")
}
