package transcript

import (
	"strings"
	"time"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/block"
	"github.com/pulseaiclub/phi/internal/components/splash"
	"github.com/pulseaiclub/phi/internal/components/status"
	"github.com/pulseaiclub/phi/internal/components/toast"
	msglist "github.com/pulseaiclub/phi/internal/components/transcript"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// textSel tracks drag selection over the transcript.
// Coordinates are content-space (relative to MessageList content origin),
// so the highlight stays on the selected text when the list scrolls.
type textSel struct {
	pending  bool
	dragging bool
	active   bool
	ax, ay   int
	ex, ey   int
}

func (s *textSel) clear() {
	*s = textSel{}
}

// TranscriptPane owns session snapshot, transcript widgets, and list interaction.
type TranscriptPane struct {
	theme components.Theme

	list      msglist.MessageList
	listIDs   []string
	snap      session.Snapshot
	mapper    *Mapper
	subagents *SubagentStore
	welcome   splash.Screen
	startedAt time.Time

	sel          textSel
	listH        int
	lastListSurf components.Surface

	onUsage func(session.TokenUsage)
	copyFn  func(text string) bool
	bus     *controller.Bus
}

// NewTranscriptPane builds an empty transcript view.
func NewTranscriptPane(theme components.Theme, spin *status.Spinner, brand string) *TranscriptPane {
	t := &TranscriptPane{
		theme: theme,
		list: msglist.MessageList{
			Theme:    theme,
			Selected: -1,
		},
		welcome: splash.Screen{
			Sphere: &splash.Sphere{Fast: true},
			Theme:  theme,
			Brand:  brand,
		},
		startedAt: time.Now(),
		subagents: NewSubagentStore(),
	}
	t.mapper = NewMapper(theme, spin, func() {
		t.list.InvalidateHeights()
	})
	t.mapper.Children = t.subagents.Children
	t.mapper.ChildrenByJob = t.subagents.ChildrenByJob
	return t
}

// SetUsageCallback fires when an assistant message reports token usage.
func (t *TranscriptPane) SetUsageCallback(fn func(session.TokenUsage)) {
	if t != nil {
		t.onUsage = fn
	}
}

// SetCopyHandlers wires clipboard copy; feedback toasts go through the bus.
func (t *TranscriptPane) SetCopyHandlers(
	bus *controller.Bus,
	copyFn func(text string) bool,
) {
	if t == nil {
		return
	}
	t.copyFn = copyFn
	t.bus = bus
}

// Snapshot returns the current session model (read-only use on UI goroutine).
func (t *TranscriptPane) Snapshot() session.Snapshot {
	if t == nil {
		return session.Snapshot{}
	}
	return t.snap
}

// IsStreaming reports whether the agent stream is in flight.
func (t *TranscriptPane) IsStreaming() bool {
	if t == nil {
		return false
	}
	return session.IsStreaming(t.snap)
}

// IsEmpty reports whether the transcript has no committed entries.
func (t *TranscriptPane) IsEmpty() bool {
	if t == nil {
		return true
	}
	return len(t.list.Entries) == 0
}

// LastCopyText returns copy text for the last message block.
func (t *TranscriptPane) LastCopyText() string {
	if t == nil {
		return ""
	}
	return t.list.LastCopyText()
}

// AtBottom reports whether the list is pinned to the latest content.
func (t *TranscriptPane) AtBottom() bool {
	if t == nil {
		return true
	}
	return t.list.ScrollFromBottom == 0
}

// StickToBottom scrolls the list to the latest content.
func (t *TranscriptPane) StickToBottom() {
	if t != nil {
		t.list.StickToBottom()
	}
}

// ListHeight is the last drawn transcript area height (for picker anchoring).
func (t *TranscriptPane) ListHeight() int {
	if t == nil {
		return 0
	}
	return t.listH
}

// SelectionActive reports whether a drag-selection highlight is shown.
func (t *TranscriptPane) SelectionActive() bool {
	return t != nil && t.sel.active
}

// ClearSelection clears transcript text selection state.
func (t *TranscriptPane) ClearSelection() {
	if t != nil {
		t.sel.clear()
	}
}

// ApplySession applies one session event on the UI goroutine.
func (t *TranscriptPane) ApplySession(ev session.Event) {
	if t == nil {
		return
	}
	t.snap = session.Apply(t.snap, ev)
	if upd, ok := ev.(session.AssistantMessageUpdate); ok && upd.Message.Usage.Reported() && t.onUsage != nil {
		t.onUsage(upd.Message.Usage)
	}
	if td, ok := ev.(session.ToolData); ok {
		t.applyAgentToolData(td)
	}
}

// ApplyJobProgress updates nested sub-agent rows. Returns true when sync is needed.
func (t *TranscriptPane) ApplyJobProgress(p job.Progress) bool {
	if t == nil || t.subagents == nil {
		return false
	}
	return t.subagents.ApplyProgress(p)
}

// Sync rebuilds transcript widgets from snap.
func (t *TranscriptPane) Sync() {
	if t == nil || t.mapper == nil {
		return
	}
	oldIDs := t.listIDs
	entries, ids, dirty := t.mapper.Sync(t.list.Entries, t.listIDs, t.snap)
	t.list.ReindexHeights(oldIDs, ids)
	t.list.Entries = entries
	t.listIDs = ids
	t.list.InvalidateHeightsAt(dirty...)
}

// LoadReplay replaces snap and clears widget cache after ctrl replay.
func (t *TranscriptPane) LoadReplay(snap session.Snapshot) {
	if t == nil {
		return
	}
	t.snap = snap
	if t.onUsage != nil {
		var usage session.TokenUsage
		for _, message := range snap.Messages {
			if message.Usage.Reported() {
				usage = message.Usage
			}
		}
		t.onUsage(usage)
	}
	t.list.Entries = nil
	t.listIDs = nil
	t.list.InvalidateHeights()
}

// ResetSubagents clears nested job UI state (e.g. after /clear).
func (t *TranscriptPane) ResetSubagents() {
	if t == nil {
		return
	}
	t.subagents = NewSubagentStore()
	if t.mapper != nil {
		t.mapper.Children = t.subagents.Children
		t.mapper.ChildrenByJob = t.subagents.ChildrenByJob
	}
}

// SetTheme updates transcript chrome and existing widgets.
func (t *TranscriptPane) SetTheme(th components.Theme) {
	if t == nil {
		return
	}
	t.theme = th
	t.welcome.Theme = th
	t.list.Theme = th
	if t.mapper != nil {
		t.mapper.SetTheme(th)
	}
	applyThemeToWidgets(t.list.Entries, th)
	t.list.InvalidateHeights()
}

// Draw renders the transcript or welcome screen into listH.
func (t *TranscriptPane) Draw(ctx components.DrawContext, width, height int) components.Surface {
	if t == nil {
		return components.Surface{}
	}
	t.listH = height
	if t.welcome.Sphere != nil {
		t.welcome.Sphere.Time = time.Since(t.startedAt).Seconds()
	}
	constraints := ctx.WithConstraints(components.Size{}, components.Size{Width: width, Height: height})
	var listSurf components.Surface
	if len(t.list.Entries) == 0 {
		listSurf = t.welcome.Draw(constraints)
	} else {
		listSurf = t.list.Draw(constraints)
	}
	if t.sel.active {
		hl := t.theme.SelectionBg
		hl.Fg = t.theme.SelectionFg.Fg
		ax, ay, ex, ey := t.viewSel()
		components.ApplySelectionHighlight(&listSurf, ax, ay, ex, ey, hl)
	}
	t.lastListSurf = listSurf
	return listSurf
}

// HandlePageKey forwards page up/down to the message list.
func (t *TranscriptPane) HandlePageKey(ctx *components.EventContext, ev xui.KeyEvent) {
	if t != nil {
		t.list.Handle(ctx, ev)
	}
}

// HandleCopyKey handles copy chords over the transcript.
func (t *TranscriptPane) HandleCopyKey(ctx *components.EventContext, e xui.KeyEvent) bool {
	if t == nil || !e.Press {
		return false
	}
	copyChord := false
	if e.Code == xui.KeyRune && (e.Rune == 'c' || e.Rune == 'C') {
		if e.Mods.Has(xui.ModCtrl) && e.Mods.Has(xui.ModShift) {
			copyChord = true
		}
		if e.Mods.Has(xui.ModSuper) && !e.Mods.Has(xui.ModCtrl) {
			copyChord = true
		}
	}
	if !copyChord {
		return false
	}
	text := t.list.SelectedCopyText()
	if text == "" {
		text = t.list.LastCopyText()
	}
	if text == "" {
		return true
	}
	t.copyBlock(text)
	ctx.ConsumeAndRedraw()
	return true
}

// HandleMouse handles wheel, drag-selection, and block selection over the list.
func (t *TranscriptPane) HandleMouse(ctx *components.EventContext, e xui.MouseEvent, focusComposer func()) {
	if t == nil {
		return
	}
	if e.Button == xui.MouseWheelUp || e.Button == xui.MouseWheelDown {
		t.list.Handle(ctx, e)
		return
	}
	if e.Button != xui.MouseLeft && e.Button != 0 {
		if e.Action != xui.MouseMotion && e.Action != xui.MouseDrag {
			return
		}
	}

	inList := e.Y >= 0 && e.Y < t.listH && t.listH > 0 && len(t.list.Entries) > 0

	switch e.Action {
	case xui.MousePress:
		if e.Button != xui.MouseLeft {
			return
		}
		if !inList {
			t.sel.clear()
			ctx.Redraw = true
			return
		}
		cy := t.toContentY(e.Y)
		t.sel = textSel{
			pending: true,
			ax:      e.X,
			ay:      cy,
			ex:      e.X,
			ey:      cy,
		}
		if focusComposer != nil {
			focusComposer()
		}
		ctx.Redraw = true
		return

	case xui.MouseDrag, xui.MouseMotion:
		if !t.sel.pending && !t.sel.dragging {
			return
		}
		if e.Action == xui.MouseMotion && e.Button != xui.MouseLeft {
			return
		}
		t.sel.dragging = true
		t.sel.active = true
		t.sel.ex = e.X
		t.sel.ey = t.toContentY(e.Y)
		ctx.ConsumeAndRedraw()
		return

	case xui.MouseRelease:
		if e.Button != xui.MouseLeft {
			return
		}
		if !t.sel.pending && !t.sel.dragging {
			return
		}
		t.sel.ex = e.X
		t.sel.ey = t.toContentY(e.Y)
		if t.sel.dragging && (t.sel.ax != t.sel.ex || t.sel.ay != t.sel.ey) {
			ax, ay, ex, ey := t.viewSel()
			text := components.ExtractSurfaceText(t.lastListSurf, ax, ay, ex, ey)
			t.sel.active = true
			if text != "" {
				t.copyResult(text, "Selection copied to clipboard", "Failed to copy selection")
			}
			t.sel.pending = false
			t.sel.dragging = false
			ctx.ConsumeAndRedraw()
			return
		}
		idx := t.list.IndexAtPoint(e.X, e.Y)
		if idx >= 0 {
			t.list.Selected = idx
		}
		t.sel.clear()
		if focusComposer != nil {
			focusComposer()
		}
		ctx.ConsumeAndRedraw()
	}
}

func (t *TranscriptPane) applyAgentToolData(td session.ToolData) {
	name := strings.ToLower(td.Run.Name)
	switch name {
	case "agent_spawn", "agent_wait":
	default:
		return
	}
	parsed := tools.ParseAgentResult(td.Run.Output)
	if !parsed.OK {
		return
	}
	t.subagents.Bind(parsed.JobID, td.Run.ToolUseID)
	t.subagents.ApplyResult(td.Run.ToolUseID, parsed)
}

func (t *TranscriptPane) viewSel() (ax, ay, ex, ey int) {
	ox := t.list.ContentOrigin()
	return t.sel.ax, t.sel.ay + ox, t.sel.ex, t.sel.ey + ox
}

func (t *TranscriptPane) toContentY(viewY int) int {
	return viewY - t.list.ContentOrigin()
}

func (t *TranscriptPane) copyResult(text, okMsg, failMsg string) {
	if text == "" {
		return
	}
	ok := t.copyFn != nil && t.copyFn(text)
	if ok {
		t.bus.Publish(controller.ToastMsg{Message: okMsg, Kind: toast.ToastSuccess, Duration: 2 * time.Second})
	} else {
		t.bus.Publish(controller.ToastMsg{Message: failMsg, Kind: toast.ToastError, Duration: 2 * time.Second})
	}
}

// CopyBlock copies text to the clipboard with user feedback.
func (t *TranscriptPane) CopyBlock(text string) {
	t.copyBlock(text)
}

func (t *TranscriptPane) copyBlock(text string) {
	t.copyResult(text, "Copied to clipboard", "Failed to copy")
}

func applyThemeToWidgets(entries []components.Widget, th components.Theme) {
	for _, w := range entries {
		switch b := w.(type) {
		case *block.UserBlock:
			b.Theme = th
		case *block.AssistantBlock:
			b.Theme = th
		case *block.ThinkingBlock:
			b.Theme = th
		case *block.CompactionBlock:
			b.Theme = th
		case *block.ToolBlock:
			b.Theme = th
		case *block.BashBlock:
			b.Theme = th
		case *block.AgentBlock:
			b.Theme = th
		}
	}
}
