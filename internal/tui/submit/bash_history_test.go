package submit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/status"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/session/shellhist"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/transcript"
)

func historyRunner(t *testing.T, dir string) *BashRunner {
	t.Helper()
	th := components.DefaultTheme()
	pane := transcript.NewTranscriptPane(th, status.NewSpinner(th.ToolName), "Phi test")
	b := newBashRunner(pane, &stubComposer{}, controller.NewBus(nil), shellhist.New(dir), nil)
	t.Cleanup(func() {
		b.Cancel()
		require.Eventually(t, func() bool { return !b.Running() }, 5*time.Second, time.Millisecond)
	})
	return b
}

func recordedEntries(t *testing.T, dir string) []shellhist.Entry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "history.jsonl"))
	require.NoError(t, err)
	var entries []shellhist.Entry
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var entry shellhist.Entry
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		entries = append(entries, entry)
	}
	return entries
}

func waitForShell(t *testing.T, b *BashRunner) session.ToolRun {
	t.Helper()
	require.Eventually(t, func() bool { return !b.Running() }, 5*time.Second, time.Millisecond)
	for _, msg := range b.bus.Drain() {
		if ev, ok := msg.(controller.SessionEventMsg); ok {
			if data, ok := ev.Event.(session.ToolData); ok && data.Run.Status != session.ToolInProgress {
				return data.Run
			}
		}
	}
	require.FailNow(t, "missing completed shell result")
	return session.ToolRun{}
}

func TestBashHistoryCompletedCommands(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	t.Chdir(cwd)
	actualCwd, err := os.Getwd()
	require.NoError(t, err)
	for _, tc := range []struct {
		command string
		exit    int
		status  session.ToolStatus
	}{
		{"printf success", 0, session.ToolDone},
		{"printf failure; exit 7", 7, session.ToolError},
	} {
		t.Run(tc.command, func(t *testing.T) {
			// New runners still append to the same project history across sessions.
			b := historyRunner(t, dir)
			before := time.Now().UnixMilli()
			require.True(t, b.HandleSubmit("! "+tc.command))
			accepted := time.Now().UnixMilli()
			result := waitForShell(t, b)
			assert.Equal(t, tc.status, result.Status)
			assert.Equal(t, tc.exit, result.ExitCode)
			entries := recordedEntries(t, dir)
			entry := entries[len(entries)-1]
			assert.Equal(t, 1, entry.Version)
			assert.Equal(t, actualCwd, entry.Cwd)
			assert.Equal(t, tc.command, entry.Command)
			require.NotNil(t, entry.Exit)
			assert.Equal(t, tc.exit, *entry.Exit)
			assert.GreaterOrEqual(t, entry.At, before)
			assert.LessOrEqual(t, entry.At, accepted)
		})
	}
	assert.Len(t, recordedEntries(t, dir), 2)
}

type acceptingComposer struct {
	stubComposer
	onAccept func()
}

func (c *acceptingComposer) HideCompleters() { c.onAccept() }

func TestBashHistoryImmediateCancelAndBusyRefusal(t *testing.T) {
	dir := t.TempDir()
	b := historyRunner(t, dir)
	b.composer = &acceptingComposer{onAccept: func() {
		// This executes before the run goroutine starts.
		assert.True(t, b.Running())
		assert.True(t, b.HandleSubmit("!printf refused"))
		assert.True(t, b.Cancel())
	}}
	require.True(t, b.HandleSubmit("!sleep 30"))
	result := waitForShell(t, b)
	assert.Equal(t, session.ToolCancelled, result.Status)
	entries := recordedEntries(t, dir)
	require.Len(t, entries, 1)
	assert.Equal(t, "sleep 30", entries[0].Command)
	assert.Nil(t, entries[0].Exit)
	assert.False(t, b.Cancel())
}

func TestBashHistoryRefusedInput(t *testing.T) {
	dir := t.TempDir()
	b := historyRunner(t, dir)
	assert.False(t, b.HandleSubmit("hello"))
	assert.False(t, b.HandleSubmit("!  "))
	b.transcript.ApplySession(session.AssistantMessageUpdate{Message: session.Message{
		ID: "assistant", State: session.StateStreaming,
	}})
	assert.True(t, b.HandleSubmit("!printf refused"))
	assert.False(t, b.Running())
	assert.NoFileExists(t, filepath.Join(dir, "history.jsonl"))
}

func TestBashHistoryLaunchFailure(t *testing.T) {
	dir := t.TempDir()
	b := historyRunner(t, dir)
	cwd := filepath.Join(t.TempDir(), "missing")
	ctx, cancel := context.WithCancel(tooldef.WithCwd(t.Context(), cwd))
	b.cancel = cancel
	b.running.Store(true)
	b.run(ctx, "failed-launch", shellhist.Entry{
		Version: 1, At: time.Now().UnixMilli(), Cwd: cwd, Command: "printf never",
	}, nil, false, "")
	result := waitForShell(t, b)
	assert.Equal(t, session.ToolError, result.Status)
	assert.NotEmpty(t, result.Error)
	entries := recordedEntries(t, dir)
	require.Len(t, entries, 1)
	assert.Nil(t, entries[0].Exit)
}

func TestBashHistoryWriteFailurePreservesResult(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "not-a-directory")
	require.NoError(t, os.WriteFile(blocked, []byte("occupied"), 0o600))
	b := historyRunner(t, blocked)
	require.True(t, b.HandleSubmit("!printf success"))
	require.Eventually(t, func() bool { return !b.Running() }, 5*time.Second, time.Millisecond)
	var sawToast, sawResult bool
	for _, msg := range b.bus.Drain() {
		switch msg := msg.(type) {
		case controller.ToastMsg:
			sawToast = true
			assert.Contains(t, msg.Message, "Shell history was not saved")
			assert.Contains(t, msg.Message, "session directory permissions")
		case controller.SessionEventMsg:
			if data, ok := msg.Event.(session.ToolData); ok && data.Run.Status == session.ToolDone {
				sawResult = true
				assert.Equal(t, "success", data.Run.Output)
				assert.Zero(t, data.Run.ExitCode)
			}
		}
	}
	assert.True(t, sawToast)
	assert.True(t, sawResult)
}
