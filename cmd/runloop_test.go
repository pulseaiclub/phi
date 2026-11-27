package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/agent"
	domain "github.com/pulseaiclub/phi/internal/core"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools"
)

func TestRunLoopTimeoutCancelsLLMRequest(t *testing.T) {
	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-block
	}))
	defer server.Close()
	defer close(block)

	sess, err := agent.NewSession(agent.WithCwd(t.TempDir()))
	require.NoError(t, err)
	engine, err := (agent.EngineConfig{}).NewEngine(
		domain.ModelConfig{
			Name:    "fake",
			BaseURL: server.URL,
			APIKey:  "test",
		},
		sess,
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	exit := runLoop(ctx, sess, engine, runOptions{prompt: "wait"})

	assert.Equal(t, ExitError, exit)
	assert.Less(t, time.Since(start), time.Second)
}

func TestJSONLEncoderEvents(t *testing.T) {
	var buf bytes.Buffer
	enc := &jsonlEncoder{out: &buf, enabled: true}

	enc.event(session.AssistantMessageUpdate{Message: session.Message{
		ID:    "a1",
		State: session.StateComplete,
		Content: []session.ContentBlock{
			{Type: session.BlockThinking, Text: "thinking…"},
			{Type: session.BlockText, Text: "hello world"},
		},
		Usage: session.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}})
	enc.event(session.ToolData{Run: session.ToolRun{
		ToolUseID: "c1", Name: "bash", Status: session.ToolDone, Detail: "echo hi", Output: "hi",
	}})
	enc.event(session.CompactionStarted{})
	enc.doneEvent("sess-1", "/tmp/sess.jsonl", ExitOK)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 4)

	var assistant map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &assistant))
	assert.Equal(t, "assistant", assistant["type"])
	assert.Equal(t, "complete", assistant["state"])
	assert.Equal(t, "hello world", assistant["text"])
	assert.Equal(t, "thinking…", assistant["thinking"])
	usage := assistant["usage"].(map[string]any)
	assert.Equal(t, float64(10), usage["prompt"])
	assert.Equal(t, float64(15), usage["total"])

	var tool map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &tool))
	assert.Equal(t, "tool", tool["type"])
	assert.Equal(t, "c1", tool["toolUseId"])
	assert.Equal(t, "bash", tool["toolName"])
	assert.Equal(t, "done", tool["status"])

	var compaction map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[2]), &compaction))
	assert.Equal(t, "compaction", compaction["type"])
	assert.Equal(t, "started", compaction["phase"])

	var done map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[3]), &done))
	assert.Equal(t, "done", done["type"])
	assert.Equal(t, "sess-1", done["sessionId"])
	assert.Equal(t, float64(ExitOK), done["exitCode"])
}

func TestJSONLEncoderDisabled(t *testing.T) {
	var buf bytes.Buffer
	enc := &jsonlEncoder{out: &buf, enabled: false}
	enc.event(session.ToolData{Run: session.ToolRun{ToolUseID: "c1", Status: session.ToolDone}})
	enc.doneEvent("s", "", ExitOK)
	assert.Empty(t, buf.String())
}

func TestResolveRunModel(t *testing.T) {
	cfg := &project.Config{
		Models: []domain.ModelConfig{
			{Name: "default"},
			{Name: "fast"},
			{Name: "smart"},
		},
		DefaultModel: "default",
	}

	m, err := resolveRunModel(cfg, "")
	require.NoError(t, err)
	assert.Equal(t, "default", m.Name)

	m, err = resolveRunModel(cfg, "smart")
	require.NoError(t, err)
	assert.Equal(t, "smart", m.Name)

	_, err = resolveRunModel(cfg, "missing")
	require.Error(t, err)
	assert.ErrorContains(t, err, `unknown model "missing"`)
	assert.ErrorContains(t, err, "configured models: default, fast, smart")
}

func TestResolveRunModelConfiguredModelWinsOverPHIModel(t *testing.T) {
	cfg := &project.Config{
		Models: []domain.ModelConfig{{
			Name:    "local-default",
			BaseURL: "https://local.example/v1",
		}},
		DefaultModel: "local-default",
	}

	m, err := resolveRunModel(cfg, "local-default")
	require.NoError(t, err)
	assert.Equal(t, "local-default", m.Name)
	assert.Equal(t, "https://local.example/v1", m.BaseURL)
}

func TestSelectBuiltinToolsErrorListsAvailableNames(t *testing.T) {
	_, err := selectBuiltinTools("read,nope,missing", nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, `unknown built-in tools "missing", "nope"`)
	assert.ErrorContains(t, err, "available: "+strings.Join(toolNames(tools.DefaultTools(nil)), ", "))
}

func TestSelectedBuiltinToolsStillAppendExternalTools(t *testing.T) {
	selected, err := selectBuiltinTools("read", nil)
	require.NoError(t, err)
	pool := mcp.NewPool(map[string]mcp.ServerConfig{
		"echo": {Command: []string{"true"}},
	})
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	jobs, err := job.New(t.TempDir(), job.RunnerFunc(func(_ context.Context, _ job.RunEnv) (string, error) {
		return "ok", nil
	}))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, jobs.Close(t.Context())) })

	sess, err := agent.NewSession(agent.WithCwd(t.TempDir()))
	require.NoError(t, err)
	cfg := agent.EngineConfig{Tools: selected, MCP: pool, Jobs: jobs}
	names := toolNames(cfg.CoreTools(sess))
	assert.Contains(t, names, "read")
	assert.NotContains(t, names, "bash")
	assert.Contains(t, names, "agent_spawn")
}
