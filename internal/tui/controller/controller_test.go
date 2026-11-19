package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/agent/prompt"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/llm/openai"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/project/model"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/session/compaction"
	"github.com/pulseaiclub/phi/internal/tools"
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

func TestNewController_ReadyEngine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PHI_MODEL", "test-model")
	t.Setenv("PHI_API_KEY", "test-key")
	t.Setenv("PHI_BASE_URL", "http://127.0.0.1:9")

	cwd := t.TempDir()
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	require.NoError(t, proj.LoadConfig())

	ctrl := newInjectedController(t, proj)
	require.NotNil(t, ctrl)
	require.NotNil(t, ctrl.engine)
	assert.Equal(t, cwd, ctrl.session.Cwd())
	assert.NotEmpty(t, ctrl.SessionDir())
	assert.Same(t, proj, ctrl.proj)
}

func TestRedrawRelay_BindAfterBus(t *testing.T) {
	relay := NewRedrawRelay()
	bus := NewBus(relay.Fire)
	var n int
	relay.Bind(func() { n++ })
	bus.Publish(SubmitMsg{Text: "y"})
	assert.GreaterOrEqual(t, n, 1)

	// Drain so the next Publish can re-arm wake + Fire.
	_ = bus.Drain()
	bus.Publish(SubmitMsg{Text: "z"})
	assert.GreaterOrEqual(t, n, 2)
}

func TestUnsupportedThinkingChangeKeepsSessionConfig(t *testing.T) {
	ctrl := &EngineController{modelCfg: llm.ModelConfig{
		Name: "group/auto-glm-5-3-flash", Think: llm.ThinkConfig{Enabled: true, Mode: llm.Max},
	}}
	require.ErrorContains(t, ctrl.SetThinkLevel(llm.Medium), "use low, high, max")
	assert.Equal(t, llm.Max, ctrl.ThinkLevel())
	assert.Equal(t, []llm.ThinkMode{llm.Low, llm.High, llm.Max}, ctrl.ThinkingModes())
}

func TestFinishLoopClassifiesCancellationAndProviderFailure(t *testing.T) {
	tests := []struct {
		name      string
		ctx       context.Context
		err       error
		wantText  string
		cancelled bool
	}{
		{
			name:      "wrapped cancellation",
			ctx:       t.Context(),
			err:       fmt.Errorf("request stopped: %w", context.Canceled),
			cancelled: true,
		},
		{
			name:      "cancelled context",
			ctx:       cancelledContext(t),
			err:       context.Canceled,
			cancelled: true,
		},
		{
			name:     "provider deadline",
			ctx:      t.Context(),
			err:      fmt.Errorf("provider request: %w", context.DeadlineExceeded),
			wantText: "provider request: context deadline exceeded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus := NewBus(nil)
			ctrl := &EngineController{bus: bus, streamGen: 7}

			ctrl.finishLoop(tt.ctx, 7, tt.err)

			batch := bus.Drain()
			require.Len(t, batch, 1)
			finished, ok := batch[0].(LoopFinishedMsg)
			require.True(t, ok)
			assert.Equal(t, 7, finished.Gen)
			assert.Equal(t, tt.wantText, finished.ErrorText)
			assert.Equal(t, tt.cancelled, finished.Cancelled)
			assert.Equal(t, tt.wantText != "", retryReady(ctrl))
		})
	}
}

func TestFinishLoopDropsStaleGeneration(t *testing.T) {
	bus := NewBus(nil)
	ctrl := &EngineController{bus: bus, streamGen: 2}

	ctrl.finishLoop(t.Context(), 1, errors.New("stale provider failure"))

	assert.Empty(t, bus.Drain())
	assert.True(t, ctrl.CurrentLoop(2))
	assert.False(t, ctrl.CurrentLoop(1))
}

func TestCancelInvalidatesQueuedLoopResult(t *testing.T) {
	bus := NewBus(nil)
	ctrl := &EngineController{bus: bus, streamGen: 4}

	ctrl.finishLoop(t.Context(), 4, errors.New("provider failure"))
	ctrl.Cancel()

	batch := bus.Drain()
	require.Len(t, batch, 1)
	finished, ok := batch[0].(LoopFinishedMsg)
	require.True(t, ok)
	assert.Equal(t, 4, finished.Gen)
	assert.False(t, ctrl.CurrentLoop(finished.Gen), "Cancel must invalidate a queued completion")
	assert.False(t, retryReady(ctrl))
}

func TestRetryLastPromptResumesWithoutDuplicateUserMessage(t *testing.T) {
	var requests chan struct{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests <- struct{}{}
		http.Error(w, "provider unavailable", http.StatusUnauthorized)
	}))
	defer server.Close()

	ctrl := newThinkingTestController(t, setupThinkingTest(t, llm.OpenAI, server.URL))
	_ = ctrl.bus.Drain()
	requests = make(chan struct{}, 4)

	ctrl.StartPrompt("keep this once", []string{"review-skill"}, nil)
	first := waitLoopFinished(t, ctrl.bus, 1)
	require.NotEmpty(t, first.ErrorText)
	assert.False(t, first.Cancelled)
	assert.True(t, retryReady(ctrl))
	users := userMessages(ctrl)
	require.Len(t, users, 1)
	assert.Contains(t, users[0].Content, "follow them: review-skill.")
	assert.Contains(t, users[0].Content, "\n\nkeep this once")

	require.True(t, ctrl.RetryLastPrompt())
	assert.False(t, retryReady(ctrl), "starting a retry must consume retryReady")
	second := waitLoopFinished(t, ctrl.bus, 2)
	require.NotEmpty(t, second.ErrorText)
	assert.False(t, second.Cancelled)
	assert.Len(t, userMessages(ctrl), 1, "a resumed retry must not append the prompt again")
	assert.True(t, retryReady(ctrl))

	ctrl.Cancel()
	assert.False(t, retryReady(ctrl))
	assert.False(t, ctrl.RetryLastPrompt(), "a cancelled retry must not be retriable")
}

func TestCancelInFlightPromptPublishesSilentCompletion(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCancelled := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(requestStarted) })
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		close(requestCancelled)
	}))
	defer server.Close()

	ctrl := newThinkingTestController(t, setupThinkingTest(t, llm.OpenAI, server.URL))
	_ = ctrl.bus.Drain()
	ctrl.StartPrompt("cancel this", nil, nil)

	select {
	case <-requestStarted:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "provider request did not start")
	}

	ctrl.Cancel()
	select {
	case <-requestCancelled:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "provider request was not cancelled")
	}
	assert.False(t, ctrl.CurrentLoop(1), "manual cancellation must invalidate the request generation")
	assert.False(t, retryReady(ctrl))
	assert.False(t, ctrl.RetryLastPrompt(), "a manually cancelled request must not be retriable")
	for _, msg := range ctrl.bus.Drain() {
		finished, ok := msg.(LoopFinishedMsg)
		if ok {
			assert.NotEqual(t, 1, finished.Gen, "cancelled request must not publish a current completion")
		}
	}
}

func cancelledContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx
}

func waitLoopFinished(t *testing.T, bus *Bus, gen int) LoopFinishedMsg {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-bus.Chan():
			for _, msg := range bus.Drain() {
				finished, ok := msg.(LoopFinishedMsg)
				if ok && finished.Gen == gen {
					return finished
				}
			}
		case <-timer.C:
			require.FailNowf(t, "loop completion timeout", "timed out waiting for loop %d completion", gen)
		}
	}
}

func retryReady(ctrl *EngineController) bool {
	ctrl.streamMu.Lock()
	defer ctrl.streamMu.Unlock()
	return ctrl.retryReady
}

func userMessages(ctrl *EngineController) []llm.Message {
	var users []llm.Message
	for _, entry := range ctrl.session.PathEntries() {
		message, ok := entry.(session.SessionMessageEntry)
		if ok && message.Message.Role == llm.RoleUser {
			users = append(users, message.Message)
		}
	}
	return users
}

// pathIDs names the entries of the active branch, so a fork can be compared
// with its source without depending on in-memory timestamps.
func pathIDs(s *agent.Session) []string {
	entries := s.PathEntries()
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.GetID())
	}
	return ids
}

// forkTestController seeds one user/assistant round so the session file exists
// and the transcript has something to copy.
func forkTestController(t *testing.T) *EngineController {
	t.Helper()
	ctrl := newThinkingTestController(t, setupThinkingTest(t, llm.OpenAI, "http://127.0.0.1:9"))
	require.NoError(t, ctrl.session.Append(
		llm.Message{Role: llm.RoleUser, Content: "hello"},
		llm.Message{Role: llm.RoleAssistant, Content: "hi"},
	))
	return ctrl
}

func TestForkSessionSwitchesToDistinctSessionWithSameContext(t *testing.T) {
	ctrl := forkTestController(t)
	srcID, srcFile := ctrl.SessionID(), ctrl.session.File()
	srcPath := pathIDs(ctrl.session)
	srcBytes, err := os.ReadFile(srcFile)
	require.NoError(t, err)

	newID, err := ctrl.ForkSession()
	require.NoError(t, err)

	assert.NotEqual(t, srcID, newID)
	assert.Equal(t, newID, ctrl.SessionID())
	assert.NotEqual(t, srcFile, ctrl.session.File())
	assert.Equal(t, srcPath, pathIDs(ctrl.session), "the fork starts on the source's branch")
	assert.Equal(t, "primary", ctrl.ModelName(), "the fork keeps the active model")
	assert.NotEmpty(t, ctrl.ReplaySnapshot().Messages, "the transcript survives the fork")

	after, err := os.ReadFile(srcFile)
	require.NoError(t, err)
	assert.Equal(t, srcBytes, after, "forking must not touch the source file")
}

func TestForkSessionPersistsForkAndParent(t *testing.T) {
	ctrl := forkTestController(t)
	srcID := ctrl.SessionID()

	newID, err := ctrl.ForkSession()
	require.NoError(t, err)

	loaded, err := session.OpenSession(ctrl.session.File())
	require.NoError(t, err)
	assert.Equal(t, newID, loaded.ID())
	assert.Equal(t, pathIDs(ctrl.session), pathIDsOf(loaded))
	assert.Equal(t, srcID, forkHeader(t, ctrl.session.File()).ParentSession, "the fork records its source")

	list, err := session.ListSessions(ctrl.SessionDir())
	require.NoError(t, err)
	assert.Len(t, ids(list), 2, "both sessions stay available")
	assert.Contains(t, ids(list), srcID)
	assert.Contains(t, ids(list), newID)
}

func TestForkSessionLeavesSourceResumable(t *testing.T) {
	ctrl := forkTestController(t)
	srcID := ctrl.SessionID()
	srcPath := pathIDs(ctrl.session)

	newID, err := ctrl.ForkSession()
	require.NoError(t, err)
	require.Equal(t, newID, ctrl.SessionID())

	_, err = ctrl.Resume(srcID)
	require.NoError(t, err)
	assert.Equal(t, srcID, ctrl.SessionID())
	assert.Equal(t, srcPath, pathIDs(ctrl.session))

	_, err = ctrl.Resume(newID)
	require.NoError(t, err)
	assert.Equal(t, srcPath, pathIDs(ctrl.session), "the fork reopens with the same branch")
}

func TestForkSessionFailureKeepsSourceActive(t *testing.T) {
	ctrl := forkTestController(t)
	srcID := ctrl.SessionID()
	path := pathIDs(ctrl.session)

	// A file where the session directory belongs makes the fork write fail; it
	// is the only failure this package can force without a fake session store.
	dir := ctrl.SessionDir()
	require.NoError(t, os.RemoveAll(dir))
	require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0o600))

	id, err := ctrl.ForkSession()
	require.Error(t, err)
	assert.Empty(t, id)
	assert.Equal(t, srcID, ctrl.SessionID(), "a failed fork must not switch sessions")
	assert.Equal(t, path, pathIDs(ctrl.session), "the source context is untouched")
	assert.NotEmpty(t, ctrl.ReplaySnapshot().Messages)
}

func TestForkSessionOfEmptySession(t *testing.T) {
	ctrl := newThinkingTestController(t, setupThinkingTest(t, llm.OpenAI, "http://127.0.0.1:9"))
	require.Empty(t, ctrl.ReplaySnapshot().Messages)

	newID, err := ctrl.ForkSession()
	require.NoError(t, err)
	assert.Equal(t, newID, ctrl.SessionID())
	assert.Empty(t, ctrl.ReplaySnapshot().Messages, "an empty session forks empty")
	assert.Equal(t, ctrl.proj.Root(), ctrl.session.Cwd())
}

// forkHeader decodes the first line of a session file, which is where a fork
// records the session it came from.
func forkHeader(t *testing.T, path string) session.SessionHeader {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	line, _, _ := bytes.Cut(data, []byte("\n"))
	var header session.SessionHeader
	require.NoError(t, json.Unmarshal(line, &header))
	return header
}

func pathIDsOf(m *session.Manager) []string {
	entries := m.BuildContext()
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.GetID())
	}
	return ids
}

func ids(list []session.SessionMeta) []string {
	out := make([]string, 0, len(list))
	for _, meta := range list {
		out = append(out, meta.ID)
	}
	return out
}

func captureModelRequests(t *testing.T) (string, func(*agent.Engine) openai.Request) {
	t.Helper()
	requests := make(chan openai.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openai.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- req
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(
			w,
			"data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
		)
	}))
	t.Cleanup(server.Close)
	return server.URL, func(engine *agent.Engine) openai.Request {
		t.Helper()
		for _, err := range engine.Loop(t.Context(), "hello", agent.LoopOpts{}) {
			require.NoError(t, err)
		}
		require.Len(t, requests, 1)
		return <-requests
	}
}

func requestToolNames(req openai.Request) []string {
	var names []string
	for _, tool := range req.Tools {
		names = append(names, tool.Function.Name)
	}
	return names
}

func TestConfiguredMCPUsesBuiltinTools(t *testing.T) {
	url, request := captureModelRequests(t)
	cwd := setupThinkingTest(t, llm.OpenAI, url)
	t.Setenv("PHI_MCP", "on")
	require.NoError(t, mcp.SaveUser(map[string]mcp.ServerConfig{"fixture-catalog": {Command: []string{"true"}}}))
	ctrl := newThinkingTestController(t, cwd)
	req := request(ctrl.engine)
	require.NotEmpty(t, req.Messages)
	assert.Equal(t, llm.RoleSystem, req.Messages[0].Role)
	assert.Contains(t, req.Messages[0].Content, "- fixture-catalog")
	names := requestToolNames(req)
	for _, name := range []string{"ls", "read", "write", "bash"} {
		assert.Contains(t, names, name)
	}
	for _, name := range []string{"mcp_list", "mcp_inspect", "mcp_call"} {
		assert.NotContains(t, names, name)
	}
}

func TestModelSwitchRetainsAgentTools(t *testing.T) {
	url, request := captureModelRequests(t)
	ctrl := newThinkingTestController(t, setupThinkingTest(t, llm.OpenAI, url))
	require.NoError(t, ctrl.SetAgentsEnabled(true))
	require.NoError(t, ctrl.SetModel("other-model"))
	req := request(ctrl.engine)
	assert.Equal(t, "other-model", req.Model)
	assert.Contains(t, requestToolNames(req), "agent_spawn")
	require.NoError(t, ctrl.SetAgentsEnabled(false))
	names := requestToolNames(request(ctrl.engine))
	assert.NotContains(t, names, "agent_spawn")
	assert.Contains(t, names, "bash")
}

func TestSelectedBuiltinToolsStillAppendJobTools(t *testing.T) {
	url, request := captureModelRequests(t)
	ctrl := newThinkingTestController(t, setupThinkingTest(t, llm.OpenAI, url))
	base := tools.DefaultTools(nil)[:1]
	jobs := agenttool.BindForSession(ctrl.jobs, ctrl.session.Cwd())
	toolList := append(append([]tooldef.Tool(nil), base...), jobs.Tools()...)
	client := llmclient.NewClient(ctrl.modelCfg, model.HooksForConfig(ctrl.modelCfg), tooldef.Definitions(toolList), "")
	executor := agent.NewExecutor(tooldef.NewRegistry(toolList), ctrl.session.Cwd())
	engine, err := agent.NewEngine(compaction.LimitsForModel(ctrl.modelCfg), ctrl.session, client, executor)
	require.NoError(t, err)
	assert.ElementsMatch(
		t,
		[]string{base[0].Definition.Name, "agent_spawn", "agent_wait"},
		requestToolNames(request(engine)),
	)
}

func TestModelLifecycle(t *testing.T) {
	ctrl := newTestController(t)
	cwd := ctrl.proj.Root()
	flush := func() {
		require.NoError(t, ctrl.session.Append(llm.Message{Role: llm.RoleUser, Content: "hello"}))
		require.NoError(t, ctrl.session.Append(llm.Message{Role: llm.RoleAssistant, Content: "done"}))
	}

	require.NoError(t, ctrl.SetModel("primary"))
	firstID := ctrl.SessionID()
	flush()
	require.NoError(t, ctrl.NewSession())
	secondID := ctrl.SessionID()
	require.NoError(t, ctrl.SetModel("secondary"))
	flush()

	_, err := ctrl.Resume(firstID)
	require.NoError(t, err)
	assert.Equal(t, "primary", ctrl.ModelName())
	_, err = ctrl.Resume(secondID)
	require.NoError(t, err)
	assert.Equal(t, "secondary", ctrl.ModelName())
	require.NoError(t, ctrl.NewSession())
	assert.Equal(t, "secondary", ctrl.ModelName())

	ctrl.Close()
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	restarted := newInjectedController(t, proj)
	assert.Equal(t, "primary", restarted.ModelName(), "startup must use the configured default")
	_, err = restarted.Resume(secondID)
	require.NoError(t, err)
	assert.Equal(t, "secondary", restarted.ModelName(), "resume must restore only the selected session's model")
}

func TestBoundSpawnUsesCurrentRoleModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model  string `json:"model"`
			Effort string `json:"reasoning_effort"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(
			w,
			`data: {"choices":[{"delta":{"content":%q},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n",
			req.Model+":"+req.Effort,
		)
	}))
	t.Cleanup(server.Close)
	ctrl := newThinkingTestController(t, setupThinkingTest(t, llm.OpenAI, server.URL))
	firstID := ctrl.SessionID()
	spawn := tooldef.NewRegistry(agenttool.BindForSession(ctrl.jobs, ctrl.session.Cwd()).Tools())["agent_spawn"]
	check := func(want string) {
		t.Helper()
		res, err := spawn.Run(t.Context(), json.RawMessage(`{"role":"worker","prompt":"hello"}`))
		require.NoError(t, err)
		result, err := ctrl.jobs.Wait(t.Context(), agenttool.ParseAgentResult(res.Content).JobID)
		require.NoError(t, err)
		require.Equal(t, want, result.Summary, result.Info.Error)
	}

	check("primary:")
	require.NoError(t, ctrl.SetRoleModel("worker", "secondary"))
	require.Error(t, ctrl.SetRoleModel("worker", "missing-model"))
	check("secondary:high")
	require.NoError(t, ctrl.SetRoleModel("worker", ""))
	check("primary:")

	require.NoError(t, ctrl.session.Append(llm.Message{Role: llm.RoleAssistant, Content: "saved"}))
	require.NoError(t, ctrl.NewSession())
	require.NoError(t, ctrl.SetModel("secondary"))
	check("secondary:high")
	require.NoError(t, ctrl.SetThinkLevel(llm.Low))
	check("secondary:low")
	_, err := ctrl.Resume(firstID)
	require.NoError(t, err)
	check("primary:")

	// A directory at the transcript path forces the model-change append to fail.
	path := ctrl.session.File()
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Mkdir(path, 0o700))
	require.ErrorContains(t, ctrl.SetModel("secondary"), "append model change")
	check("primary:")
}

func TestAppendUserBashAttributesOutputToActiveSession(t *testing.T) {
	ctrl := newTestController(t)
	oldID := ctrl.SessionID()

	require.NoError(t, ctrl.NewSession())
	newID := ctrl.SessionID()
	require.NotEqual(t, oldID, newID)

	require.NoError(t, ctrl.AppendUserBash(oldID, "printf old", "old", 0, false))
	assert.Empty(t, ctrl.ReplaySnapshot().Messages, "stale bash output must not enter the new session")

	require.NoError(t, ctrl.AppendUserBash(newID, "printf new", "new", 0, false))
	snapshot := ctrl.ReplaySnapshot()
	require.Len(t, snapshot.Messages, 1)
	assert.Equal(t, "new", snapshot.Tools[snapshot.Messages[0].ID].Output)
}

func TestUnlistedModelUsesPrimaryConnection(t *testing.T) {
	const baseURL = "http://127.0.0.1:9"
	ctrl := newTestController(t)
	require.NoError(t, ctrl.SetModel("unlisted"))
	cfg := ctrl.modelSnapshot()
	assert.Equal(t, "unlisted", cfg.Name)
	assert.Equal(t, baseURL, cfg.BaseURL)
	assert.Equal(t, "test-key", cfg.APIKey)
}

func newTestController(t *testing.T) *EngineController {
	t.Helper()
	const baseURL = "http://127.0.0.1:9"
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PHI_MODEL", "primary")
	t.Setenv("PHI_API_KEY", "test-key")
	t.Setenv("PHI_BASE_URL", baseURL)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".phi"), 0o700))
	config := fmt.Sprintf(`models:
  - name: primary
    api: OpenAI
    api_key: test-key
    base_url: %q
  - name: secondary
    api: OpenAI
    api_key: test-key
    base_url: %q
`, baseURL, baseURL)
	require.NoError(t, os.WriteFile(filepath.Join(home, ".phi", "config.yaml"), []byte(config), 0o600))
	cwd := t.TempDir()
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	return newInjectedController(t, proj)
}

func TestShouldPublishJobProgressDedup(t *testing.T) {
	last := make(map[progressSlot]progressSig)
	p := job.Progress{
		JobID:     "j",
		ToolUseID: "t1",
		Name:      "read",
		Status:    "in-progress",
		Detail:    "a.go",
	}
	require.True(t, shouldPublishJobProgress(last, p), "first should publish")
	require.False(t, shouldPublishJobProgress(last, p), "duplicate should drop")
	p.Status = "done"
	require.True(t, shouldPublishJobProgress(last, p), "status change should publish")
	p2 := job.Progress{
		JobID:     "j",
		ToolUseID: "t2",
		Name:      "bash",
		Status:    "done",
		Detail:    "ls",
	}
	require.True(t, shouldPublishJobProgress(last, p2), "new child should publish")
}

// The tool_use slot and the name+detail fallback slot are distinct rows even
// when their parts spell the same string. A tool_use id with a separator inside
// is not reachable from the model, but it pins key injectivity so a later
// refactor cannot merge the branches back into one joined key.
func TestShouldPublishJobProgressKeyKeepsBranchesApart(t *testing.T) {
	last := make(map[progressSlot]progressSig)
	require.True(t, shouldPublishJobProgress(last, job.Progress{
		JobID: "a", ToolUseID: "n\x00d", Name: "n", Detail: "d", Status: "done",
	}), "tool_use slot should publish")
	require.True(t, shouldPublishJobProgress(last, job.Progress{
		JobID: "a", Name: "n", Detail: "d", Status: "done",
	}), "fallback slot should not dedupe against the tool_use slot")
	require.False(t, shouldPublishJobProgress(last, job.Progress{
		JobID: "a", Name: "n", Detail: "d", Status: "done",
	}), "duplicate fallback slot should still drop")
}

func TestNewSessionRetainsThinking(t *testing.T) {
	ctrl := newThinkingTestController(t, setupThinkingTest(t, llm.OpenAI, "http://127.0.0.1:9"))
	require.NoError(t, ctrl.SetThinkLevel(llm.Low))
	require.NoError(t, ctrl.NewSession())
	assert.Equal(t, llm.Low, ctrl.ThinkLevel())
}

func TestModelSwitchUsesConfiguredThinking(t *testing.T) {
	requests := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(
			w,
			"data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
		)
	}))
	defer server.Close()
	cwd := setupThinkingTest(t, llm.OpenAI, server.URL)
	ctrl := newThinkingTestController(t, cwd)

	for _, tc := range []struct {
		model string
		mode  llm.ThinkMode
	}{{"secondary", llm.High}, {"primary", llm.Off}} {
		require.NoError(t, ctrl.SetThinkLevel(llm.Low))
		require.NoError(t, ctrl.SetModel(tc.model))
		assert.Equal(t, tc.mode, ctrl.ThinkLevel())
		for _, err := range ctrl.engine.Loop(t.Context(), "hello", agent.LoopOpts{}) {
			require.NoError(t, err)
		}
		select {
		case body := <-requests:
			assert.Equal(t, tc.model, body["model"])
			if tc.mode == llm.Off {
				assert.NotContains(t, body, "reasoning_effort")
			} else {
				assert.Equal(t, string(tc.mode), body["reasoning_effort"])
			}
		default:
			require.FailNow(t, "model switch did not send a request")
		}
	}
}

func TestModelSwitchExplicitHighRequestUnchanged(t *testing.T) {
	for _, tc := range []struct {
		api     llm.RouterType
		path    string
		fixture string
	}{
		{
			api:  llm.OpenAI,
			path: "/chat/completions",
			fixture: "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: [DONE]\n\n",
		},
		{
			api:  llm.OpenAIResponses,
			path: "/responses",
			fixture: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3," +
				"\"output_tokens\":1,\"total_tokens\":4}}}\n\n",
		},
	} {
		t.Run(string(tc.api), func(t *testing.T) {
			requests := make(chan map[string]any, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tc.path, r.URL.Path)
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				requests <- body
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, tc.fixture)
			}))
			defer server.Close()
			cwd := setupThinkingTest(t, tc.api, server.URL)

			run := func(ctrl *EngineController, prompt string) map[string]any {
				t.Helper()
				var text string
				for ev, err := range ctrl.engine.Loop(t.Context(), prompt, agent.LoopOpts{}) {
					require.NoError(t, err)
					if update, ok := ev.(session.AssistantMessageUpdate); ok &&
						update.Message.State == session.StateComplete {
						text = update.Message.FlatText()
					}
				}
				require.Equal(t, "OK", text, "fixture must produce a completed assistant response")
				select {
				case body := <-requests:
					require.Empty(t, requests, "expected exactly one request per turn")
					return body
				default:
					require.FailNow(t, "model switch did not send a request")
					return nil
				}
			}

			var bodies []map[string]any
			var sessionIDs []string
			for _, explicitHigh := range []bool{false, true} {
				ctrl := newThinkingTestController(t, cwd)
				sessionIDs = append(sessionIDs, ctrl.SessionID())
				// Seed identical history in fresh sessions so the extra setter is the only variable.
				run(ctrl, "remember this exchange")
				require.NoError(t, ctrl.SetThinkLevel(llm.Low))
				require.Equal(t, llm.Low, ctrl.ThinkLevel())
				require.NoError(t, ctrl.SetModel("secondary"))
				require.Equal(t, llm.High, ctrl.ThinkLevel())
				if explicitHigh {
					require.NoError(t, ctrl.SetThinkLevel(llm.High))
				}
				body := run(ctrl, "hello")
				require.Equal(t, "secondary", body["model"])
				if tc.api == llm.OpenAI {
					require.Equal(t, "high", body["reasoning_effort"])
				} else {
					reasoning, ok := body["reasoning"].(map[string]any)
					require.True(t, ok, "Responses request must contain reasoning")
					require.Equal(t, "high", reasoning["effort"])
				}
				bodies = append(bodies, body)
			}
			require.NotEqual(t, sessionIDs[0], sessionIDs[1], "controllers must use separate fresh sessions")
			// JSON decoding normalizes object order and whitespace; no request fields are excluded.
			assert.Equal(t, bodies[0], bodies[1], "explicit High must not change the complete outgoing request")
		})
	}
}

func setupThinkingTest(t *testing.T, api llm.RouterType, baseURL string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{"PHI_MODEL", "PHI_API_KEY", "PHI_BASE_URL", "PHI_THINK_LEVEL"} {
		t.Setenv(key, "")
	}
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".phi"), 0o700))
	config := fmt.Sprintf(`models:
  - name: primary
    default: true
    api: %s
    api_key: test-key
    base_url: %q
    think_level: off
  - name: secondary
    api: %s
    api_key: test-key
    base_url: %q
    think_level: high
`, api, baseURL, api, baseURL)
	require.NoError(t, os.WriteFile(filepath.Join(home, ".phi", "config.yaml"), []byte(config), 0o600))
	return t.TempDir()
}

func newThinkingTestController(t *testing.T, cwd string) *EngineController {
	t.Helper()
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	return newInjectedController(t, proj)
}

func newInjectedController(t *testing.T, proj *project.Project) *EngineController {
	t.Helper()
	require.NoError(t, proj.LoadConfig())
	config := proj.Config()
	pool, err := mcp.LoadPool(proj.MCPConfigFile())
	require.NoError(t, err)
	if pool == nil {
		pool = mcp.NewPool(nil)
	}
	var jobs *job.Manager
	cleanup := true
	t.Cleanup(func() {
		if cleanup {
			if jobs != nil {
				_ = jobs.Close(context.WithoutCancel(t.Context()))
			}
			_ = pool.Close()
		}
	})
	modelConfig := config.Model()
	sess, err := agent.NewSession(
		agent.WithCwd(proj.Root()),
		agent.WithSessionDir(proj.SessionDir()),
		agent.WithPersist(true),
		agent.WithModel(modelConfig.Name),
	)
	require.NoError(t, err)
	jobs, err = job.New(
		proj.JobsDir(),
		agent.NewEngineRunner(agent.DefaultChildSpecs()),
		job.WithParent(sess.ID(), sess.Cwd(), 0),
		job.WithRoleModels(config.ResolveRoleModels(modelConfig, config.Agents.Models.RoleOverrides())),
	)
	require.NoError(t, err)
	var binding *agenttool.Binding
	if config.Agents.Enabled {
		binding = agenttool.BindForSession(jobs, sess.Cwd())
	}
	toolList := tools.DefaultTools(pool)
	toolList = append(toolList, binding.Tools()...)
	system := prompt.Build(
		sess.Cwd(),
		modelConfig.SkillPaths,
		config.Agents.Enabled,
		binding.MaxConcurrent(),
		pool.ServerNames(),
	)
	client := llmclient.NewClient(
		modelConfig,
		model.HooksForConfig(modelConfig),
		tooldef.Definitions(toolList),
		system,
	)
	executor := agent.NewExecutor(tooldef.NewRegistry(toolList), sess.Cwd())
	engine, err := agent.NewEngine(compaction.LimitsForModel(modelConfig), sess, client, executor)
	require.NoError(t, err)
	ctrl := NewController(NewBus(nil), proj, sess, jobs, pool, engine)
	cleanup = false
	t.Cleanup(ctrl.Close)
	return ctrl
}
