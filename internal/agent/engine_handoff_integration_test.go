package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/session"
)

func TestParentHandoffThroughSpawnAndWait(t *testing.T) {
	for _, tc := range []struct {
		role       string
		mode       string
		background bool
	}{
		{"explore", "", true},
		{"worker", "", true},
		{"review", "", false},
		{"worker", "clean", false},
		{"review", "handoff", true},
	} {
		t.Run(tc.role+"/"+tc.mode, func(t *testing.T) {
			requests := make(chan string, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages []struct{ Role, Content string }
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				var texts []string
				for _, message := range body.Messages {
					if message.Role == "user" {
						texts = append(texts, message.Content)
					}
				}
				requests <- strings.Join(texts, "\n")
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(
					w,
					"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"child-summary\"}}]}\n\ndata: [DONE]\n\n",
				)
			}))
			defer srv.Close()
			model := llm.ModelConfig{Name: "fake", BaseURL: srv.URL, APIKey: "x"}
			mgr, err := job.New(job.Options{Root: t.TempDir(), Runner: EngineRunner{Model: model}})
			require.NoError(t, err)
			t.Cleanup(func() { _ = mgr.Close(context.WithoutCancel(t.Context())) })
			parent, err := NewSession(WithCwd(t.TempDir()), WithPersist(true), WithSessionDir(t.TempDir()))
			require.NoError(t, err)
			require.NoError(t, parent.AddUser("obsolete-history"))
			require.NoError(t, parent.Append(llm.Message{
				Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "kept-read"}},
				ProviderState: &llm.ProviderState{Model: "private-provider-state"},
			}))
			keptID := parent.LastID()
			require.NoError(
				t,
				parent.Append(
					llm.Message{Role: llm.RoleTool, ToolCallID: "kept-read", Content: "retained-tool-result"},
				),
			)
			require.NoError(
				t,
				parent.AppendCompaction(session.Compaction{Summary: "approved-design", FirstKeptEntryID: keptID}),
			)
			parent, err = NewSession(WithResumePath(parent.File()))
			require.NoError(t, err)
			require.NoError(t, parent.Append(llm.Message{
				Role:      llm.RoleAssistant,
				Content:   "historical-incomplete",
				ToolCalls: []llm.ToolCall{{ID: "interrupted"}},
			}))
			require.NoError(t, parent.AddUser("current-constraint"))
			engine, err := NewEngine(model, parent, WithJobs(mgr), WithGate(permission.AllowAll{}))
			require.NoError(t, err)
			args, err := json.Marshal(
				map[string]string{"prompt": "assigned-work", "role": tc.role, "context_mode": tc.mode},
			)
			require.NoError(t, err)
			spawn := llm.ToolCall{ID: "spawn", Function: llm.Function{Name: "agent_spawn", Arguments: string(args)}}
			require.NoError(
				t,
				parent.Append(
					llm.Message{Role: llm.RoleAssistant, Content: "unfinished-batch", ToolCalls: []llm.ToolCall{spawn}},
				),
			)
			before := parent.BuildContext()
			results, _, _ := engine.executor.Run(
				t.Context(),
				[]llm.ToolCall{spawn},
				func(session.ToolData) bool { return true },
			)
			require.Len(t, results, 1)
			var info struct {
				JobID string `json:"job_id"`
			}
			require.NoError(t, json.Unmarshal([]byte(results[0].Content), &info))
			require.NotEmpty(t, info.JobID)
			waitArgs, err := json.Marshal(map[string]string{"job_id": info.JobID})
			require.NoError(t, err)
			waited, _, _ := engine.executor.Run(
				t.Context(),
				[]llm.ToolCall{{ID: "wait", Function: llm.Function{Name: "agent_wait", Arguments: string(waitArgs)}}},
				func(session.ToolData) bool { return true },
			)
			require.Len(t, waited, 1)
			var result struct{ Status, Summary string }
			require.NoError(t, json.Unmarshal([]byte(waited[0].Content), &result))
			require.Equal(t, "completed", result.Status)
			assert.Equal(t, "child-summary", result.Summary)
			assert.NotContains(t, waited[0].Content, "approved-design")
			assert.Equal(t, before, parent.BuildContext(), "child must not modify parent history")
			select {
			case prompt := <-requests:
				assert.Contains(t, prompt, "assigned-work")
				assert.NotContains(t, prompt, "obsolete-history")
				assert.NotContains(t, prompt, "unfinished-batch")
				assert.NotContains(t, prompt, "historical-incomplete")
				assert.NotContains(t, prompt, "private-provider-state")
				if tc.background {
					assert.Equal(t, 1, strings.Count(prompt, "approved-design"))
					assert.Contains(t, prompt, "current-constraint")
					assert.Equal(t, 1, strings.Count(prompt, "retained-tool-result"))
				} else {
					assert.NotContains(t, prompt, "approved-design")
					assert.NotContains(t, prompt, "current-constraint")
				}
			default:
				require.FailNow(t, "child did not issue a model request")
			}
		})
	}
}
