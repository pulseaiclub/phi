package agent_test

import (
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

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/session"
)

func textOnlySSEServer(reply string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := jsonMarshalDelta(reply)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
		_, _ = fmt.Fprint(
			w,
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n",
		)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func jsonMarshalDelta(content string) string {
	// Keep tiny and stable for tests; escape is enough for plain ASCII replies.
	content = strings.ReplaceAll(content, `\`, `\\`)
	content = strings.ReplaceAll(content, `"`, `\"`)
	content = strings.ReplaceAll(content, "\n", `\n`)
	return `{"choices":[{"delta":{"role":"assistant","content":"` + content + `"}}]}`
}

func newExploreManager(t *testing.T, baseURL string, opts ...job.Option) *job.Manager {
	t.Helper()
	opts = append(opts, job.WithRoleModels(map[job.Role]llm.ModelConfig{
		job.RoleExplore: {Name: "fake", BaseURL: baseURL, APIKey: "x"},
	}))
	mgr, err := job.New(t.TempDir(), agent.NewEngineRunner(agent.DefaultChildSpecs()), opts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close(t.Context()) })
	return mgr
}

func TestEngineRunnerMissingSpec(t *testing.T) {
	runner := agent.NewEngineRunner(map[job.Role]agent.ChildSpec{
		job.RoleWorker: agent.SpecForRole(job.RoleWorker),
	})
	_, err := runner.Run(t.Context(), job.RunEnv{Job: job.Meta{Dir: t.TempDir()}})
	require.EqualError(t, err, `agent: no child spec for role "explore"`)
}

func TestEngineRunnerViaJobManager(t *testing.T) {
	srv := textOnlySSEServer("explored auth module; see internal/auth/login.go")
	defer srv.Close()

	workdir := t.TempDir()
	mgr := newExploreManager(t, srv.URL, job.WithParent("parent-sess-1", "", 0))

	info, err := mgr.Spawn(t.Context(), job.SpawnRequest{
		Prompt:      "Look at auth",
		Description: "auth explore",
		WorkDir:     workdir,
	})
	require.NoError(t, err)
	res, err := mgr.Wait(t.Context(), info.ID)
	require.NoError(t, err)
	assert.Equal(t, job.StatusCompleted, res.Info.Status)
	assert.Contains(t, res.Summary, "auth module")

	// result.md on disk
	data, err := os.ReadFile(res.Info.ResultPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "login.go")

	// child session persisted under job dir
	sessDir := filepath.Join(res.Info.Dir, "session")
	entries, err := os.ReadDir(sessDir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	var jsonl string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			jsonl = filepath.Join(sessDir, e.Name())
			break
		}
	}
	require.NotEmpty(t, jsonl)
	raw, err := os.ReadFile(jsonl)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "parent-sess-1")
	assert.Contains(t, string(raw), "parentSession")

	_, err = session.OpenSession(jsonl)
	require.NoError(t, err)
}

func TestEngineRunnerExplicitPreset(t *testing.T) {
	type request struct {
		GenerationConfig struct {
			ThinkingConfig struct {
				ThinkingBudget *int `json:"thinkingBudget"`
			} `json:"thinkingConfig"`
		} `json:"generationConfig"`
	}
	requests := make(chan request, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body request
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		requests <- body
		assert.Contains(t, r.URL.Path, "opaque-route:streamGenerateContent")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"OK\"}]}}]}\n\n")
	}))
	defer srv.Close()

	text, err := agent.NewEngineRunner(agent.DefaultChildSpecs()).Run(t.Context(), job.RunEnv{
		Model: llm.ModelConfig{
			Name: "opaque-route", Preset: "gemini-2.5-pro", API: llm.Gemini,
			BaseURL: srv.URL, APIKey: "test",
			Think: llm.ThinkConfig{Enabled: true, Mode: llm.Low},
		},
		Job: job.Meta{Dir: t.TempDir(), WorkDir: t.TempDir(), Prompt: "Reply OK"},
		Log: func(string) {},
	})
	require.NoError(t, err)
	assert.Equal(t, "OK", text)
	select {
	case body := <-requests:
		require.NotNil(t, body.GenerationConfig.ThinkingConfig.ThinkingBudget)
		assert.Equal(t, 2048, *body.GenerationConfig.ThinkingConfig.ThinkingBudget)
	default:
		assert.Fail(t, "child engine did not send a request")
	}
}

func TestEngineRunnerCancel(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-block // never stream until cancelled path abandons request
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	defer close(block)

	mgr := newExploreManager(t, srv.URL)

	info, err := mgr.Spawn(t.Context(), job.SpawnRequest{
		Prompt:  "hang",
		WorkDir: t.TempDir(),
	})
	require.NoError(t, err)

	require.NoError(t, mgr.Cancel(t.Context(), info.ID))
	res, err := mgr.Wait(t.Context(), info.ID)
	require.NoError(t, err)
	assert.Equal(t, job.StatusCancelled, res.Info.Status)
}
