package editor

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/components/sessiontree"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/transcript"
	"github.com/pulseaiclub/phi/internal/util/clipboard"
)

func (e *Editor) showTree() {
	if e.ctrl == nil || e.ctrl.TreeBusy() {
		return
	}
	if e.extCmds != nil && e.extCmds.Running() {
		e.toast.Show("Wait for the extension command to finish before opening /tree", toast.ToastWarning, 3*time.Second)
		return
	}
	snap := e.ctrl.TreeSnapshot()
	settings := e.ctrl.TreeSettings()
	p := &e.composer.Tree
	selected := ""
	start := func(summarize bool, instructions string) {
		if e.extCmds != nil && e.extCmds.Running() {
			p.Status = "Wait for the extension command to finish"
			return
		}
		localDone := e.submitter.StopForTree()
		if e.ctrl.StartTreeNavigation(
			selected,
			agent.TreeOptions{Summarize: summarize, Instructions: instructions},
			localDone,
		) {
			p.Mode, p.Status = "wait", ""
			e.footer.Activity().Apply(controller.ActivityCompacting)
		}
	}
	p.OnAccept = func(item sessiontree.Item) {
		selected = item.ID
		plan, err := e.ctrl.TreeSnapshot().Plan(selected)
		if err != nil {
			p.Status = err.Error()
			return
		}
		if plan.Noop {
			p.Close()
			return
		}
		switch settings.Summary {
		case "always":
			start(true, "")
		case "never":
			start(false, "")
		default:
			p.Mode, p.Choice = "summary", 0
		}
	}
	p.OnChoice = start
	p.OnCancel = e.ctrl.CancelTreeNavigation
	p.OnLabel = func(item sessiontree.Item, label string) error {
		if err := e.ctrl.SetTreeLabel(item.ID, label); err != nil {
			return err
		}
		snap := e.ctrl.TreeSnapshot()
		p.Refresh(transcript.TreeItems(snap), snap.LeafID)
		return nil
	}
	p.OnCopy = func(text string) {
		if e.vx != nil && e.vx.CopyToClipboard(text) == nil {
			p.Status = "Copied"
			return
		}
		if err := clipboard.CopyText(text); err != nil {
			p.Status = "Copy failed: " + err.Error()
		} else {
			p.Status = "Copied"
		}
	}
	e.composer.ShowTree(transcript.TreeItems(snap), snap.LeafID, settings.Filter, settings.Keys)
}

func (e *Editor) applyTreeResult(msg controller.TreeResultMsg) {
	defer e.ctrl.FinishTreeNavigation()
	e.footer.ClearTokenDisplay()
	e.transcript.LoadReplay(msg.Snapshot)
	e.transcript.Sync()
	e.transcript.StickToBottom()
	e.footer.Activity().Apply(controller.ActivityIdle)
	e.footer.SyncFromSnap(msg.Snapshot)
	p := &e.composer.Tree
	if msg.Err != nil {
		p.Mode = "browse"
		p.Refresh(transcript.TreeItems(msg.Tree), msg.Tree.LeafID)
		p.Status = msg.Err.Error()
		if errors.Is(msg.Err, context.Canceled) {
			p.Status = "Navigation cancelled"
		}
		e.composer.RestoreTreeFocus()
		return
	}
	if strings.TrimSpace(e.composer.Chat.Value) == "" && msg.Plan.EditorText != "" {
		e.composer.SetInput(msg.Plan.EditorText)
	}
	p.Close()
	e.requestRedraw()
}
