package submit

import (
	"context"
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
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/transcript"
)

func shellRunner(t *testing.T) *BashRunner {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	th := components.DefaultTheme()
	pane := transcript.NewTranscriptPane(th, status.NewSpinner(th.ToolName), "Phi test")
	b := newBashRunner(pane, &stubComposer{}, controller.NewBus(nil))
	t.Cleanup(func() {
		b.Cancel()
		require.Eventually(t, func() bool { return !b.Running() }, 5*time.Second, time.Millisecond)
		// Shell execution must not create history.jsonl, history.lock, backups,
		// or a directory to hold them.
		files, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Empty(t, files, "shell execution must not persist history")
	})
	return b
}

func waitForShell(t *testing.T, b *BashRunner) session.ToolRun {
	t.Helper()
	require.Eventually(t, func() bool { return !b.Running() }, 5*time.Second, time.Millisecond)
	var results []session.ToolRun
	for _, msg := range b.bus.Drain() {
		switch msg := msg.(type) {
		case controller.SessionEventMsg:
			if data, ok := msg.Event.(session.ToolData); ok && data.Run.Status != session.ToolInProgress {
				results = append(results, data.Run)
			}
		case controller.ToastMsg:
			assert.Fail(t, "unexpected shell toast", "%s", msg.Message)
		}
	}
	require.Len(t, results, 1, "expected one completed shell result")
	assert.True(t, results[0].Local)
	return results[0]
}

func TestBashCompletedCommands(t *testing.T) {
	for _, tc := range []struct {
		command string
		output  string
		exit    int
		status  session.ToolStatus
	}{
		{"printf success", "success", 0, session.ToolDone},
		{"printf failure; exit 7", "failure", 7, session.ToolError},
		{"true", "(no output)", 0, session.ToolDone},
	} {
		t.Run(tc.command, func(t *testing.T) {
			b := shellRunner(t)
			require.True(t, b.HandleSubmit("! "+tc.command))
			result := waitForShell(t, b)
			assert.Equal(t, tc.status, result.Status)
			assert.Equal(t, tc.exit, result.ExitCode)
			assert.Equal(t, tc.command, result.Detail)
			assert.Equal(t, tc.output, result.Output)
			assert.False(t, b.Cancel())
		})
	}
}

type acceptingComposer struct {
	stubComposer
	onAccept func()
}

func (c *acceptingComposer) HideCompleters() { c.onAccept() }

func TestBashImmediateCancelAndBusyRefusal(t *testing.T) {
	b := shellRunner(t)
	b.composer = &acceptingComposer{onAccept: func() {
		// This executes before the run goroutine starts.
		assert.True(t, b.Running())
		assert.True(t, b.HandleSubmit("!printf refused"))
		messages := b.bus.Drain()
		require.Len(t, messages, 1)
		warning, ok := messages[0].(controller.ToastMsg)
		require.True(t, ok)
		assert.Contains(t, warning.Message, "already running")
		assert.True(t, b.Cancel())
	}}
	require.True(t, b.HandleSubmit("!sleep 30"))
	result := waitForShell(t, b)
	assert.Equal(t, session.ToolCancelled, result.Status)
	assert.Equal(t, "sleep 30", result.Detail)
	assert.NotContains(t, result.Output, "refused")
	assert.False(t, b.Cancel())
}

func TestBashRefusedInput(t *testing.T) {
	b := shellRunner(t)
	assert.False(t, b.HandleSubmit("hello"))
	assert.False(t, b.HandleSubmit("!  "))
	assert.Empty(t, b.bus.Drain())
	b.transcript.ApplySession(session.AssistantMessageUpdate{Message: session.Message{
		ID: "assistant", State: session.StateStreaming,
	}})
	assert.True(t, b.HandleSubmit("!printf refused"))
	assert.False(t, b.Running())
	messages := b.bus.Drain()
	require.Len(t, messages, 1)
	warning, ok := messages[0].(controller.ToastMsg)
	require.True(t, ok)
	assert.Contains(t, warning.Message, "while agent is active")
}

func TestBashLaunchFailure(t *testing.T) {
	b := shellRunner(t)
	cwd := filepath.Join(t.TempDir(), "missing")
	ctx, cancel := context.WithCancel(tooldef.WithCwd(t.Context(), cwd))
	b.cancel = cancel
	b.running.Store(true)
	b.run(ctx, "failed-launch", "printf never")
	result := waitForShell(t, b)
	assert.Equal(t, session.ToolError, result.Status)
	assert.Equal(t, "printf never", result.Detail)
	assert.NotEmpty(t, result.Error)
	assert.Empty(t, result.Output)
	assert.False(t, b.Cancel())
}

func TestBashUsesSubmissionCwd(t *testing.T) {
	b := shellRunner(t)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	cwd, err = filepath.EvalSymlinks(cwd)
	require.NoError(t, err)
	other := t.TempDir()
	b.composer = &acceptingComposer{onAccept: func() {
		// Change cwd after submission captures it, before execution starts.
		t.Chdir(other)
	}}
	require.True(t, b.HandleSubmit("!pwd"))
	result := waitForShell(t, b)
	assert.Equal(t, session.ToolDone, result.Status)
	actual, err := filepath.EvalSymlinks(strings.TrimSpace(result.Output))
	require.NoError(t, err)
	assert.Equal(t, cwd, actual)
}
