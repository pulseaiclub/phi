// Package editor wires the TUI root widget and assembles domain panes.
package editor

import (
	"time"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/app"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/composer"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/footer"
	"github.com/pulseaiclub/phi/internal/tui/overlays"
	"github.com/pulseaiclub/phi/internal/tui/pathutil"
	"github.com/pulseaiclub/phi/internal/tui/submit"
	"github.com/pulseaiclub/phi/internal/tui/transcript"
	"github.com/pulseaiclub/phi/internal/util/update"
	"github.com/pulseaiclub/phi/internal/version"
)

// Editor is the TUI root widget: layout composition and the UI-goroutine
// message loop. Cross-component work goes through controller.Bus — producers Publish,
// Draw drains and Update applies. Agent lifecycle lives in controller.EngineController;
// session→widget projection lives in TranscriptPane (Mapper/SubagentStore).
//
// Construction: cmd assembles App, controller.Bus, controller.EngineController and passes
// them into NewEditor, which builds the CommandRegistry (commands.NewBuiltinRegistry).
// Editor does not create controller.EngineController or fetch the project singleton.
type Editor struct {
	vx    *xui.XUI
	App   *app.App
	theme components.Theme
	bus   *controller.Bus
	cwd   string

	transcript *transcript.TranscriptPane
	composer   *composer.ComposerPane
	footer     *footer.FooterChrome
	overlays   *overlays.Overlays
	toast      toast.Toast

	ctrl *controller.EngineController

	commands   *commands.CommandRegistry
	modelNames []string
	skillPath  string

	sessions  *commands.SessionCommands
	extCmds   *commands.ExtCommands
	submitter *submit.Submitter
}

// NewEditor builds the TUI panes and wires injected collaborators.
// application, bus, and ctrl must be non-nil.
func NewEditor(
	application *app.App,
	bus *controller.Bus,
	ctrl *controller.EngineController,
	vx *xui.XUI,
	theme components.Theme,
	cwd, model, skillPath string,
	contextWindow int,
	modelNames []string,
) *Editor {
	registry := commands.NewBuiltinRegistry()
	e := &Editor{
		vx:         vx,
		App:        application,
		theme:      theme,
		cwd:        cwd,
		bus:        bus,
		ctrl:       ctrl,
		modelNames: append([]string(nil), modelNames...),
		skillPath:  skillPath,
		commands:   registry,
		toast:      toast.Toast{Theme: theme},
		composer:   composer.NewComposerPane(theme, model, cwd),
		footer:     footer.NewFooterChrome(theme, contextWindow),
	}
	e.transcript = transcript.NewTranscriptPane(theme, e.footer.Spinner(), "Phi "+version.Version)
	e.transcript.SetThinkingExpanded(ctrl.ThinkingExpanded())
	e.transcript.SetUsageCallback(e.footer.UpdateTokenDisplay)
	e.footer.BindComposer(e.composer)
	e.footer.SetLabelContext(e.transcript.Snapshot)
	e.footer.SetLiveJobs(func() int {
		if e.ctrl != nil {
			return e.ctrl.LiveJobCount()
		}
		return 0
	})
	e.overlays = overlays.NewOverlays(
		theme,
		e.footer.Activity(),
		e.composer,
		func() {
			if e.App != nil {
				e.App.RequestFocus(e)
			}
		},
		func() {
			if e.App != nil {
				e.composer.RestoreTreeFocus()
			}
		},
	)
	e.transcript.SetCopyHandlers(
		e.bus,
		func(text string) bool {
			return e.vx != nil && e.vx.CopyToClipboard(text) == nil
		},
	)
	e.extCmds = &commands.ExtCommands{
		Registry: e.commands,
		Ctrl:     e.ctrl,
		Composer: e.composer,
		Footer:   e.footer,
		Bus:      e.bus,
	}
	e.sessions = commands.NewSessionCommands(
		e.ctrl,
		e.transcript,
		e.footer,
		e.bus,
		e.extCmds.Sync,
	)

	var bridge *commandBridge
	e.submitter = submit.NewSubmitter(
		e.ctrl,
		e.commands,
		e.transcript,
		e.footer.Activity(),
		e.composer,
		e.bus,
		func() commands.CommandContext {
			if bridge == nil {
				return commands.CommandContext{}
			}
			return bridge.context()
		},
		e.overlays.PermissionActive,
		e.overlays.ContinueActive,
		e.overlays.ConfirmActive,
		e.overlays.ResolvePermission,
		e.overlays.ResolveContinue,
		e.overlays.ResolveConfirm,
	)
	e.extCmds.Submitter = e.submitter
	e.sessions.OpenPicker = e.composer.ShowSessionList
	e.sessions.StreamActive = e.submitter.StreamActive
	e.composer.SetListPickHandler(func(item listpicker.Item) {
		e.sessions.Accept(item.ID)
	})
	bridge = newCommandBridge(
		e.bus,
		e.composer,
		e.ctrl,
		e.submitter,
		e.sessions,
		e.extCmds,
		e.modelNames,
		e.skillPath,
	)
	e.extCmds.CommandCtx = bridge.context
	e.composer.Wire(
		e.transcript,
		e.submitter,
		e.commands,
		e.cwd,
		e.bus,
		e.drainBus,
		func() {
			if e.vx != nil {
				e.vx.QueueRefresh()
			}
		},
		func() bool { return e.ctrl != nil && e.ctrl.ImageEnabled() },
		e.overlays.BlocksComposer,
		e.overlays.HandlePermissionKey,
		e.overlays.HandleContinueKey,
		e.overlays.HandleConfirmKey,
		e.handleCopyKey,
		func() {
			if e.App != nil {
				e.App.RequestFocus(e)
			}
		},
		func(w components.Widget) {
			if e.App != nil {
				e.App.RequestFocus(w)
			}
		},
		func() {
			if e.ctrl != nil {
				e.ctrl.Close()
			}
		},
	)

	e.extCmds.Sync()
	return e
}

// Publish sends a message onto the bus from any goroutine / widget callback.
func (e *Editor) Publish(m controller.Msg) {
	if e.bus == nil {
		return
	}
	e.bus.Publish(m)
}

// Update applies one message on the UI goroutine.
func (e *Editor) Update(m controller.Msg) {
	switch msg := m.(type) {
	case controller.SubmitMsg:
		e.submitter.Submit(msg.Text)
	case controller.CancelStreamMsg:
		if e.ctrl != nil && e.ctrl.TreeBusy() {
			e.ctrl.CancelTreeNavigation()
		} else {
			e.submitter.Cancel()
		}
	case controller.TreeOpenMsg:
		e.showTree()
	case controller.TreeResultMsg:
		e.applyTreeResult(msg)
	case controller.MentionResultsMsg:
		e.composer.ApplyMentionResults(msg)
	case controller.OverlayMsg:
		e.overlays.Apply(msg)
	case controller.FooterMsg:
		if msg.Gen != 0 && e.ctrl != nil && !e.ctrl.Alive(msg.Gen) {
			return
		}
		e.footer.Apply(msg)
	case controller.ToastMsg:
		e.toast.Show(msg.Message, msg.Kind, msg.Duration)
	case controller.ThemeMsg:
		e.applyTheme(msg.Name)
	case controller.ThinkingExpandedMsg:
		e.setThinkingExpanded(msg.Expanded)
	case controller.ExtSessionEffectsMsg:
		e.footer.ApplySessionEffects(msg)
		if msg.Toast != "" {
			e.toast.Show(msg.Toast, toast.ToastSuccess, 3*time.Second)
		}
	case controller.BranchLabelMsg:
		e.composer.SetBranchLabel(msg.Text)
		if e.vx != nil {
			e.vx.QueueRefresh()
		}
	case controller.ExtCommandResultMsg:
		if e.extCmds != nil {
			e.extCmds.Apply(msg)
		}
	case controller.JobProgressMsg:
		// Applied in drainBus so we can skip Sync when the tree is unchanged.
	case controller.RedrawMsg:
		// no state change; drain already requested redraw
	}
}

func (e *Editor) drainBus() {
	batch := e.bus.Drain()
	if len(batch) == 0 {
		return
	}
	atBottom := e.transcript.AtBottom()
	agentEvent := false
	for _, m := range batch {
		switch msg := m.(type) {
		case controller.SessionEventMsg:
			if msg.Gen != 0 && e.ctrl != nil && !e.ctrl.Alive(msg.Gen) {
				continue
			}
			agentEvent = true
			e.transcript.ApplySession(msg.Event)
		case controller.JobProgressMsg:
			if e.transcript.ApplyJobProgress(msg.Progress) {
				agentEvent = true
			}
		default:
			e.Update(m)
		}
	}
	if agentEvent {
		e.transcript.Sync()
		e.footer.SyncFromSnap(e.transcript.Snapshot())
		if atBottom {
			e.transcript.StickToBottom()
		}
	}
}

func (e *Editor) Handle(ctx *components.EventContext, ev xui.Event) {
	e.composer.Handle(ctx, ev)
}

func (e *Editor) handleCopyKey(ctx *components.EventContext, ke xui.KeyEvent) bool {
	return e.transcript.HandleCopyKey(ctx, ke)
}

// Draw renders the editor surface for the given draw context.
func (e *Editor) Draw(ctx components.DrawContext) components.Surface {
	e.drainBus()

	if e.footer != nil {
		e.footer.AdvanceTick()
	}
	_ = e.toast.Visible()

	maxSize := ctx.Max
	root := components.Surface{Size: maxSize, Widget: e}

	footerH := 1
	var chatH int
	if askH, overlay := e.overlays.PreferredBottomHeight(maxSize.Width, ctx.Method); overlay {
		chatH = askH
		maxChatH := maxSize.Height - footerH - 3
		if chatH > maxChatH {
			chatH = maxChatH
		}
		if chatH < 8 {
			chatH = 8
		}
	} else {
		chatH = e.composer.PreferredHeight(maxSize.Width, ctx.Method)
		minChatH := 5
		if len(e.composer.Chat.PendingSkills) > 0 {
			minChatH++
		}
		if chatH < minChatH {
			chatH = minChatH
		}
		maxChatH := maxSize.Height - footerH - 3
		maxChatH = max(maxChatH, minChatH)
		if chatH > maxChatH {
			chatH = maxChatH
		}
	}
	listH := maxSize.Height - chatH - footerH
	if listH < 3 {
		listH = 3
		chatH = maxSize.Height - listH - footerH
		chatH = max(chatH, 5)
	}
	if e.composer.Tree.Open {
		footerH = min(1, max(maxSize.Height, 0))
		chatH = min(chatH, max(maxSize.Height-footerH, 0))
		listH = max(0, maxSize.Height-chatH-footerH)
	}

	var listSurf components.Surface
	if listH > 0 {
		listSurf = e.transcript.Draw(ctx, maxSize.Width, listH)
		listH = e.transcript.ListHeight()
	}

	var chatSurf components.Surface
	if surf, ok := e.overlays.DrawBottom(ctx, maxSize.Width, chatH); ok {
		chatSurf = surf
	} else {
		chatSurf = e.composer.DrawChat(ctx, maxSize.Width, chatH)
	}
	footerSurf := e.footer.Draw(ctx, maxSize.Width)

	root.Children = []components.SubSurface{
		{Origin: components.Point{X: 0, Y: 0}, Surface: listSurf},
		{Origin: components.Point{X: 0, Y: listH}, Surface: chatSurf, Z: 1},
		{Origin: components.Point{X: 0, Y: maxSize.Height - footerH}, Surface: footerSurf, Z: 2},
	}
	if !e.overlays.Active() {
		root.Children = append(root.Children, e.composer.PickerOverlays(ctx, listH, maxSize.Width)...)
	}
	if pal, ok := e.composer.PaletteOverlay(ctx); ok {
		root.Children = append(root.Children, pal)
	}
	if list, ok := e.composer.ListOverlay(ctx); ok {
		root.Children = append(root.Children, list)
	}
	if e.toast.Visible() {
		toastSurf := e.toast.Draw(ctx)
		root.Children = append(root.Children, components.SubSurface{
			Origin:  components.Point{X: 0, Y: 0},
			Surface: toastSurf,
			Z:       40,
		})
	}
	return root
}

func (e *Editor) requestRedraw() {
	if e.App != nil {
		e.App.RequestRedraw()
	}
}

// RequestRedraw asks the app to repaint (safe to bind onto controller.RedrawRelay / controller.Bus).
func (e *Editor) RequestRedraw() {
	e.requestRedraw()
}

// StartUpdateCheck queries GitHub for a newer release in the background and
// surfaces a footer hint when one is available. cacheDir is where the version
// check may store its cache (e.g. project global root); empty disables disk cache.
func (e *Editor) StartUpdateCheck(cacheDir string) {
	ch := update.CheckAsync(update.CheckOptions{
		Current:  version.Version,
		CacheDir: cacheDir,
	})
	go func() {
		info, ok := <-ch
		if !ok || !info.Available {
			return
		}
		e.Publish(controller.FooterMsg{
			Kind:    controller.FooterUpdateAvailable,
			Latest:  info.Latest,
			Current: info.Current,
		})
	}()
}

// StartBranchWatch hot-reloads the git branch in the path label when the
// repo HEAD changes (checkout from another terminal, editor, …).
func (e *Editor) StartBranchWatch() {
	pathutil.WatchBranch(e.cwd, func(label string) {
		e.Publish(controller.BranchLabelMsg{Text: label})
	})
}

func (e *Editor) applyTheme(name string) {
	th, ok := components.ThemeByName(name)
	if !ok {
		return
	}
	e.theme = th
	e.composer.SetTheme(th)
	e.toast.Theme = th
	e.transcript.SetTheme(th)
	e.footer.SetTheme(th)
	e.overlays.SetTheme(th)
	e.toast.Show("Theme: "+name, toast.ToastSuccess, 2*time.Second)
	if e.vx != nil {
		e.vx.QueueRefresh()
	}
}
