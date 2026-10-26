package submit

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/session/shellhist"
	"github.com/pulseaiclub/phi/internal/tools/bashtool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
	"github.com/pulseaiclub/phi/internal/tui/composer"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/transcript"
)

// BashRunner runs user "!cmd" shells locally (not via the agent).
type BashRunner struct {
	transcript *transcript.TranscriptPane
	composer   composer.Input
	bus        *controller.Bus
	history    *shellhist.Store

	running atomic.Bool
	mu      sync.Mutex
	cancel  context.CancelFunc
}

func newBashRunner(
	transcript *transcript.TranscriptPane,
	composer composer.Input,
	bus *controller.Bus,
	history *shellhist.Store,
) *BashRunner {
	return &BashRunner{
		transcript: transcript,
		composer:   composer,
		bus:        bus,
		history:    history,
	}
}

// Running reports whether a local bash command is in flight.
func (b *BashRunner) Running() bool {
	return b != nil && b.running.Load()
}

// HandleSubmit runs a user "!cmd". Returns true when the input was consumed.
func (b *BashRunner) HandleSubmit(text string) bool {
	if b == nil || !strings.HasPrefix(text, "!") {
		return false
	}
	return b.start(strings.TrimSpace(text[1:]))
}

// HandleShellSubmit runs the bare text typed in composer shell mode: the mode
// already says "this is a command", so there is no prefix to strip.
func (b *BashRunner) HandleShellSubmit(text string) bool {
	if b == nil {
		return false
	}
	return b.start(strings.TrimSpace(text))
}

func (b *BashRunner) start(command string) bool {
	if command == "" {
		return false
	}
	if b.transcript != nil && b.transcript.IsStreaming() {
		b.showToast("Unable to use shell mode while agent is active", toast.ToastWarning, 3*time.Second)
		return true
	}
	b.mu.Lock()
	if b.running.Load() {
		b.mu.Unlock()
		b.showToast(
			"A bash command is already running. Press Esc to cancel it first.",
			toast.ToastWarning,
			3*time.Second,
		)
		return true
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.running.Store(true)
	b.mu.Unlock()
	started := time.Now()
	cwd, cwdErr := os.Getwd()
	entry := shellhist.Entry{Version: 1, At: started.UnixMilli(), Cwd: cwd, Command: command}
	if cwdErr == nil {
		ctx = tooldef.WithCwd(ctx, cwd)
	}
	b.composer.HideCompleters()
	b.composer.ClearInput()
	// Through the pane: shell mode keeps its border after the clear, a one-shot
	// "!cmd" drops it with the prefix that set it.
	b.composer.SyncBashBorder("")

	id := fmt.Sprintf("bash-%d", started.UnixNano())
	b.transcript.ApplySession(session.LocalBashStart{ID: id, Command: command})
	b.transcript.Sync()
	b.transcript.StickToBottom()

	go b.run(ctx, id, entry, cwdErr)
	return true
}

func (b *BashRunner) run(ctx context.Context, id string, entry shellhist.Entry, cwdErr error) {
	defer func() {
		b.mu.Lock()
		b.cancel()
		b.cancel = nil
		b.running.Store(false)
		b.mu.Unlock()
	}()
	command := entry.Command
	const bashPublishInterval = 100 * time.Millisecond

	liveOutput := newBashLiveOutput(bashPublishInterval, func(cur string) {
		b.publishSession(session.ToolData{Run: session.ToolRun{
			ToolUseID: id,
			Name:      "bash",
			Status:    session.ToolInProgress,
			Detail:    command,
			Output:    cur,
			Local:     true,
		}})
	})

	result, err := bashtool.ExecShell(ctx, command, bashtool.ShellExecOptions{
		OnChunk: liveOutput.Append,
	})
	liveOutput.Close()
	if b.history != nil {
		if err == nil && !result.Canceled && result.ExitCode >= 0 {
			entry.Exit = &result.ExitCode
		}
		historyErr := cwdErr
		if historyErr == nil {
			historyErr = b.history.Append(entry)
		}
		if historyErr != nil {
			b.showToast(
				fmt.Sprintf(
					"Shell history was not saved: %v. Check the working directory and session directory permissions.",
					historyErr,
				),
				toast.ToastWarning,
				6*time.Second,
			)
		}
	}
	if err != nil {
		b.publishSession(session.ToolData{Run: session.ToolRun{
			ToolUseID: id,
			Name:      "bash",
			Status:    session.ToolError,
			Detail:    command,
			Output:    result.Output,
			Error:     err.Error(),
			Local:     true,
		}})
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
	b.publishSession(session.ToolData{Run: session.ToolRun{
		ToolUseID: id,
		Name:      "bash",
		Status:    status,
		Detail:    command,
		Output:    outText,
		ExitCode:  result.ExitCode,
		Local:     true,
	}})
}

func (b *BashRunner) publishSession(ev session.Event) {
	if b == nil {
		return
	}
	b.bus.Publish(controller.SessionEventMsg{Event: ev})
}

func (b *BashRunner) showToast(msg string, kind toast.ToastKind, d time.Duration) {
	if b == nil {
		return
	}
	b.bus.Publish(controller.ToastMsg{Message: msg, Kind: kind, Duration: d})
}

// Cancel aborts a running user "!cmd". Returns true if one was cancelled.
func (b *BashRunner) Cancel() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancel == nil {
		return false
	}
	b.cancel()
	return true
}

// SyncBorder paints the composer border for bash mode when text starts with "!".
func (b *BashRunner) SyncBorder(text string) {
	if b == nil || b.composer == nil {
		return
	}
	bash := strings.HasPrefix(strings.TrimLeft(text, " \t"), "!")
	b.composer.SetBashBorderActive(bash)
}

// bashLiveOutput publishes a bounded live tail immediately, then at most once
// per interval. A skipped update always schedules one trailing publication.
type bashLiveOutput struct {
	mu          sync.Mutex
	tail        *bashtool.BashOutputTail
	interval    time.Duration
	lastPublish time.Time
	timer       *time.Timer
	stopped     bool
	publish     func(output string)
}

func newBashLiveOutput(interval time.Duration, publish func(output string)) *bashLiveOutput {
	return &bashLiveOutput{
		tail:     bashtool.NewBashOutputTail(bashtool.BashMaxOutputLines, bashtool.BashMaxOutputBytes),
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
