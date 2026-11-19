// Package bash runs user "!cmd" shells locally (not via the agent) as a
// runtime plugin. It claims "!"-prefixed submits through the runtime's submit
// filter chain.
package bash

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
	"github.com/pulseaiclub/phi/internal/tools/bashtool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/runtime"
)

// SessionSink is the transcript surface the runner needs. Defined here so the
// plugin depends on a consumer-shaped port, not the transcript package.
type SessionSink interface {
	ApplySession(ev session.Event)
	Sync()
	StickToBottom()
	IsStreaming() bool
}

// EnginePort is the controller surface the runner needs.
type EnginePort interface {
	SessionID() string
	AppendUserBash(sessionID, command, output string, exitCode int, cancelled bool) error
}

// Input is the composer surface the runner needs.
type Input interface {
	HideCompleters()
	ClearInput()
	SyncBashBorder(text string)
	SetBashBorderActive(active bool)
}

// Plugin runs user "!cmd" shells and appends "!!cmd" output to the session.
type Plugin struct {
	transcript SessionSink
	composer   Input
	bus        *controller.Bus
	ctrl       EnginePort

	running atomic.Bool
	mu      sync.Mutex
	cancel  context.CancelFunc
}

// New builds the bash plugin. transcript, composer, and bus must be non-nil;
// ctrl may be nil where the process has no engine.
func New(transcript SessionSink, composer Input, bus *controller.Bus, ctrl EnginePort) *Plugin {
	return &Plugin{
		transcript: transcript,
		composer:   composer,
		bus:        bus,
		ctrl:       ctrl,
	}
}

// Register wires the plugin into the runtime.
func (p *Plugin) Register(rt *runtime.Runtime) {
	rt.AddSubmitFilter(p.HandleSubmit)
}

// Running reports whether a local bash command is in flight.
func (p *Plugin) Running() bool {
	return p.running.Load()
}

// HandleSubmit runs a user "!cmd". Returns true when the input was consumed.
func (p *Plugin) HandleSubmit(text string) bool {
	command, includeInContext, ok := parseBashInput(text)
	if !ok {
		return false
	}
	if p.transcript.IsStreaming() {
		p.showToast("Unable to use shell mode while agent is active", 3*time.Second)
		return true
	}
	p.mu.Lock()
	if p.running.Load() {
		p.mu.Unlock()
		p.showToast(
			"A bash command is already running. Press Esc to cancel it first.",
			3*time.Second,
		)
		return true
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.running.Store(true)
	p.mu.Unlock()
	started := time.Now()
	if cwd, err := os.Getwd(); err == nil {
		ctx = tooldef.WithCwd(ctx, cwd)
	}
	p.composer.HideCompleters()
	p.composer.ClearInput()
	p.SyncBorder("")

	id := fmt.Sprintf("bash-%d", started.UnixNano())
	p.transcript.ApplySession(session.LocalBashStart{ID: id, Command: command, InContext: includeInContext})
	p.transcript.Sync()
	p.transcript.StickToBottom()

	// Capture the session now: a switch while the command runs must not route
	// its output into whichever session is open when it finishes.
	sessionID := ""
	if p.ctrl != nil {
		sessionID = p.ctrl.SessionID()
	}
	go p.run(ctx, id, command, includeInContext, sessionID)
	return true
}

// parseBashInput splits a "!cmd" / "!!cmd" line. "!" runs a local shell; "!!"
// also feeds the finished output into the model context.
func parseBashInput(text string) (command string, includeInContext, ok bool) {
	if !strings.HasPrefix(text, "!") {
		return "", false, false
	}
	includeInContext = strings.HasPrefix(text, "!!")
	start := 1
	if includeInContext {
		start = 2
	}
	command = strings.TrimSpace(text[start:])
	return command, includeInContext, command != ""
}

func (p *Plugin) run(ctx context.Context, id, command string, includeInContext bool, sessionID string) {
	defer func() {
		p.mu.Lock()
		p.cancel()
		p.cancel = nil
		p.running.Store(false)
		p.mu.Unlock()
	}()
	const bashPublishInterval = 100 * time.Millisecond

	liveOutput := newBashLiveOutput(bashPublishInterval, func(cur string) {
		p.publishSession(session.ToolData{Run: session.ToolRun{
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

	if err != nil {
		p.publishSession(session.ToolData{Run: session.ToolRun{
			ToolUseID: id,
			Name:      "bash",
			Status:    session.ToolError,
			Detail:    command,
			Output:    result.Output,
			Error:     err.Error(),
			Local:     true,
			InContext: includeInContext,
		}})
		p.recordContext(sessionID, includeInContext, command, result.Output, -1, false)
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
	p.publishSession(session.ToolData{Run: session.ToolRun{
		ToolUseID: id,
		Name:      "bash",
		Status:    status,
		Detail:    command,
		Output:    outText,
		ExitCode:  result.ExitCode,
		Local:     true,
		InContext: includeInContext,
	}})
	p.recordContext(sessionID, includeInContext, command, outText, result.ExitCode, result.Canceled)
}

// recordContext appends a "!!cmd" result to the active session so the next
// turn sees it. Best effort: the command already ran, so a persistence failure
// is reported as a toast rather than rewinding the UI.
func (p *Plugin) recordContext(
	sessionID string,
	includeInContext bool,
	command, output string,
	exitCode int,
	cancelled bool,
) {
	if !includeInContext || p.ctrl == nil {
		return
	}
	// Appending mid-turn would break tool_use/tool_result pairing, so the output
	// is dropped rather than corrupting the conversation.
	if p.transcript.IsStreaming() {
		p.showToast(
			"Shell output was not added to the conversation because the agent is busy",
			6*time.Second,
		)
		return
	}
	if err := p.ctrl.AppendUserBash(sessionID, command, output, exitCode, cancelled); err != nil {
		p.showToast(
			fmt.Sprintf("Shell output was not added to the conversation: %v", err),
			6*time.Second,
		)
	}
}

func (p *Plugin) publishSession(ev session.Event) {
	p.bus.Publish(controller.SessionEventMsg{Event: ev})
}

func (p *Plugin) showToast(msg string, d time.Duration) {
	p.bus.Publish(controller.ToastMsg{Message: msg, Kind: toast.ToastWarning, Duration: d})
}

// Cancel aborts a running user "!cmd". Returns true if one was cancelled.
func (p *Plugin) Cancel() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel == nil {
		return false
	}
	p.cancel()
	return true
}

// SyncBorder paints the composer border for bash mode when text starts with "!".
func (p *Plugin) SyncBorder(text string) {
	if p.composer == nil {
		return
	}
	bash := strings.HasPrefix(strings.TrimLeft(text, " \t"), "!")
	p.composer.SetBashBorderActive(bash)
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
