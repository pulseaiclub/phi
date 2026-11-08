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
)

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

	workdir := t.TempDir()
	require.NoError(t,
		os.WriteFile(filepath.Join(workdir, "AGENTS.md"), []byte("child-workdir-rule"), 0o644))

	mgr, err := job.New(job.Options{
		Root: t.TempDir(),
		Runner: agent.EngineRunner{
			Model: llm.ModelConfig{Name: "fake", BaseURL: srv.URL, APIKey: "x"},
		},
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
