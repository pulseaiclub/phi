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

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRunArgs(t *testing.T) {
	opts, err := parseRunArgs([]string{
		"-p", "do the thing",
		"--jsonl",
		"--max-rounds", "10",
		"--timeout", "10m",
		"--session", "abc123",
	})
	require.NoError(t, err)
	assert.Equal(t, "do the thing", opts.prompt)
	assert.True(t, opts.jsonl)
	assert.Equal(t, 10, opts.maxRounds)
	assert.Equal(t, 10*time.Minute, opts.timeout)
	assert.Equal(t, "abc123", opts.session)
	assert.False(t, opts.continueLast)
}

func TestParseRunArgsEqualsForms(t *testing.T) {
	opts, err := parseRunArgs([]string{
		"--prompt=hi",
		"--max-rounds=5",
		"--timeout=1500ms",
		"--session-dir=/tmp/sess",
		"--continue-last",
	})
	require.NoError(t, err)
	assert.Equal(t, "hi", opts.prompt)
	assert.Equal(t, 5, opts.maxRounds)
	assert.Equal(t, 1500*time.Millisecond, opts.timeout)
	assert.Equal(t, "/tmp/sess", opts.sessionDir)
	assert.True(t, opts.continueLast)
}

func TestParseRunArgsErrors(t *testing.T) {
	cases := [][]string{
		{"--prompt"},            // missing value
		{"--max-rounds", "abc"}, // non-integer
		{"--max-rounds", "0"},   // non-positive
		{"--timeout"},           // missing value
		{"--timeout", "abc"},    // invalid duration
		{"--timeout", "0"},      // non-positive
		{"--timeout", "-1s"},    // non-positive
		{"--bogus", "x"},        // unknown flag
	}
	for _, args := range cases {
		_, err := parseRunArgs(args)
		assert.Error(t, err, "args %v should error", args)
	}
}

func TestRunLoopTimeoutCancelsLLMRequest(t *testing.T) {
	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer server.Close()
	defer close(block)

	engine, err := agent.NewEngine(agent.EngineOpts{
		Model: llm.ModelConfig{
			Name:    "fake",
			BaseURL: server.URL,
			APIKey:  "test",
		},
		SessionOpts: agent.SessionOpts{Cwd: t.TempDir()},
		Gate:        permission.AllowAll{},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
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
