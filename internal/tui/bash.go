package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools"
)

// handleBashSubmit runs a user "!cmd" shell locally (not via the agent).
// Returns true when the input was consumed as a bash command.
func (editor *Editor) handleBashSubmit(text string) bool {
	if !strings.HasPrefix(text, "!") {
		return false
	}
	command := strings.TrimSpace(text[1:])
	if command == "" {
		return false
	}
	if session.IsStreaming(editor.snap) {
		editor.toast.Show("Unable to use shell mode while agent is active", toast.ToastWarning, 3*time.Second)
		return true
	}
	if editor.bashRunning.Load() {
		editor.toast.Show("A bash command is already running. Press Esc to cancel it first.", toast.ToastWarning, 3*time.Second)
		return true
	}

	editor.hideCompleters()
	editor.Chat.Value = ""
	editor.Chat.Cursor = 0
	editor.syncBashModeBorder("")

	id := fmt.Sprintf("bash-%d", time.Now().UnixNano())
	editor.applySessionEvent(session.LocalBashStart{ID: id, Command: command})
	editor.syncThread()
	editor.list.StickToBottom()

	go editor.runBash(id, command)
	return true
}

func (editor *Editor) runBash(id, command string) {
	editor.bashMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	editor.bashCancel = cancel
	editor.bashMu.Unlock()
	editor.bashRunning.Store(true)
	defer func() {
		editor.bashRunning.Store(false)
		editor.bashMu.Lock()
		editor.bashCancel = nil
		editor.bashMu.Unlock()
	}()

	// Live updates publish at most this often and carry only a display-sized
	// tail. This keeps both the event payload and BashBlock layout bounded while
	// the final event still carries the formatted command result.
	const bashPublishInterval = 100 * time.Millisecond

	liveOutput := newBashLiveOutput(bashPublishInterval, func(cur string) {
		editor.Publish(SessionEventMsg{Event: session.ToolData{Run: session.ToolRun{
			ToolUseID: id,
			Name:      "bash",
			Status:    session.ToolInProgress,
			Detail:    command,
			Output:    cur,
			Local:     true,
		}}})
	})

	result, err := tools.ExecShell(ctx, command, tools.ShellExecOptions{
		OnChunk: liveOutput.Append,
	})
	// Cmd.Run waits for all writer callbacks, so no Append can race with Close.
	// Closing before the final event prevents a trailing in-progress update from
	// replacing the completed state.
	liveOutput.Close()
	if err != nil {
		editor.Publish(SessionEventMsg{Event: session.ToolData{Run: session.ToolRun{
			ToolUseID: id,
			Name:      "bash",
			Status:    session.ToolError,
			Detail:    command,
			Output:    result.Output,
			Error:     err.Error(),
			Local:     true,
		}}})
		return
	}
	status := session.ToolDone
	if result.Canceled {
		status = session.ToolCancelled
	} else if result.ExitCode != 0 {
		status = session.ToolError
	}
	outText := result.Output
	if strings.TrimSpace(outText) == "" && !result.Canceled {
		outText = "(no output)"
	}
	editor.Publish(SessionEventMsg{Event: session.ToolData{Run: session.ToolRun{
		ToolUseID: id,
		Name:      "bash",
		Status:    status,
		Detail:    command,
		Output:    outText,
		ExitCode:  result.ExitCode,
		Local:     true,
	}}})
}

// bashLiveOutput publishes a bounded live tail immediately, then at most once
// per interval. A skipped update always schedules one trailing publication.
type bashLiveOutput struct {
	mu          sync.Mutex
	tail        *tools.BashOutputTail
	interval    time.Duration
	lastPublish time.Time
	timer       *time.Timer
	stopped     bool
	publish     func(output string)
}

func newBashLiveOutput(interval time.Duration, publish func(output string)) *bashLiveOutput {
	return &bashLiveOutput{
		tail:     tools.NewBashOutputTail(tools.BashMaxOutputLines, tools.BashMaxOutputBytes),
		interval: interval,
		publish:  publish,
	}
}

func (o *bashLiveOutput) Append(chunk string) {
	_, _ = o.tail.WriteString(chunk)

	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stopped || o.timer != nil {
		return
	}
	now := time.Now()
	if o.lastPublish.IsZero() || now.Sub(o.lastPublish) >= o.interval {
		o.publishLocked(now)
		return
	}
	delay := o.interval - now.Sub(o.lastPublish)
	o.timer = time.AfterFunc(delay, o.publishTrailing)
}

func (o *bashLiveOutput) publishTrailing() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.timer = nil
	if o.stopped {
		return
	}
	o.publishLocked(time.Now())
}

func (o *bashLiveOutput) publishLocked(now time.Time) {
	cur, truncated := o.tail.Snapshot()
	if truncated {
		cur = "[live output truncated; showing latest output]\n" + cur
	}
	o.lastPublish = now
	if o.publish != nil {
		o.publish(cur)
	}
}

// Close synchronizes with any active timer callback and prevents future
// in-progress publications.
func (o *bashLiveOutput) Close() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stopped = true
	if o.timer != nil {
		o.timer.Stop()
		o.timer = nil
	}
}

// cancelBash aborts a running user "!cmd". Returns true if one was cancelled.
func (editor *Editor) cancelBash() bool {
	if !editor.bashRunning.Load() {
		return false
	}
	editor.bashMu.Lock()
	cancel := editor.bashCancel
	editor.bashMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return true
}

func (editor *Editor) syncBashModeBorder(text string) {
	bash := strings.HasPrefix(strings.TrimLeft(text, " \t"), "!")
	if bash {
		editor.Chat.BorderStyle = editor.theme.ToolName
	} else {
		editor.Chat.BorderStyle = editor.theme.Border
	}
}
