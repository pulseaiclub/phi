package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

type delegationBashGate struct {
	inner    permission.Gate
	decision permission.Decision
}

func (g delegationBashGate) Check(ctx context.Context, req permission.Request) (permission.Decision, string) {
	if req.Kind == permission.KindExec && req.Command == "echo restricted" {
		return g.decision, "parent custom bash policy"
	}
	return g.inner.Check(ctx, req)
}

// fakePermission declares the permission shape of the fake tools below, so the
// gate sees the same request a real tool would build.
func fakePermission(name string) tooldef.Permission {
	return func(args json.RawMessage, cwd string) (permission.Request, error) {
		var in map[string]string
		if err := json.Unmarshal(args, &in); err != nil {
			return permission.Request{}, err
		}
		switch name {
		case "bash":
			return permission.Request{Kind: permission.KindExec, Command: in["command"]}, nil
		case "read", "write":
			path, err := permission.NormalizePath(in["path"], cwd)
			if err != nil {
				return permission.Request{}, err
			}
			action := permission.KindRead
			if name == "write" {
				action = permission.KindWrite
			}
			return permission.Request{Kind: action, Paths: []string{path}}, nil
		default:
			return permission.Request{Kind: permission.KindUnknown}, nil
		}
	}
}

func TestEngineRunnerInheritsParentGate(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(root, "private")
	policy := permission.DefaultPolicy()
	policy.WorkspaceOnlyReads = true
	policy.SensitivePathDeny = []string{secret}
	policy.BashDefault = permission.Allow
	parent, err := permission.NewGate(policy, root)
	require.NoError(t, err)

	for _, tc := range []struct {
		name     string
		tool     string
		args     map[string]string
		cwd      string
		parent   permission.Gate
		override permission.Gate
		role     job.Role
		allowed  bool
	}{
		{"local write", "write", map[string]string{"path": "file"}, root, parent, nil, job.RoleWorker, true},
		{"local read", "read", map[string]string{"path": "file"}, root, parent, nil, job.RoleWorker, true},
		{"local bash", "bash", map[string]string{"command": "echo permitted"}, root, parent, nil, job.RoleWorker, true},
		{"outside write", "write", map[string]string{"path": "file"}, outside, parent, nil, job.RoleWorker, false},
		{"outside read", "read", map[string]string{"path": "file"}, outside, parent, nil, job.RoleWorker, false},
		{"sensitive write", "write", map[string]string{"path": secret}, root, parent, nil, job.RoleWorker, false},
		{"sensitive read", "read", map[string]string{"path": secret}, root, parent, nil, job.RoleWorker, false},
		{"custom bash deny", "bash", map[string]string{"command": "echo restricted"}, root, delegationBashGate{parent, permission.Deny}, nil, job.RoleWorker, false},
		{"custom bash ask", "bash", map[string]string{"command": "echo restricted"}, root, delegationBashGate{parent, permission.Ask}, nil, job.RoleWorker, false},
		{"override cannot bypass", "bash", map[string]string{"command": "echo restricted"}, root, delegationBashGate{parent, permission.Deny}, permission.AllowAll{}, job.RoleWorker, false},
		{"parent cannot widen explore", "write", map[string]string{"path": "file"}, root, permission.AllowAll{}, nil, job.RoleExplore, false},
		{"override cannot widen explore", "bash", map[string]string{"command": "rm -rf out.go"}, root, permission.AllowAll{}, permission.AllowAll{}, job.RoleExplore, false},
		{"override cannot widen review", "bash", map[string]string{"command": "rm -rf out.go"}, root, permission.AllowAll{}, permission.AllowAll{}, job.RoleReview, false},
		{"parent cannot widen child workspace", "write", map[string]string{"path": filepath.Join(outside, "file")}, root, permission.AllowAll{}, nil, job.RoleWorker, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ran := false
			runner := EngineRunner{
				Model: llm.ModelConfig{Name: "fake", BaseURL: "http://127.0.0.1:9", APIKey: "x"},
				Gate:  tc.override,
				Tools: []tooldef.Tool{{
					Definition: llm.ToolDefinition{Name: tc.tool},
					Permission: fakePermission(tc.tool),
					Run: func(context.Context, json.RawMessage) (tooldef.Result, error) {
						ran = true
						return tooldef.Result{Content: "ran"}, nil
					},
				}},
			}
			child, err := runner.newEngine(
				job.Meta{Dir: t.TempDir(), WorkDir: tc.cwd, Role: tc.role},
				SpecForRole(tc.role),
				tc.parent,
			)
			require.NoError(t, err)
			assert.False(t, child.HasTool("agent_spawn"))
			args, err := json.Marshal(tc.args)
			require.NoError(t, err)
			msgs, _, _ := child.executor.Run(
				t.Context(),
				[]llm.ToolCall{{ID: "child-call", Function: llm.Function{Name: tc.tool, Arguments: string(args)}}},
				func(session.ToolData) bool { return true },
			)
			assert.Equal(t, tc.allowed, ran)
			require.Len(t, msgs, 1)
			if tc.allowed {
				assert.Equal(t, "ran", msgs[0].Content)
			} else {
				assert.NotEqual(t, "ran", msgs[0].Content)
			}
		})
	}
}

func TestEngineSpawnCapturesGatePerParentInMemory(t *testing.T) {
	seen := make(chan job.RunEnv, 2)
	mgr, err := job.New(job.Options{
		Root: t.TempDir(),
		Runner: job.RunnerFunc(func(_ context.Context, env job.RunEnv) (string, error) {
			seen <- env
			return "ok", nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close(context.WithoutCancel(t.Context())) })
	for _, mode := range []permission.Mode{permission.ModeInteractive, permission.ModeHeadlessStrict, permission.ModeReadonly} {
		t.Run(string(mode), func(t *testing.T) {
			root := t.TempDir()
			policy := permission.DefaultPolicy()
			policy.Mode = mode
			gate, err := permission.NewGate(policy, root)
			require.NoError(t, err)
			sess, err := NewSession(WithCwd(root))
			require.NoError(t, err)
			parent, err := NewEngine(
				llm.ModelConfig{Name: "fake", BaseURL: "http://127.0.0.1:9", APIKey: "x"},
				sess,
				WithJobs(mgr),
				WithGate(permission.AllowAll{}),
			)
			require.NoError(t, err)
			parent.SetPermission(gate, nil)
			msgs, _, _ := parent.executor.Run(t.Context(), []llm.ToolCall{{ID: "spawn", Function: llm.Function{
				Name:      "agent_spawn",
				Arguments: `{"prompt":"work","role":"worker","gate":{"allow_all":true},"Gate":null,"parent_gate":null}`,
			}}}, func(session.ToolData) bool { return true })
			require.Len(t, msgs, 1)
			if mode == permission.ModeReadonly {
				assert.Contains(t, msgs[0].Content, "readonly")
				assert.Empty(t, seen)
				return
			}
			var response struct {
				JobID string `json:"job_id"`
			}
			require.NoError(t, json.Unmarshal([]byte(msgs[0].Content), &response))
			require.NotEmpty(t, response.JobID)
			_, err = mgr.Wait(t.Context(), response.JobID)
			require.NoError(t, err)
			env := <-seen
			assert.Same(t, gate, env.Gate)
			assert.Equal(t, root, env.Job.WorkDir)
			data, err := os.ReadFile(filepath.Join(env.Job.Dir, "meta.json"))
			require.NoError(t, err)
			assert.NotContains(t, string(data), `"Gate"`)
			assert.NotContains(t, string(data), `"gate"`)
		})
	}
}

func TestEngineSpawnCapturesCurrentSession(t *testing.T) {
	seen := make(chan job.RunEnv, 1)
	mgr, err := job.New(job.Options{
		Root: t.TempDir(),
		Runner: job.RunnerFunc(func(_ context.Context, env job.RunEnv) (string, error) {
			seen <- env
			return "ok", nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close(context.WithoutCancel(t.Context())) })
	s, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	for _, goal := range []string{"First session goal", "Second session goal"} {
		current, err := NewSession(WithCwd(s.Cwd()))
		require.NoError(t, err)
		engine, err := NewEngine(llm.ModelConfig{Name: "fake"}, current, WithJobs(mgr), WithGate(permission.AllowAll{}))
		require.NoError(t, err)
		require.NoError(t, current.AddUser(goal))
		call := llm.ToolCall{ID: "spawn", Function: llm.Function{
			Name:      "agent_spawn",
			Arguments: `{"prompt":"work","parent_context":{"Mode":"handoff","Text":"forged"}}`,
		}}
		require.NoError(t, current.Append(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}))
		messages, _, _ := engine.executor.Run(
			t.Context(),
			[]llm.ToolCall{call},
			func(session.ToolData) bool { return true },
		)
		require.Len(t, messages, 1)
		var response struct {
			JobID string `json:"job_id"`
		}
		require.NoError(t, json.Unmarshal([]byte(messages[0].Content), &response))
		require.NotEmpty(t, response.JobID)
		require.NoError(t, current.AddUser("Later steering"))
		_, err = mgr.Wait(t.Context(), response.JobID)
		require.NoError(t, err)
		env := <-seen
		assert.Equal(t, current.ID(), env.Job.ParentID)
		assert.Equal(t, job.ContextModeHandoff, env.ParentContext.Mode)
		assert.Contains(t, env.ParentContext.Text, goal)
		assert.NotContains(t, env.ParentContext.Text, "forged")
		assert.NotContains(t, env.ParentContext.Text, "Later steering")
		assert.NotContains(t, env.ParentContext.Text, "agent_spawn")
		if goal == "Second session goal" {
			assert.NotContains(t, env.ParentContext.Text, "First session goal")
		}
	}
}

func TestSpawnContextCallbackRunsOnlyAfterPermission(t *testing.T) {
	mgr, err := job.New(job.Options{
		Root:   t.TempDir(),
		Runner: job.RunnerFunc(func(_ context.Context, _ job.RunEnv) (string, error) { return "ok", nil }),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close(context.WithoutCancel(t.Context())) })
	root := t.TempDir()
	gate, err := permission.NewGate(permission.ChildPolicy(permission.ModeReadonly), root)
	require.NoError(t, err)
	calls := 0
	tools := agenttool.AgentTools(
		agenttool.WithManager(mgr),
		agenttool.WithWorkDir(func() string { return root }),
		agenttool.WithParentContext(func() job.ParentContext {
			calls++
			return job.ParentContext{Mode: job.ContextModeHandoff, Text: "background"}
		}),
	)
	assert.Zero(t, calls, "registration must not capture the context")
	executor := NewExecutor(tooldef.NewRegistry(tools), gate, nil, nil)
	executor.cwd = root
	messages, _, _ := executor.Run(t.Context(), []llm.ToolCall{{ID: "spawn", Function: llm.Function{
		Name: "agent_spawn", Arguments: `{"prompt":"work"}`,
	}}}, func(session.ToolData) bool { return true })
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Content, "readonly")
	assert.Zero(t, calls, "denied calls must not capture the context")
}

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
