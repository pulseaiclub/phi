package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/extension/loader"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/project"
)

func TestMaxRoundsLifecycle(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		id := requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(
			w,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_%d","type":"function","function":{"name":"missing_test_tool","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n",
			id,
		)
	}))
	defer server.Close()

	for _, initial := range []int{0, 3} {
		t.Run(fmt.Sprintf("configured_%d", initial), func(t *testing.T) {
			ctrl := newRoundsController(t, server.URL, initial)
			assert.Equal(t, initial, ctrl.MaxRounds(), "palette getter must reflect config")
			require.NoError(t, ctrl.SetModel("secondary"))
			assert.Equal(t, initial, ctrl.MaxRounds())
			require.NoError(t, ctrl.SetMaxRounds(2))
			require.Error(t, ctrl.SetMaxRounds(0))
			require.Error(t, ctrl.SetMaxRounds(-1))

			checkBudget := func(t *testing.T) {
				t.Helper()
				assert.Equal(t, 2, ctrl.MaxRounds(), "palette getter must retain session override")
				assert.Equal(t, 2, ctrl.modelForRole(job.RoleExplore).MaxRounds)
				require.NoError(t, ctrl.SetRoleModel("worker", "secondary"))
				assert.Equal(t, 2, ctrl.modelForRole(job.RoleWorker).MaxRounds)
				before := requests.Load()
				asked := 0
				ctrl.engine.SetContinueAsk(func(_ context.Context, budget int) (bool, error) {
					asked++
					assert.Equal(t, 2, budget)
					return false, nil
				})
				var loopErr error
				for _, err := range ctrl.engine.Loop(t.Context(), "test budget", agent.LoopOpts{}) {
					if err != nil {
						loopErr = err
						break
					}
				}
				require.ErrorIs(t, loopErr, agent.ErrMaxRounds)
				assert.Equal(t, 1, asked)
				assert.Equal(t, int32(3), requests.Load()-before)
			}

			for _, name := range []string{"primary", "arbitrary-model"} {
				require.NoError(t, ctrl.SetModel(name))
				assert.Equal(t, name, ctrl.ModelName())
				checkBudget(t)
			}
			previousID := ctrl.SessionID()
			require.NoError(t, ctrl.NewSession())
			assert.NotEqual(t, previousID, ctrl.SessionID())
			checkBudget(t)
			_, err := ctrl.Resume(previousID)
			require.NoError(t, err)
			assert.Equal(t, previousID, ctrl.SessionID())
			checkBudget(t)
			assert.Equal(t, initial, ctrl.proj.Config().MaxRounds, "session override must not alter config")
		})
	}
}

func TestModelLifecycle(t *testing.T) {
	ctrl := newRoundsController(t, "http://127.0.0.1:9", 3)
	cwd := ctrl.cwd
	flush := func() {
		require.NoError(t, ctrl.engine.Session().Append(llm.Message{Role: llm.RoleUser, Content: "hello"}))
		require.NoError(t, ctrl.engine.Session().Append(llm.Message{Role: llm.RoleAssistant, Content: "done"}))
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

func TestMaxRoundsConcurrentRoleSnapshot(t *testing.T) {
	ctrl := newRoundsController(t, "http://127.0.0.1:9", 3)
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 1000 {
			assert.Positive(t, ctrl.modelForRole(job.RoleExplore).MaxRounds)
			assert.Positive(t, ctrl.MaxRounds())
		}
	})
	for i := range 1000 {
		require.NoError(t, ctrl.SetMaxRounds(i+1))
	}
	wg.Wait()
}

func newRoundsController(t *testing.T, baseURL string, maxRounds int) *EngineController {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PHI_MODEL", "primary")
	t.Setenv("PHI_API_KEY", "test-key")
	t.Setenv("PHI_BASE_URL", baseURL)
	t.Setenv(loader.EnvExtensions, "off")
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".phi"), 0o700))
	config := fmt.Sprintf(`max_rounds: %d
models:
  - name: primary
    api: OpenAI
    api_key: test-key
    base_url: %q
  - name: secondary
    api: OpenAI
    api_key: test-key
    base_url: %q
`, maxRounds, baseURL, baseURL)
	require.NoError(t, os.WriteFile(filepath.Join(home, ".phi", "config.yaml"), []byte(config), 0o600))
	cwd := t.TempDir()
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	ctrl, err := NewController(NewBus(nil), proj)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)
	return ctrl
}
