package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/llm/openai"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/tools"
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
)

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
	jobs := agenttool.Bind(ctrl.jobs, ctrl.session.ID(), ctrl.session.Cwd())
	engine, err := NewParentEngine(ctrl.modelCfg, ctrl.session, base, nil, jobs)
	require.NoError(t, err)
	assert.ElementsMatch(
		t,
		[]string{base[0].Definition.Name, "agent_spawn", "agent_wait"},
		requestToolNames(request(engine)),
	)
}
