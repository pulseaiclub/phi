// Package editor wires the TUI root widget and assembles domain panes.
package editor

import (
	"strings"
	"time"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/app"
	"github.com/pulseaiclub/phi/internal/components/chat"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/components/status"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/session/shellhist"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/composer"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/footer"
	"github.com/pulseaiclub/phi/internal/tui/pathutil"
	"github.com/pulseaiclub/phi/internal/tui/plugin/bash"
	"github.com/pulseaiclub/phi/internal/tui/plugin/code"
	"github.com/pulseaiclub/phi/internal/tui/plugin/diff"
	"github.com/pulseaiclub/phi/internal/tui/plugin/overlay"
	toastplugin "github.com/pulseaiclub/phi/internal/tui/plugin/toast"
	"github.com/pulseaiclub/phi/internal/tui/runtime"
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
// Transport rules: UI→domain is direct calls (NewEditor wires method values,
// never anonymous forwarding closures; signature adapters are named Editor
// methods). Any-goroutine→UI is bus.Publish. See doc/tui.md "Transport rules".
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

	rt *runtime.Runtime

	transcript    *transcript.TranscriptPane
	composer      *composer.ComposerPane
	footer        *footer.FooterChrome
	overlayPlugin *overlay.Plugin
	toastPlugin   *toastplugin.Plugin
	diffPlugin    *diff.Plugin
	codePlugin    *code.Plugin
	bashPlugin    *bash.Plugin

	// The TUI redraws at 60fps; the flowing status highlight moves every fourth
	// frame, matching the ~80ms cadence status.Spinner used to declare.
	spin       *status.Spinner
	spinTick   int
	activity   *controller.ActivityHandler
	updateHint string
	commandCtx commands.Context

	ctrl *controller.EngineController

	commands  *commands.CommandRegistry
	sessions  *commands.SessionCommands
	submitter *submit.Submitter
}

// spinnerFrameDivisor throttles the 60fps app redraw to the status slot's
// ~15Hz spinner cadence.
const spinnerFrameDivisor = 4

// NewEditor builds the TUI panes and wires injected collaborators.
// application, bus, and ctrl must be non-nil.
func NewEditor(
	application *app.App,
	bus *controller.Bus,
	ctrl *controller.EngineController,
	vx *xui.XUI,
	theme components.Theme,
	cwd, modelLabel string,
	skillPaths []string,
	modelNames []string,
) *Editor {
	e := &Editor{
		vx:          vx,
		App:         application,
		theme:       theme,
		cwd:         cwd,
		bus:         bus,
		ctrl:        ctrl,
		rt:          runtime.New(),
		toastPlugin: toastplugin.New(theme),
		composer:    composer.NewComposerPane(theme, modelLabel, cwd),
		footer:      footer.NewFooterChrome(theme),
		spin:        status.NewSpinner(theme.ToolName),
		activity:    controller.NewActivityHandler(),
	}
	e.rt.Register(e.toastPlugin)
	e.activity.SetOnChange(e.onActivityChange)
	e.composer.SetBashPredictor(composer.NewBashPredictor(cwd))
	// Prompt recall is opt-in: it persists what the user typed under the home
	// directory, so it stays off until PHI_PROMPT_HISTORY turns it on.
	if dir := ctrl.SessionDir(); dir != "" && shellhist.PromptsEnabled() {
		e.composer.SetPromptHistory(shellhist.NewPromptHistory(dir))
	}
	e.transcript = transcript.NewTranscriptPane(theme, e.spin, "Phi "+version.Version)
	e.syncStatusSlot()
	e.overlayPlugin = overlay.New(
		theme,
		e.activity,
		e.composer,
		e.focusSelf,
		e.composer.FocusChat,
		e.handleErrorReply,
	)
	e.rt.Register(e.overlayPlugin)
	e.transcript.SetCopyHandlers(e.bus, e.copyToClipboard)
	e.diffPlugin = diff.New(e.theme, cwd, diff.Deps{
		Submit:      e.publishSubmit,
		Copy:        e.copyToClipboard,
		Toast:       e.toastPlugin,
		Publish:     e.publishToast,
		Lists:       e.composer,
		FocusPane:   e.captureOverlayFocus,
		FocusChat:   e.composer.FocusChat,
		FocusTarget: e,
	})
	e.rt.Register(e.diffPlugin)
	e.codePlugin = code.New(e.theme, cwd, code.Deps{
		AddRef:      e.addPendingRef,
		Toast:       e.toastPlugin,
		Publish:     e.publishToast,
		Lists:       e.composer,
		FocusPane:   e.captureOverlayFocus,
		FocusChat:   e.composer.FocusChat,
		FocusTarget: e,
	})
	e.rt.Register(e.codePlugin)
	e.bashPlugin = bash.New(e.transcript, e.composer, e.bus, e.ctrl)
	e.rt.Register(e.bashPlugin)

	builtins := commands.NewBuiltinRegistry(
		e.bus,
		e.ctrl,
		e.composer,
		e.transcript,
		modelNames,
		skillPaths,
		e.openDiff,
		e.openCode,
	)
	e.commands = builtins.Registry
	e.sessions = builtins.Sessions
	e.sessions.ResetActivity = e.resetActivity

	cmdCtx := commands.NewContext(e.bus, e.composer.PushPalette)
	e.commandCtx = cmdCtx
	e.composer.OnPaletteOpen = e.refreshPalette
	e.submitter = submit.NewSubmitter(
		e.ctrl,
		e.commands,
		e.transcript,
		e.activity,
		e.composer,
		e.bus,
		cmdCtx,
		submit.ShellHooks{
			Running:    e.bashPlugin.Running,
			Cancel:     e.bashPlugin.Cancel,
			SyncBorder: e.bashPlugin.SyncBorder,
		},
		e.overlayPlugin.BlocksSessionSwitch,
		e.overlayPlugin.Cancel,
	)
	builtins.Bind(
		e.showSessionList,
		e.composer.RefreshSessionList,
		e.composer.ShowBranchList,
		e.composer.RefreshBranchList,
		e.showSkillList,
		e.cwd,
		e.submitter.StreamActive,
	)

	e.composer.Wire(composer.WireDeps{
		Transcript:            e.transcript,
		Submitter:             e.submitter,
		Commands:              e.commands,
		Bus:                   e.bus,
		DrainBus:              e.drainBus,
		OnRedraw:              e.vx.QueueRefresh,
		ImageEnabled:          e.ctrl.ImageEnabled,
		OverlayBlocksComposer: e.blocksComposer,
		HandleOverlayKey:      e.handleOverlayKey,
		HandleCopyKey:         e.handleCopyKey,
		RequestFocusEditor:    e.focusSelf,
		RequestFocus:          e.App.RequestFocus,
		CtrlClose:             e.ctrl.Close,
	})
	// The palette draws inside the transcript area, so the draft below stays
	// clickable; keep it from taking the keyboard while the palette is open.
	e.App.ModalInput = e.composer.PaletteOpen

	e.refreshPalette()
	return e
}

// Publish sends a message onto the bus from any goroutine / widget callback.
func (e *Editor) Publish(m controller.Msg) {
	e.bus.Publish(m)
}

// Update applies one message on the UI goroutine. Runtime plugins get the
// types they subscribed to; the switch below keeps the unmigrated shell msgs.
func (e *Editor) Update(m controller.Msg) {
	if e.rt.Dispatch(m) {
		return
	}
	switch msg := m.(type) {
	case controller.SubmitMsg:
		// Plugins claim their inputs first ("!cmd" shell lines); the rest
		// follows the shell submit path.
		if !e.rt.FilterSubmit(msg.Text) {
			e.submitter.Submit(msg.Text)
		}
	case controller.CancelStreamMsg:
		e.submitter.Cancel()
	case controller.MentionResultsMsg:
		e.composer.ApplyMentionResults(msg)
	case controller.BashSuggestionsMsg:
		e.composer.ApplyBashSuggestions(msg)
	case controller.LoopStartedMsg:
		if e.ctrl.CurrentLoop(msg.Gen) {
			e.overlayPlugin.ResolveError(controller.ErrorReply{})
			e.activity.Apply(controller.ActivityWaiting)
		}
	case controller.LoopFinishedMsg:
		if !e.ctrl.CurrentLoop(msg.Gen) {
			return
		}
		e.transcript.ApplySession(session.CancelStreaming{})
		e.transcript.Sync()
		e.activity.Apply(controller.ActivityIdle)
		if msg.Cancelled {
			e.activity.Apply(controller.ActivityCancelled)
		}
		if msg.ErrorText != "" {
			e.overlayPlugin.Apply(controller.OverlayMsg{Kind: controller.OverlayErrorAsk, ErrorText: msg.ErrorText})
		}
	case controller.FooterMsg:
		e.applyFooterMessage(msg)
	case controller.ThemeMsg:
		e.applyTheme(msg.Name)
	case controller.BranchLabelMsg:
		e.composer.SetBranchLabel(msg.Text)
		e.vx.QueueRefresh()
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
			if msg.Gen != 0 && !e.ctrl.CurrentLoop(msg.Gen) {
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
		e.activity.SyncFromSnap(e.transcript.Snapshot())
		e.syncStatusSlot()
		if atBottom {
			e.transcript.StickToBottom()
		}
	}
}

func (e *Editor) onActivityChange() {
	e.spin.Frame = 0
	e.spinTick = 0
	e.syncStatusSlot()
}

func (e *Editor) advanceSpinner() {
	e.spinTick++
	if e.activity.ShowSpinner() && e.spinTick%spinnerFrameDivisor == 0 {
		e.spin.Tick()
		e.syncStatusSlot()
	}
}

func (e *Editor) syncStatusSlot() {
	label := footer.StatusLabel(e.theme, footer.StatusInput{
		Activity:      e.activity.Label(e.transcript.Snapshot()),
		ShowSpinner:   e.activity.ShowSpinner(),
		Spinner:       e.spin,
		Usage:         e.transcript.Snapshot().LastUsage(),
		ContextWindow: e.ctrl.ContextWindow(),
	})
	if !label.Visible() {
		e.composer.ClearBottomLeftLabel()
		return
	}
	e.composer.SetBottomLeftLabel(label)
}

func (e *Editor) resetActivity() {
	e.activity.Apply(controller.ActivityIdle)
}

func (e *Editor) refreshPalette() {
	e.composer.SetPaletteCommands(e.commands.BuildPalette(e.commandCtx))
}

func (e *Editor) applyFooterMessage(msg controller.FooterMsg) {
	switch msg.Kind {
	case controller.FooterClearIfActivity:
		if e.activity.Current == msg.If {
			e.activity.Apply(controller.ActivityIdle)
		}
	case controller.FooterSetActivity:
		e.activity.Apply(msg.Activity)
	case controller.FooterUpdateAvailable:
		e.updateHint = strings.TrimPrefix(msg.Latest, "v") + " available · phi update"
	}
}

func (e *Editor) blocksComposer() bool { return e.rt.BlocksComposer() }

// handleOverlayKey routes composer keys to runtime bottom panes (error ask).
func (e *Editor) handleOverlayKey(ctx *components.EventContext, ke xui.KeyEvent) bool {
	return e.rt.BottomKey(ctx, ke)
}

// openDiff and openCode each close the other: two full-screen overlays cannot
// both own the frame, and the one underneath would come back on the next Esc.
// The mutual exclusion is the shell's rule; the plugins own the rest.
func (e *Editor) openDiff(args []string) {
	e.codePlugin.Close()
	e.diffPlugin.Open(args)
}

// openCode shows a file in the viewer (see plugin/code for the path syntax).
func (e *Editor) openCode(args []string) {
	e.diffPlugin.Close()
	e.codePlugin.Open(args)
}

func (e *Editor) captureOverlayFocus() {
	e.App.RequestFocus(e)
	e.composer.HideCompleters()
	e.composer.HidePalette()
}

func (e *Editor) Handle(ctx *components.EventContext, ev xui.Event) {
	if ke, ok := ev.(xui.KeyEvent); ok && ke.CtrlC() {
		e.composer.Handle(ctx, ev)
		return
	}
	if pane, ok := e.rt.ActiveFullPane(); ok {
		pane.Handle(ctx, ev)
		return
	}
	e.composer.Handle(ctx, ev)
}

func (e *Editor) handleCopyKey(ctx *components.EventContext, ke xui.KeyEvent) bool {
	return e.transcript.HandleCopyKey(ctx, ke)
}

// focusSelf returns keyboard focus to the editor itself; full-screen overlays
// keep keys away from the composer while open.
func (e *Editor) focusSelf() { e.App.RequestFocus(e) }

// publishSubmit feeds diff-review edits through the normal submit path.
func (e *Editor) publishSubmit(text string) {
	e.Publish(controller.SubmitMsg{Text: text})
}

// publishToast is the panes' success-toast callback: it hops to the bus so the
// toast renders on the UI goroutine.
func (e *Editor) publishToast(msg string) {
	e.Publish(controller.ToastMsg{Message: msg, Kind: toast.ToastSuccess, Duration: 2 * time.Second})
}

// copyToClipboard adapts xui's error-returning copy to the panes' bool
// contract.
func (e *Editor) copyToClipboard(text string) bool {
	return e.vx.CopyToClipboard(text) == nil
}

// handleErrorReply performs the error dialog's Retry/Copy side effects. It
// runs on the UI goroutine as the overlay resolves.
func (e *Editor) handleErrorReply(r controller.ErrorReply, text string) {
	switch {
	case r.Retry:
		e.activity.Apply(controller.ActivityWaiting)
		if !e.ctrl.RetryLastPrompt() {
			e.activity.Apply(controller.ActivityIdle)
			e.toastPlugin.Show("Nothing to retry", toast.ToastWarning, 2*time.Second)
		}
	case r.Copy:
		if text == "" {
			return
		}
		if e.copyToClipboard(text) {
			e.toastPlugin.Show("Copied to clipboard", toast.ToastSuccess, 2*time.Second)
		} else {
			e.toastPlugin.Show("Clipboard unavailable", toast.ToastError, 2*time.Second)
		}
	}
}

// addPendingRef drops a code-viewer reference into the composer and refocuses
// chat; the pane's own toast reports the add.
func (e *Editor) addPendingRef(ref chat.Ref) {
	e.composer.AddPendingRef(ref)
	e.composer.FocusChat()
}

// showSessionList opens the session picker bound to the session commands'
// accept/delete handlers.
func (e *Editor) showSessionList(items []session.SessionMeta, currentID string) {
	e.composer.ShowSessionList(items, currentID, e.sessions.Accept, e.sessions.Delete)
}

// showSkillList opens the skills picker bound to the skills commands' accept
// handler. /skill is a slash command, not a Ctrl+K palette entry: attaching a
// skill belongs to composing a turn.
func (e *Editor) showSkillList(
	items []listpicker.Item,
	cfg listpicker.ShowConfig,
	onAccept func(listpicker.Item),
) {
	e.composer.ShowList(items, cfg, onAccept, nil)
}

// Draw renders the editor surface for the given draw context.
func (e *Editor) Draw(ctx components.DrawContext) components.Surface {
	e.drainBus()

	e.advanceSpinner()
	_ = e.toastPlugin.Visible()

	if pane, ok := e.rt.ActiveFullPane(); ok {
		return e.drawOverlay(ctx, pane.Draw(ctx))
	}

	maxSize := ctx.Max
	root := components.Surface{Size: maxSize, Widget: e}

	footerH := 1
	var chatH int
	// askActive mirrors the old overlays.Active(): the ask pane replaces the
	// composer slot and suppresses the pickers below.
	askH, askActive := e.rt.BottomHeight(maxSize.Width, ctx.Method)
	if askActive {
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
	if surf, ok := e.rt.DrawBottom(ctx, maxSize.Width, chatH); ok {
		chatSurf = surf
	} else {
		chatSurf = e.composer.DrawChat(ctx, maxSize.Width, chatH)
	}
	footerSurf := e.footer.Draw(ctx, maxSize.Width, footer.State{
		UpdateHint: e.updateHint,
	})

	root.Children = []components.SubSurface{
		{Origin: components.Point{X: 0, Y: 0}, Surface: listSurf},
		{Origin: components.Point{X: 0, Y: listH}, Surface: chatSurf, Z: 1},
		{Origin: components.Point{X: 0, Y: maxSize.Height - footerH}, Surface: footerSurf, Z: 2},
	}
	if !askActive {
		root.Children = append(root.Children, e.composer.PickerOverlays(ctx, listH, maxSize.Width)...)
	}
	// The palette may not shrink to an empty box: below this height the picker
	// draws neither rows nor prompt nor hint, yet keeps eating keys while it
	// is open. Let it overlap the draft instead — it is a modal, like it was
	// before it moved into the transcript area.
	const minPaletteOverlayHeight = 9
	paletteCtx := ctx.WithConstraints(components.Size{}, components.Size{
		Width:  maxSize.Width,
		Height: max(listH, minPaletteOverlayHeight),
	})
	if pal, ok := e.composer.PaletteOverlay(paletteCtx); ok {
		root.Children = append(root.Children, pal)
	}
	if list, ok := e.composer.ListOverlay(ctx); ok {
		root.Children = append(root.Children, list)
	}
	root.Children = append(root.Children, e.rt.Overlays(ctx)...)
	return root
}

// drawOverlay finishes a full-screen overlay: it keeps keyboard focus on the
// Editor (palette and slash leave it on Chat, where keys would land under the
// overlay) and stacks the runtime overlay layers (toast) on top.
func (e *Editor) drawOverlay(ctx components.DrawContext, root components.Surface) components.Surface {
	e.captureOverlayFocus()
	root.Widget = e
	root.Children = append(root.Children, e.rt.Overlays(ctx)...)
	return root
}

func (e *Editor) requestRedraw() {
	e.App.RequestRedraw()
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
	e.rt.ApplyTheme(th)
	e.transcript.SetTheme(th)
	e.footer.SetTheme(th)
	e.spin.Style = th.ToolName
	e.syncStatusSlot()
	// overlay, diff, and code follow via rt.ApplyTheme subscriptions.
	e.toastPlugin.Show("Theme: "+name, toast.ToastSuccess, 2*time.Second)
	e.vx.QueueRefresh()
}
