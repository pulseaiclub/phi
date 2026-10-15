package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cli "github.com/pulseaiclub/pli"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

func TestRunOptionsFromFlags(t *testing.T) {
	f, _, err := runCommand.Parse([]string{
		"-p", "do the thing",
		"--jsonl",
		"--yolo",
		"--max-rounds", "10",
		"--timeout", "10m",
		"--session", "abc123",
		"--tools", "grep, read,grep",
	})
	require.NoError(t, err)
	opts, err := runOptionsFromFlags(f)
	require.NoError(t, err)
	assert.Equal(t, "do the thing", opts.prompt)
	assert.True(t, opts.jsonl)
	assert.True(t, opts.yolo)
	assert.Equal(t, 10, opts.maxRounds)
	assert.Equal(t, 10*time.Minute, opts.timeout)
	assert.Equal(t, "abc123", opts.session)
	assert.Equal(t, "grep, read,grep", opts.builtinTools)
	// Selection is order-stable and deduplicated at selection time.
	selected, err := selectBuiltinTools(opts.builtinTools, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"read", "grep"}, toolNames(selected))
	assert.False(t, opts.continueLast)
}

func TestRunOptionsFromFlagsEqualsForms(t *testing.T) {
	f, _, err := runCommand.Parse([]string{
		"--prompt=hi",
		"--max-rounds=5",
		"--timeout=1500ms",
		"--session-dir=/tmp/sess",
		"--continue-last",
		"--tools=write,bash",
	})
	require.NoError(t, err)
	opts, err := runOptionsFromFlags(f)
	require.NoError(t, err)
	assert.Equal(t, "hi", opts.prompt)
	assert.Equal(t, 5, opts.maxRounds)
	assert.Equal(t, 1500*time.Millisecond, opts.timeout)
	assert.Equal(t, "/tmp/sess", opts.sessionDir)
	assert.True(t, opts.continueLast)
	assert.Equal(t, "write,bash", opts.builtinTools)
	selected, err := selectBuiltinTools(opts.builtinTools, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"bash", "write"}, toolNames(selected))
}

func TestRunCommandParseErrors(t *testing.T) {
	cases := [][]string{
		{"--prompt"},             // missing value
		{"--max-rounds", "abc"},  // non-integer
		{"--max-rounds", "0"},    // non-positive
		{"--timeout"},            // missing value
		{"--timeout", "abc"},     // invalid duration
		{"--timeout", "0"},       // non-positive
		{"--timeout", "-1s"},     // non-positive
		{"--tools"},              // missing value
		{"--tools="},             // empty list
		{"--tools", "read,,ls"},  // empty name
		{"--tools", "read,nope"}, // unknown name
		{"--bogus", "x"},         // unknown flag
	}
	for _, args := range cases {
		_, _, err := runCommand.Parse(args)
		assert.Error(t, err, "args %v should error", args)
	}
}

func TestRunOptionsRequiresPrompt(t *testing.T) {
	f, _, err := runCommand.Parse([]string{"--jsonl"})
	require.NoError(t, err)
	_, err = runOptionsFromFlags(f)
	var ue *cli.UsageError
	require.ErrorAs(t, err, &ue)
	assert.Contains(t, err.Error(), "prompt is required")
}

func TestRunOptionsMutualExclusion(t *testing.T) {
	f, _, err := runCommand.Parse([]string{"-p", "hi", "--continue-last", "--session", "x"})
	require.NoError(t, err)
	_, err = runOptionsFromFlags(f)
	var ue *cli.UsageError
	require.ErrorAs(t, err, &ue)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestRunOptionsLeavesBuiltinToolsUnsetByDefault(t *testing.T) {
	f, _, err := runCommand.Parse([]string{"-p", "hi"})
	require.NoError(t, err)
	opts, err := runOptionsFromFlags(f)
	require.NoError(t, err)
	assert.Empty(t, opts.builtinTools)
}

func toolNames(list []tooldef.Tool) []string {
	names := make([]string, 0, len(list))
	for _, tool := range list {
		names = append(names, tool.Definition.Name)
	}
	return names
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
	jobs, err := job.New(job.Options{
		Root: t.TempDir(),
		Runner: job.RunnerFunc(func(_ context.Context, _ job.RunEnv) (string, error) {
			return "ok", nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, jobs.Close(t.Context())) })

	sess, err := agent.NewSession(agent.WithCwd(t.TempDir()))
	require.NoError(t, err)
	engine, err := agent.NewEngine(
		llm.ModelConfig{Name: "test", APIKey: "x", BaseURL: "http://127.0.0.1:9"},
		sess,
		agent.WithTools(selected),
		agent.WithMCP(pool),
		agent.WithJobs(jobs),
	)
	require.NoError(t, err)
	assert.True(t, engine.HasTool("read"))
	assert.False(t, engine.HasTool("bash"))
	assert.True(t, engine.HasTool("agent_spawn"))
}

func TestRunLoopTimeoutCancelsLLMRequest(t *testing.T) {
	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-block
	}))
	defer server.Close()
	defer close(block)

	sess, err := agent.NewSession(agent.WithCwd(t.TempDir()))
	require.NoError(t, err)
	engine, err := agent.NewEngine(
		llm.ModelConfig{
			Name:    "fake",
			BaseURL: server.URL,
			APIKey:  "test",
		},
		sess,
		agent.WithGate(permission.AllowAll{}),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	exit := runLoop(ctx, engine, runOptions{prompt: "wait"})

	assert.Equal(t, ExitError, exit)
	assert.Less(t, time.Since(start), time.Second)
}

func TestClassifyRunError(t *testing.T) {
	assert.Equal(t, ExitMaxRounds, classifyRunError(fmt.Errorf("agent: %w (64)", agent.ErrMaxRounds)))
	assert.Equal(t, ExitError, classifyRunError(errors.New("LLM API error: (500) boom")))
	assert.Equal(t, ExitError, classifyRunError(nil))
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

func TestJSONLEncoderNoSecretLeak(t *testing.T) {
	// The encoder only serializes session events; it must not emit anything
	// resembling an API key from config or headers.
	var buf bytes.Buffer
	enc := &jsonlEncoder{out: &buf, enabled: true}
	enc.event(session.ToolData{Run: session.ToolRun{
		ToolUseID: "c1", Name: "read", Status: session.ToolDone, Output: "file contents",
	}})
	assert.NotContains(t, buf.String(), "sk-")
}

func TestRootDispatchHelpAndUnknown(t *testing.T) {
	err := root().Dispatch([]string{"--help"})
	var he *cli.HelpError
	require.ErrorAs(t, err, &he)
	assert.Contains(t, he.Help, "usage: phi")
	assert.Contains(t, he.Help, "run")
	assert.Contains(t, he.Help, "sessions")

	err = root().Dispatch([]string{"bogus"})
	var ue *cli.UsageError
	require.ErrorAs(t, err, &ue)
	assert.Contains(t, err.Error(), `unknown command "bogus"`)
}

func TestRunCommandHelp(t *testing.T) {
	err := root().Dispatch([]string{"run", "--help"})
	var he *cli.HelpError
	require.ErrorAs(t, err, &he)
	assert.Contains(t, he.Help, "usage: phi run")
	assert.Contains(t, he.Help, "--prompt")
	assert.Contains(t, he.Help, "--yolo")
	assert.Contains(t, he.Help, "--tools")
}
