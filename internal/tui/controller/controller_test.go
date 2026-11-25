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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/extension/loader"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/session"

	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/extension/create"

	"github.com/pulseaiclub/phi/internal/job"
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

	bus := NewBus(nil)
	ctrl, err := NewController(bus, proj)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)
	require.NotNil(t, ctrl)
	require.NotNil(t, ctrl.engine)
	assert.Equal(t, cwd, ctrl.cwd)
	assert.NotEmpty(t, ctrl.sessionDir)
	assert.Same(t, proj, ctrl.proj)
}

func TestModelLifecycle(t *testing.T) {
	ctrl := newTestController(t, "http://127.0.0.1:9")
	cwd := ctrl.cwd
	flush := func() {
		require.NoError(t, ctrl.sess.Append(llm.Message{Role: llm.RoleUser, Content: "hello"}))
		require.NoError(t, ctrl.sess.Append(llm.Message{Role: llm.RoleAssistant, Content: "done"}))
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
	restarted, err := NewController(NewBus(nil), proj)
	require.NoError(t, err)
	defer restarted.Close()
	assert.Equal(t, "primary", restarted.ModelName(), "startup must use the configured default")
	_, err = restarted.Resume(secondID)
	require.NoError(t, err)
	assert.Equal(t, "secondary", restarted.ModelName(), "resume must restore only the selected session's model")
}

func TestAgentToolsFollowSettingsAcrossModelSwitch(t *testing.T) {
	ctrl := newTestController(t, "http://127.0.0.1:9")
	id := ctrl.SessionID()
	// The installed engine is built from exactly this config, so its CoreTools
	// are the base and agent_* tools that engine actually registers.
	toolNames := func() []string {
		cfg := ctrl.engineConfig(ctrl.modelSnapshot(), ctrl.Extensions())
		list := cfg.CoreTools(ctrl.sess)
		names := make([]string, 0, len(list))
		for _, tool := range list {
			names = append(names, tool.Definition.Name)
		}
		return names
	}
	require.NoError(t, ctrl.SetAgentsEnabled(true))
	require.NoError(t, ctrl.SetModel("secondary"))
	assert.Contains(t, toolNames(), "agent_spawn")
	require.NoError(t, ctrl.SetAgentsEnabled(false))
	assert.NotContains(t, toolNames(), "agent_spawn")
	assert.Contains(t, toolNames(), "bash")
	assert.Equal(t, id, ctrl.SessionID())
}

func newTestController(t *testing.T, baseURL string) *EngineController {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PHI_MODEL", "primary")
	t.Setenv("PHI_API_KEY", "test-key")
	t.Setenv("PHI_BASE_URL", baseURL)
	t.Setenv(loader.EnvExtensions, "off")
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
	ctrl, err := NewController(NewBus(nil), proj)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)
	return ctrl
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
				sessionIDs = append(sessionIDs, ctrl.sess.ID())
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
	t.Setenv(loader.EnvExtensions, "off")
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
	ctrl, err := NewController(NewBus(nil), proj)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)
	return ctrl
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

	ctrl.StartPrompt("keep this once", nil, nil)
	first := waitLoopFinished(t, ctrl.bus, 1)
	require.NotEmpty(t, first.ErrorText)
	assert.False(t, first.Cancelled)
	assert.True(t, retryReady(ctrl))
	assert.Len(t, userMessages(ctrl), 1)

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
	for _, entry := range ctrl.sess.PathEntries() {
		message, ok := entry.(session.SessionMessageEntry)
		if ok && message.Message.Role == llm.RoleUser {
			users = append(users, message.Message)
		}
	}
	return users
}

func TestSwapExtensionRunner_ClosesPrevious(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PHI_MODEL", "test-model")
	t.Setenv("PHI_API_KEY", "test-key")
	t.Setenv("PHI_BASE_URL", "http://127.0.0.1:9")
	t.Setenv(loader.EnvExtensions, "off")

	cwd := t.TempDir()
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	require.NoError(t, proj.LoadConfig())

	ctrl, err := NewController(NewBus(nil), proj)
	require.NoError(t, err)

	first, _, err := extension.Load(t.TempDir(), "")
	require.NoError(t, err)
	require.NotNil(t, first)
	ctrl.bus.Drain() // drop startup messages
	ctrl.swapExtensionRunner(first)
	assert.Same(t, first, ctrl.Extensions())
	assertStatusReset(t, ctrl.bus)

	second, _, err := extension.Load(t.TempDir(), "")
	require.NoError(t, err)
	require.NotNil(t, second)
	ctrl.bus.Drain()
	ctrl.swapExtensionRunner(second)
	assert.Same(t, second, ctrl.Extensions())
	assertStatusReset(t, ctrl.bus)
	// Previous runner must be closed; a second Close is a safe no-op.
	first.Close()

	ctrl.Close()
	assert.Nil(t, ctrl.Extensions())
	// Idempotent.
	ctrl.Close()
}

func TestResumeExtensionLifecycleUsesOutgoingRunner(t *testing.T) {
	t.Setenv("PHI_MODEL", "test-model")
	t.Setenv("PHI_API_KEY", "test-key")
	t.Setenv("PHI_BASE_URL", "http://127.0.0.1:9")

	t.Setenv(loader.EnvExtensions, "")
	t.Setenv("PHI_THINK_LEVEL", "")
	logPath := filepath.Join(t.TempDir(), "lifecycle.log")
	t.Setenv("PHI_TEST_LIFECYCLE_LOG", logPath)
	cwd := t.TempDir()
	extDir := filepath.Join(cwd, ".phi", "extensions", "lifecycle")
	src := `package main

import (
	"fmt"
	"os"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/phi"
)

func record(kind string) {
	f, err := os.OpenFile(os.Getenv("PHI_TEST_LIFECYCLE_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(f, "%s %d\n", kind, os.Getpid())
	_ = f.Close()
}

func main() {
	m := phi.New("lifecycle", "0.0.1")
	m.Subscribe(ext.EventSessionStart, func() { record("start") })
	m.Subscribe(ext.EventSessionShutdown, func() { record("shutdown") })
	_ = m.Run()
}
`
	require.NoError(t, create.Materialize(t.Context(), extDir, "lifecycle", "0.0.1", src))
	// Build before isolating HOME so Go can use its existing module cache.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	ctrl, err := NewController(NewBus(nil), proj)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)

	events := func() map[string]map[string]bool {
		data, err := os.ReadFile(logPath)
		if err != nil {
			return nil
		}
		out := make(map[string]map[string]bool)
		for line := range strings.SplitSeq(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			pid := fields[1]
			if _, ok := out[pid]; !ok {
				out[pid] = make(map[string]bool)
			}
			out[pid][fields[0]] = true
		}
		return out
	}
	var oldPID string
	require.Eventually(t, func() bool {
		for pid, got := range events() {
			if got["start"] {
				oldPID = pid
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond)

	outgoing := ctrl.Extensions()
	_, err = ctrl.Resume("missing-session")
	require.Error(t, err)
	assert.Same(t, outgoing, ctrl.Extensions())
	require.Len(t, outgoing.Loaded(), 1)

	target, err := ctrl.sess.Fork()
	require.NoError(t, err)
	_, err = ctrl.Resume(target.ID())
	require.NoError(t, err)

	var incomingPID string
	require.Eventually(t, func() bool {
		got := events()
		for pid, eventSet := range got {
			if pid != oldPID && eventSet["start"] {
				incomingPID = pid
			}
		}
		return incomingPID != "" && got[oldPID]["shutdown"]
	}, 5*time.Second, 10*time.Millisecond)

	got := events()
	assert.True(t, got[oldPID]["shutdown"], "outgoing extension must receive session_shutdown")
	assert.False(t, got[incomingPID]["shutdown"], "incoming extension must not receive outgoing session_shutdown")
}

// assertStatusReset requires a footer extension-status reset message: swapping
// the runner kills the previous subprocesses, whose UI state must not linger.
func assertStatusReset(t *testing.T, bus *Bus) {
	t.Helper()
	for _, m := range bus.Drain() {
		if eff, ok := m.(ExtSessionEffectsMsg); ok && eff.StatusSet && eff.Status == "" {
			return
		}
	}
	require.Fail(t, "expected an extension status reset (ExtSessionEffectsMsg{StatusSet:true, Status:\"\"})")
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
	require.NoError(t, ctrl.sess.Append(
		llm.Message{Role: llm.RoleUser, Content: "hello"},
		llm.Message{Role: llm.RoleAssistant, Content: "hi"},
	))
	return ctrl
}

func TestForkSessionSwitchesToDistinctSessionWithSameContext(t *testing.T) {
	ctrl := forkTestController(t)
	srcID, srcFile := ctrl.SessionID(), ctrl.SessionFile()
	srcPath := pathIDs(ctrl.sess)
	srcBytes, err := os.ReadFile(srcFile)
	require.NoError(t, err)

	newID, err := ctrl.ForkSession()
	require.NoError(t, err)

	assert.NotEqual(t, srcID, newID)
	assert.Equal(t, newID, ctrl.SessionID())
	assert.NotEqual(t, srcFile, ctrl.SessionFile())
	assert.Equal(t, srcPath, pathIDs(ctrl.sess), "the fork starts on the source's branch")
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

	loaded, err := session.OpenSession(ctrl.SessionFile())
	require.NoError(t, err)
	assert.Equal(t, newID, loaded.ID())
	assert.Equal(t, pathIDs(ctrl.sess), pathIDsOf(loaded))
	assert.Equal(t, srcID, forkHeader(t, ctrl.SessionFile()).ParentSession, "the fork records its source")

	list, err := session.ListSessions(ctrl.SessionDir())
	require.NoError(t, err)
	assert.Len(t, ids(list), 2, "both sessions stay available")
	assert.Contains(t, ids(list), srcID)
	assert.Contains(t, ids(list), newID)
}

func TestForkSessionLeavesSourceResumable(t *testing.T) {
	ctrl := forkTestController(t)
	srcID := ctrl.SessionID()
	srcPath := pathIDs(ctrl.sess)

	newID, err := ctrl.ForkSession()
	require.NoError(t, err)
	require.Equal(t, newID, ctrl.SessionID())

	_, err = ctrl.Resume(srcID)
	require.NoError(t, err)
	assert.Equal(t, srcID, ctrl.SessionID())
	assert.Equal(t, srcPath, pathIDs(ctrl.sess))

	_, err = ctrl.Resume(newID)
	require.NoError(t, err)
	assert.Equal(t, srcPath, pathIDs(ctrl.sess), "the fork reopens with the same branch")
}

func TestForkSessionFailureKeepsSourceActive(t *testing.T) {
	ctrl := forkTestController(t)
	srcID := ctrl.SessionID()
	path := pathIDs(ctrl.sess)

	// A file where the session directory belongs makes the fork write fail; it
	// is the only failure this package can force without a fake session store.
	dir := ctrl.SessionDir()
	require.NoError(t, os.RemoveAll(dir))
	require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0o600))

	id, err := ctrl.ForkSession()
	require.Error(t, err)
	assert.Empty(t, id)
	assert.Equal(t, srcID, ctrl.SessionID(), "a failed fork must not switch sessions")
	assert.Equal(t, path, pathIDs(ctrl.sess), "the source context is untouched")
	assert.NotEmpty(t, ctrl.ReplaySnapshot().Messages)
}

func TestForkSessionOfEmptySession(t *testing.T) {
	ctrl := newThinkingTestController(t, setupThinkingTest(t, llm.OpenAI, "http://127.0.0.1:9"))
	require.Empty(t, ctrl.ReplaySnapshot().Messages)

	newID, err := ctrl.ForkSession()
	require.NoError(t, err)
	assert.Equal(t, newID, ctrl.SessionID())
	assert.Empty(t, ctrl.ReplaySnapshot().Messages, "an empty session forks empty")
	assert.Equal(t, ctrl.cwd, ctrl.sess.Cwd())
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

func TestShouldPublishJobProgressDedup(t *testing.T) {
	c := &EngineController{}
	p := job.Progress{
		JobID:     "j",
		ToolUseID: "t1",
		Name:      "read",
		Status:    "in-progress",
		Detail:    "a.go",
	}
	require.True(t, c.shouldPublishJobProgress(p), "first should publish")
	require.False(t, c.shouldPublishJobProgress(p), "duplicate should drop")
	p.Status = "done"
	require.True(t, c.shouldPublishJobProgress(p), "status change should publish")
	p2 := job.Progress{
		JobID:     "j",
		ToolUseID: "t2",
		Name:      "bash",
		Status:    "done",
		Detail:    "ls",
	}
	require.True(t, c.shouldPublishJobProgress(p2), "new child should publish")
}

// The tool_use slot and the name+detail fallback slot are distinct rows even
// when their parts spell the same string. A tool_use id with a separator inside
// is not reachable from the model, but it pins key injectivity so a later
// refactor cannot merge the branches back into one joined key.
func TestShouldPublishJobProgressKeyKeepsBranchesApart(t *testing.T) {
	c := &EngineController{}
	require.True(t, c.shouldPublishJobProgress(job.Progress{
		JobID: "a", ToolUseID: "n\x00d", Name: "n", Detail: "d", Status: "done",
	}), "tool_use slot should publish")
	require.True(t, c.shouldPublishJobProgress(job.Progress{
		JobID: "a", Name: "n", Detail: "d", Status: "done",
	}), "fallback slot should not dedupe against the tool_use slot")
	require.False(t, c.shouldPublishJobProgress(job.Progress{
		JobID: "a", Name: "n", Detail: "d", Status: "done",
	}), "duplicate fallback slot should still drop")
}

// drainCompactionResults empties the bus and returns compaction results, newest last.
func drainCompactionResults(bus *Bus) []CompactionFinishedMsg {
	var out []CompactionFinishedMsg
	for _, m := range bus.Drain() {
		if result, ok := m.(CompactionFinishedMsg); ok {
			out = append(out, result)
		}
	}
	return out
}

// /compact on a fresh session has nothing to walk. It must report that state
// rather than a success that reclaimed nothing (and must not panic in the cut
// planner, which indexes the first retained entry).
func TestCompactReportsNothingToCompact(t *testing.T) {
	ctrl := newTestController(t, "http://127.0.0.1:1")

	require.NoError(t, ctrl.Compact())

	var got CompactionFinishedMsg
	require.Eventually(t, func() bool {
		results := drainCompactionResults(ctrl.bus)
		if len(results) == 0 {
			return false
		}
		got = results[len(results)-1]
		return true
	}, 5*time.Second, 10*time.Millisecond)

	assert.False(t, got.Compacted)
	assert.NoError(t, got.Err)
}

// The slot is claimed for as long as a summary runs: a second /compact must be
// refused, because both would cut the same leaf and the loser would abort on
// the anchor check. Releasing the slot must accept the next one.
func TestBeginCompactionRefusesConcurrentRun(t *testing.T) {
	ctrl := newTestController(t, "http://127.0.0.1:1")

	_, ok := ctrl.beginCompaction()
	require.True(t, ok, "the first run claims the slot")

	_, ok = ctrl.beginCompaction()
	require.False(t, ok, "a second run must be refused while the first holds the slot")

	ctrl.endCompaction()
	_, ok = ctrl.beginCompaction()
	require.True(t, ok, "releasing the slot accepts the next run")
	ctrl.endCompaction()
}

// Esc during a compaction cancels the context the summary worker runs on, so it
// cannot keep compacting a session the user is about to switch away from.
func TestCancelCancelsRunningCompaction(t *testing.T) {
	ctrl := newTestController(t, "http://127.0.0.1:1")

	ctx, ok := ctrl.beginCompaction()
	require.True(t, ok)
	require.NoError(t, ctx.Err())

	ctrl.Cancel()
	require.ErrorIs(t, ctx.Err(), context.Canceled, "Cancel must reach the compaction context")
	ctrl.endCompaction()
}

func TestContextWindowUsesCompactionWindow(t *testing.T) {
	ctrl := &EngineController{modelCfg: llm.ModelConfig{ContextWindow: 1_050_000, InputLimit: 922_000}}
	assert.Equal(t, 922_000, ctrl.ContextWindow())

	ctrl.modelCfg = llm.ModelConfig{ContextWindow: 1_048_576}
	assert.Equal(t, 1_048_576, ctrl.ContextWindow())
}
