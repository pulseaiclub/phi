// Package editor wires the TUI root widget and assembles domain panes.
package editor

import (
	"strconv"
	"strings"
	"time"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/app"
	"github.com/pulseaiclub/phi/internal/components/chat"
	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/codepane"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/composer"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/diffpane"
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
// cmd constructs Bus/Controller/App and passes them into NewEditor, which builds
// builtins via commands.NewBuiltinRegistry. Editor does not create
// controller.EngineController or fetch the project singleton.
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
	diff       *diffpane.Pane
	code       *codepane.Pane

	ctrl *controller.EngineController

	commands  *commands.CommandRegistry
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
	cwd, modelLabel, skillPath string,
	contextWindow int,
	modelNames []string,
) *Editor {
	e := &Editor{
		vx:       vx,
		App:      application,
		theme:    theme,
		cwd:      cwd,
		bus:      bus,
		ctrl:     ctrl,
		toast:    toast.Toast{Theme: theme},
		composer: composer.NewComposerPane(theme, modelLabel, cwd),
		footer:   footer.NewFooterChrome(theme, contextWindow),
	}
	e.transcript = transcript.NewTranscriptPane(theme, e.footer.Spinner(), "Phi "+version.Version)
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
				e.composer.FocusChat()
			}
		},
	)
	e.transcript.SetCopyHandlers(
		e.bus,
		func(text string) bool {
			return e.vx != nil && e.vx.CopyToClipboard(text) == nil
		},
	)
	e.diff = diffpane.New(e.theme, cwd,
		func(text string) {
			e.Publish(controller.SubmitMsg{Text: text})
		},
		func(text string) bool {
			return e.vx != nil && e.vx.CopyToClipboard(text) == nil
		},
		func(msg string) {
			e.Publish(controller.ToastMsg{Message: msg, Kind: toast.ToastSuccess, Duration: 2 * time.Second})
		},
	)

	e.code = codepane.New(e.theme, cwd,
		func(ref chat.Ref) {
			// The pane's own toast reports the add; this only fills the composer.
			e.composer.AddPendingRef(ref)
			e.composer.FocusChat()
		},
		func(msg string) {
			e.Publish(controller.ToastMsg{Message: msg, Kind: toast.ToastSuccess, Duration: 2 * time.Second})
		},
	)

	builtins := commands.NewBuiltinRegistry(
		e.bus,
		e.ctrl,
		e.composer,
		e.transcript,
		e.footer,
		modelNames,
		skillPath,
		e.openDiff,
		e.openCode,
	)
	e.commands = builtins.Registry
	e.sessions = builtins.Sessions
	e.extCmds = builtins.Ext

	cmdCtx := commands.NewContext(e.bus, func(title string, cmds []palette.PaletteCommand) {
		e.composer.PushPalette(title, cmds)
	})
	e.submitter = submit.NewSubmitter(
		e.ctrl,
		e.commands,
		e.transcript,
		e.footer.Activity(),
		e.composer,
		e.bus,
		func() commands.Context { return cmdCtx },
		e.overlays.PermissionActive,
		e.overlays.ContinueActive,
		e.overlays.ConfirmActive,
		e.overlays.ResolvePermission,
		e.overlays.ResolveContinue,
		e.overlays.ResolveConfirm,
	)
	builtins.Bind(
		e.submitter,
		func() commands.Context { return cmdCtx },
		func(items []session.SessionMeta, currentID string) {
			e.composer.ShowSessionList(items, currentID, e.sessions.Accept)
		},
		e.composer.ShowBranchList,
		func() string { return e.cwd },
		e.submitter.StreamActive,
	)

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
		e.blocksComposer,
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
		e.submitter.Cancel()
	case controller.MentionResultsMsg:
		e.composer.ApplyMentionResults(msg)
	case controller.OverlayMsg:
		e.overlays.Apply(msg)
	case controller.FooterMsg:
		e.footer.Apply(msg)
	case controller.ToastMsg:
		e.toast.Show(msg.Message, msg.Kind, msg.Duration)
	case controller.ThemeMsg:
		e.applyTheme(msg.Name)
	case controller.ModelChangeMsg:
		switch msg.Kind {
		case "model":
			e.composer.SetModelLabel(msg.Value, string(e.ctrl.ThinkLevel()))
		case "think_level":
			e.composer.SetModelLabel(e.ctrl.ModelName(), msg.Value)
		}
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

func (e *Editor) blocksComposer() bool {
	if e.overlays != nil && e.overlays.BlocksComposer() {
		return true
	}
	if e.diff != nil && e.diff.Active() {
		return true
	}
	return e.code != nil && e.code.Active()
}

// openDiff and openCode each close the other: two full-screen overlays cannot
// both own the frame, and the one underneath would come back on the next Esc.
func (e *Editor) openDiff(args []string) {
	if e.diff == nil {
		return
	}
	if e.code != nil {
		e.code.Close()
	}
	e.diff.OpenGit(e.cwd, args)
	e.captureOverlayFocus()
}

// openCode shows a file in the viewer. A ":line" suffix puts the cursor there.
// An "@" prefix is expanded to "./" so the shell can complete the path.
func (e *Editor) openCode(args []string) {
	if e.code == nil {
		return
	}
	path := strings.TrimSpace(strings.Join(args, " "))
	if strings.HasPrefix(path, "@") {
		path = "./" + path[1:]
	}
	if path == "" {
		e.toast.Show("usage: /code <path>[:line]", toast.ToastWarning, 2*time.Second)
		return
	}
	line := 0
	if base, tail, ok := strings.Cut(path, ":"); ok {
		if n, err := strconv.Atoi(tail); err == nil && n > 0 {
			path, line = base, n
		}
	}
	if e.diff != nil {
		e.diff.Close()
	}
	e.code.OpenAt(path, line)
	e.captureOverlayFocus()
}

func (e *Editor) captureOverlayFocus() {
	if e.App != nil {
		e.App.RequestFocus(e)
	}
	if e.composer != nil {
		e.composer.HideCompleters()
		e.composer.HidePalette()
	}
}

func (e *Editor) Handle(ctx *components.EventContext, ev xui.Event) {
	if ke, ok := ev.(xui.KeyEvent); ok && ke.CtrlC() {
		e.composer.Handle(ctx, ev)
		return
	}
	if e.diff != nil && e.diff.Active() {
		e.captureOverlayFocus()
		e.diff.Handle(ctx, ev)
		if !e.diff.Active() && e.composer != nil {
			e.composer.FocusChat()
		}
		return
	}
	if e.code != nil && e.code.Active() {
		e.captureOverlayFocus()
		e.code.Handle(ctx, ev)
		if !e.code.Active() && e.composer != nil {
			e.composer.FocusChat()
		}
		return
	}
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

	if e.diff != nil && e.diff.Active() {
		return e.drawOverlay(ctx, e.diff.Draw(ctx))
	}
	if e.code != nil && e.code.Active() {
		return e.drawOverlay(ctx, e.code.Draw(ctx))
	}

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

	listSurf := e.transcript.Draw(ctx, maxSize.Width, listH)
	listH = e.transcript.ListHeight()

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

// drawOverlay finishes a full-screen overlay: it keeps keyboard focus on the
// Editor (palette and slash leave it on Chat, where keys would land under the
// overlay) and stacks the toast on top.
func (e *Editor) drawOverlay(ctx components.DrawContext, root components.Surface) components.Surface {
	e.captureOverlayFocus()
	root.Widget = e
	if e.toast.Visible() {
		root.Children = append(root.Children, components.SubSurface{
			Origin:  components.Point{X: 0, Y: 0},
			Surface: e.toast.Draw(ctx),
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
	if e.diff != nil {
		e.diff.SetTheme(th)
	}
	if e.code != nil {
		e.code.SetTheme(th)
	}
	e.toast.Show("Theme: "+name, toast.ToastSuccess, 2*time.Second)
	if e.vx != nil {
		e.vx.QueueRefresh()
	}
}
