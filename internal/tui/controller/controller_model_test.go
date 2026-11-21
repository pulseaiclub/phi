package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/extension/loader"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/session"
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
