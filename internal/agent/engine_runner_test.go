package agent_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestEngineRunnerViaJobManager(t *testing.T) {
	srv := textOnlySSEServer("explored auth module; see internal/auth/login.go")
	defer srv.Close()

	jobsRoot := t.TempDir()
	workdir := t.TempDir()

	runner := agent.EngineRunner{}
	mgr, err := job.New(job.Options{
		Root: jobsRoot,
		RoleModels: map[job.Role]llm.ModelConfig{
			job.RoleExplore: {Name: "fake", BaseURL: srv.URL, APIKey: "x"},
		},
		Runner: runner,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close(t.Context()) })

	info, err := mgr.Spawn(t.Context(), job.SpawnRequest{
		Prompt:      "Look at auth",
		Description: "auth explore",
		ParentID:    "parent-sess-1",
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

	runner := agent.EngineRunner{}
	text, err := runner.Run(t.Context(), job.RunEnv{
		Job: job.Meta{Dir: t.TempDir(), WorkDir: t.TempDir(), Prompt: "Reply OK"},
		Model: llm.ModelConfig{
			Name: "opaque-route", Preset: "gemini-2.5-pro", API: llm.Gemini,
			BaseURL: srv.URL, APIKey: "test",
			Think: llm.ThinkConfig{Enabled: true, Mode: llm.Low},
		},
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

	runner := agent.EngineRunner{}
	mgr, err := job.New(job.Options{
		Root: t.TempDir(),
		RoleModels: map[job.Role]llm.ModelConfig{
			job.RoleExplore: {Name: "fake", BaseURL: srv.URL, APIKey: "x"},
		},
		Runner: runner,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close(t.Context()) })

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

// childRequest is the slice of a chat-completion body this test inspects.
type childRequest struct {
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

// TestEngineRunnerChildUsesItsOwnWorkdir pins PP-03 end to end: the request a
// sub-agent sends must carry the instructions and cwd of its own workdir, not
// the ones of the process that spawned it.
func TestEngineRunnerChildUsesItsOwnWorkdir(t *testing.T) {
	var mu sync.Mutex
	var bodies []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", jsonMarshalDelta("recon done"))
		_, _ = fmt.Fprint(
			w,
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n",
		)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	workdir := filepath.Join(root, "pkg")
	require.NoError(t, os.Mkdir(workdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("child-workdir-rule"), 0o644))

	mgr, err := job.New(job.Options{
		Root: t.TempDir(),
		RoleModels: map[job.Role]llm.ModelConfig{
			job.RoleExplore: {Name: "fake", BaseURL: srv.URL, APIKey: "x"},
		},
		Runner: agent.EngineRunner{},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close(t.Context()) })

	info, err := mgr.Spawn(t.Context(), job.SpawnRequest{Prompt: "recon", WorkDir: workdir})
	require.NoError(t, err)
	_, err = mgr.Wait(t.Context(), info.ID)
	require.NoError(t, err)

	mu.Lock()
	raw := append([]string(nil), bodies...)
	mu.Unlock()
	require.NotEmpty(t, raw, "child engine should have issued a request")

	// Assert on the decoded prompt, never on the raw body: the wire format
	// escapes every backslash, so a path substring check against it passes on
	// POSIX and silently fails on Windows.
	system := childSystemPrompt(t, raw)
	require.NotEmpty(t, system, "child request should carry a system prompt")

	assert.Contains(t, system, "child-workdir-rule", "child must load the AGENTS.md of its workdir")
	assert.Contains(t, system, "- Current directory: "+workdir)
	assert.Contains(t, system, "- Workspace: "+root)

	processCwd, err := os.Getwd()
	require.NoError(t, err)
	assert.NotContains(t, system, "- Current directory: "+processCwd,
		"child must not report the parent process cwd")
}

// childSystemPrompt concatenates the system message of every captured request.
func childSystemPrompt(t *testing.T, bodies []string) string {
	t.Helper()
	var system strings.Builder
	for _, body := range bodies {
		var req childRequest
		require.NoError(t, json.Unmarshal([]byte(body), &req))
		for _, m := range req.Messages {
			if m.Role == "system" {
				system.WriteString(m.Content)
			}
		}
	}
	return system.String()
}
