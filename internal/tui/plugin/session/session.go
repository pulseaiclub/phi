// Package session owns the session-event projection into the transcript:
// agent stream events, nested sub-agent progress, and the loop lifecycle
// messages. The batch postlude order (sync → activity → status → stick) is
// registered by the composition root, keeping the shell's drain semantics.
package session

import (
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/pathutil"
	"github.com/pulseaiclub/phi/internal/tui/runtime"
)

// Store is the transcript projection the plugin writes to.
type Store interface {
	ApplySession(ev session.Event)
	ApplyJobProgress(progress job.Progress) bool
	Sync()
	StickToBottom()
	AtBottom() bool
}

// LoopPort is the loop-generation guard the plugin needs.
type LoopPort interface {
	CurrentLoop(gen int) bool
}

// ActivitySink is the activity surface loop transitions drive.
type ActivitySink interface {
	Apply(controller.Activity)
}

// ErrorAsker is the overlay surface loop failures drive.
type ErrorAsker interface {
	ResolveError(controller.ErrorReply)
	Apply(controller.OverlayMsg)
}

// Deps carries the collaborators the root wires as method values.
type Deps struct {
	Store    Store
	Loop     LoopPort
	Activity ActivitySink
	Errors   ErrorAsker
	// Bus publishes the branch label; the composer plugin consumes it.
	Bus *controller.Bus
}

// Plugin subscribes the session-side messages and keeps the batch state
// (at-bottom capture, agent-event mark) for the postludes the root registers.
type Plugin struct {
	deps     Deps
	rt       *runtime.Runtime
	atBottom bool
}

// New builds the session plugin.
func New(deps Deps) *Plugin { return &Plugin{deps: deps} }

// Register wires the msg subscriptions. The root registers CaptureBatch and
// the PostSync/PostStick postludes so the batch order stays explicit.
func (p *Plugin) Register(rt *runtime.Runtime) {
	p.rt = rt
	runtime.Subscribe(rt, p.onSessionEvent)
	runtime.Subscribe(rt, p.onJobProgress)
	runtime.Subscribe(rt, p.onLoopStarted)
	runtime.Subscribe(rt, p.onLoopFinished)
}

// WatchBranch hot-reloads the git branch in the path label when the repo
// HEAD changes (checkout from another terminal, editor, …).
func (p *Plugin) WatchBranch(cwd string) {
	pathutil.WatchBranch(cwd, func(label string) {
		if p.deps.Bus != nil {
			p.deps.Bus.Publish(controller.BranchLabelMsg{Text: label})
		}
	})
}

// CaptureBatch snapshots the scroll position before the batch applies.
func (p *Plugin) CaptureBatch() { p.atBottom = p.deps.Store.AtBottom() }

// PostSync projects the applied batch into widgets.
func (p *Plugin) PostSync() { p.deps.Store.Sync() }

// PostStick restores bottom-pinning after the postludes that may grow rows.
func (p *Plugin) PostStick() {
	if p.atBottom {
		p.deps.Store.StickToBottom()
	}
}

func (p *Plugin) onSessionEvent(msg controller.SessionEventMsg) {
	if msg.Gen != 0 && !p.deps.Loop.CurrentLoop(msg.Gen) {
		return
	}
	p.deps.Store.ApplySession(msg.Event)
	p.rt.MarkBatchEvent()
}

func (p *Plugin) onJobProgress(msg controller.JobProgressMsg) {
	if p.deps.Store.ApplyJobProgress(msg.Progress) {
		p.rt.MarkBatchEvent()
	}
}

func (p *Plugin) onLoopStarted(msg controller.LoopStartedMsg) {
	if !p.deps.Loop.CurrentLoop(msg.Gen) {
		return
	}
	p.deps.Errors.ResolveError(controller.ErrorReply{})
	p.deps.Activity.Apply(controller.ActivityWaiting)
}

func (p *Plugin) onLoopFinished(msg controller.LoopFinishedMsg) {
	if !p.deps.Loop.CurrentLoop(msg.Gen) {
		return
	}
	p.deps.Store.ApplySession(session.CancelStreaming{})
	p.deps.Store.Sync()
	p.deps.Activity.Apply(controller.ActivityIdle)
	if msg.Cancelled {
		p.deps.Activity.Apply(controller.ActivityCancelled)
	}
	if msg.ErrorText != "" {
		p.deps.Errors.Apply(controller.OverlayMsg{Kind: controller.OverlayErrorAsk, ErrorText: msg.ErrorText})
	}
}
