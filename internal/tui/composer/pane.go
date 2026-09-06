package composer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chat"
	"github.com/pulseaiclub/phi/internal/components/layout"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/components/mention"
	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/components/sessionlist"
	"github.com/pulseaiclub/phi/internal/components/sessiontree"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/footer"
	"github.com/pulseaiclub/phi/internal/tui/pathutil"
	"github.com/pulseaiclub/phi/internal/tui/transcript"
	"github.com/pulseaiclub/phi/internal/util/clipboard"
	"github.com/pulseaiclub/phi/internal/util/filesearch"
	imgutil "github.com/pulseaiclub/phi/internal/util/image"
)

// ComposerPane owns the chat input, slash/@ pickers, palette, and session list.
type ComposerPane struct {
	theme components.Theme
	cwd   string

	Chat       chat.ChatInput
	Tree       sessiontree.Picker
	mention    mention.Picker
	slash      mention.Picker
	question   mention.Picker
	palette    palette.CommandPalette
	listPicker listpicker.Picker
	onListPick func(listpicker.Item)

	mentionGen int
	// mentionCancel stops the search in flight; nil before the first search.
	mentionCancel context.CancelFunc
	commands      *commands.CommandRegistry

	transcript *transcript.TranscriptPane
	submitter  BusyChecker

	bus      *controller.Bus
	drainBus func()
	onRedraw func()

	overlayBlocksComposer func() bool
	handlePermissionKey   WireKeyHandler
	handleContinueKey     WireKeyHandler
	handleConfirmKey      WireKeyHandler
	handleCopyKey         WireKeyHandler
	requestFocusEditor    func()
	requestFocus          func(components.Widget)
	ctrlClose             func()
	imageEnabled          func() bool
}

// NewComposerPane builds composer widgets; call Wire before use.
func NewComposerPane(theme components.Theme, model, cwd string) *ComposerPane {
	return &ComposerPane{
		theme: theme,
		cwd:   cwd,
		Chat:  newChatInput(theme, model, cwd),
		mention: mention.Picker{
			Theme: theme,
		},
		slash: mention.Picker{
			Theme:  theme,
			Prefix: "/",
		},
		question: mention.Picker{
			Theme:    theme,
			NoPrefix: true,
		},
		palette: palette.CommandPalette{
			Theme: theme,
		},
		listPicker: listpicker.Picker{
			Theme: theme,
		},
	}
}

// Wire binds bus, transcript, and editor overlay hooks after Editor assembly.
func (c *ComposerPane) Wire(
	transcript *transcript.TranscriptPane,
	submitter BusyChecker,
	commands *commands.CommandRegistry,
	cwd string,
	bus *controller.Bus,
	drainBus func(),
	onRedraw func(),
	imageEnabled func() bool,
	overlayBlocksComposer func() bool,
	handlePermissionKey WireKeyHandler,
	handleContinueKey WireKeyHandler,
	handleConfirmKey WireKeyHandler,
	handleCopyKey WireKeyHandler,
	requestFocusEditor func(),
	requestFocus func(components.Widget),
	ctrlClose func(),
) {
	if c == nil {
		return
	}
	c.cwd = cwd
	c.commands = commands
	c.transcript = transcript
	c.submitter = submitter
	c.bus = bus
	c.drainBus = drainBus
	c.onRedraw = onRedraw
	c.imageEnabled = imageEnabled
	c.overlayBlocksComposer = overlayBlocksComposer
	c.handlePermissionKey = handlePermissionKey
	c.handleContinueKey = handleContinueKey
	c.handleConfirmKey = handleConfirmKey
	c.handleCopyKey = handleCopyKey
	c.requestFocusEditor = requestFocusEditor
	c.requestFocus = requestFocus
	c.ctrlClose = ctrlClose

	c.palette.FocusReturn = &c.Chat
	c.listPicker.FocusReturn = &c.Chat
	c.listPicker.OnAccept = func(item listpicker.Item) {
		if c.onListPick != nil {
			c.onListPick(item)
		}
	}
	c.Chat.OnSubmit = func(text string) {
		c.bus.Publish(controller.SubmitMsg{Text: text})
		if c.drainBus != nil {
			c.drainBus()
		}
	}
	c.Chat.OnChange = func(text string) {
		c.SyncBashBorder(text)
		if c.onRedraw != nil {
			c.onRedraw()
		}
	}
	c.Chat.OnMentionChange = c.onMentionChange
	c.Chat.OnSlashChange = c.onSlashChange
	c.Chat.OnQuestionChange = c.onQuestionChange
	c.mention.OnAccept = c.acceptMention
	c.slash.OnAccept = c.acceptSlash
	c.question.OnAccept = c.acceptQuestion
}

// HideCompleters closes mention, slash, question, and @ pickers.
func (c *ComposerPane) HideCompleters() {
	if c == nil {
		return
	}
	c.mention.Hide()
	c.Chat.MentionOpen = false
	c.abandonMentionSearch()
	c.slash.Hide()
	c.Chat.SlashOpen = false
	c.question.Hide()
	c.Chat.QuestionOpen = false
}

// HidePalette closes the command palette if open.
func (c *ComposerPane) HidePalette() {
	if c != nil {
		c.palette.Hide()
	}
}

// SetListPickHandler registers the callback for list picker Enter.
func (c *ComposerPane) SetListPickHandler(fn func(listpicker.Item)) {
	if c != nil {
		c.onListPick = fn
	}
}

// ShowList opens the opaque list picker overlay.
func (c *ComposerPane) ShowList(items []listpicker.Item, cfg listpicker.ShowConfig) {
	if c == nil {
		return
	}
	c.HideCompleters()
	c.HidePalette()
	c.listPicker.Show(items, cfg)
	if c.requestFocus != nil {
		c.requestFocus(&c.listPicker)
	}
	if c.onRedraw != nil {
		c.onRedraw()
	}
}

// ShowSessionList maps sessions into list rows and opens the picker.
func (c *ComposerPane) ShowSessionList(items []session.SessionMeta, currentID string) {
	if c == nil {
		return
	}
	c.ShowList(sessionlist.Items(items, currentID, time.Time{}), sessionlist.Config())
}

// ListOverlay returns the list picker surface when open.
func (c *ComposerPane) ListOverlay(ctx components.DrawContext) (components.SubSurface, bool) {
	if c == nil || !c.listPicker.Open {
		return components.SubSurface{}, false
	}
	return components.SubSurface{
		Origin:  components.Point{X: 0, Y: 0},
		Surface: c.listPicker.Draw(ctx),
		Z:       20,
	}, true
}

// ClearInput clears the chat composer text.
func (c *ComposerPane) ClearInput() {
	if c == nil {
		return
	}
	c.Chat.Value = ""
	c.Chat.Cursor = 0
}

// SetInput replaces the composer text and places the cursor at the end.
func (c *ComposerPane) SetInput(text string) {
	if c == nil {
		return
	}
	c.Chat.Value = text
	c.Chat.Cursor = len(text)
}

// PendingSkills returns attached skill names awaiting submit.
func (c *ComposerPane) PendingSkills() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.Chat.PendingSkills))
	out = append(out, c.Chat.PendingSkills...)
	return out
}

func (c *ComposerPane) PendingImages() []imgutil.Attachment {
	if c == nil {
		return nil
	}
	out := make([]imgutil.Attachment, len(c.Chat.PendingImages))
	copy(out, c.Chat.PendingImages)
	return out
}

// ClearPendingImages removes attached images from the composer.
func (c *ComposerPane) ClearPendingImages() {
	if c != nil {
		c.Chat.ClearPendingImages()
	}
}

// ClearPendingSkills removes attached skills from the composer.
func (c *ComposerPane) ClearPendingSkills() {
	if c != nil {
		c.Chat.ClearPendingSkills()
	}
}

// SyncBashBorder updates composer chrome for "!cmd" prefix.
func (c *ComposerPane) SyncBashBorder(text string) {
	if c != nil && c.submitter != nil {
		c.submitter.SyncBashBorder(text)
	}
}

// CloseMentionSlash hides @ and / pickers.
func (c *ComposerPane) CloseMentionSlash() {
	if c == nil {
		return
	}
	c.mention.Hide()
	c.Chat.MentionOpen = false
	c.abandonMentionSearch()
	c.slash.Hide()
	c.Chat.SlashOpen = false
	c.question.Hide()
	c.Chat.QuestionOpen = false
}

// SetBashBorderActive toggles bash-mode border styling.
func (c *ComposerPane) SetBashBorderActive(active bool) {
	if c == nil {
		return
	}
	if active {
		c.Chat.BorderStyle = c.theme.ToolName
	} else {
		c.Chat.BorderStyle = c.theme.Border
	}
}

// FocusChat requests keyboard focus on the chat input.
func (c *ComposerPane) FocusChat() {
	if c != nil && c.requestFocus != nil {
		c.requestFocus(&c.Chat)
	}
}

// AddPendingSkill attaches a skill badge to the composer.
func (c *ComposerPane) AddPendingSkill(name string) {
	if c != nil {
		c.Chat.AddPendingSkill(name)
		if c.onRedraw != nil {
			c.onRedraw()
		}
	}
}

// AddPendingImage attaches an image to the composer.
func (c *ComposerPane) AddPendingImage(att imgutil.Attachment) {
	if c != nil {
		c.Chat.AddPendingImage(att)
		if c.onRedraw != nil {
			c.onRedraw()
		}
	}
}

// SetModelLabel updates the model name in the composer header.
func (c *ComposerPane) SetModelLabel(name string) {
	if c != nil {
		c.Chat.TopRightLabel.Text = name
	}
}

// SetBranchLabel updates the path label in the composer footer.
func (c *ComposerPane) SetBranchLabel(text string) {
	if c != nil {
		c.Chat.BottomRightLabel.Text = text
	}
}

// ClearBottomLeftLabel clears the composer status slot (activity or tokens).
func (c *ComposerPane) ClearBottomLeftLabel() {
	if c != nil {
		c.Chat.BottomLeftLabel = layout.BorderLabel{}
	}
}

// SetBottomLeftLabel sets the composer status slot (activity or tokens).
func (c *ComposerPane) SetBottomLeftLabel(label layout.BorderLabel) {
	if c != nil {
		c.Chat.BottomLeftLabel = label
	}
}

// SetPaletteCommands replaces Ctrl+K root commands.
func (c *ComposerPane) SetPaletteCommands(cmds []palette.PaletteCommand) {
	if c != nil {
		c.palette.Commands = cmds
	}
}

// PushPalette opens or nests a palette submenu.
func (c *ComposerPane) PushPalette(title string, cmds []palette.PaletteCommand) {
	if c == nil {
		return
	}
	if !c.palette.Open {
		c.palette.Show()
	}
	c.palette.Push(title, cmds)
	if c.requestFocus != nil {
		c.requestFocus(&c.palette)
	}
}

// SetTheme updates composer widget themes.
func (c *ComposerPane) SetTheme(th components.Theme) {
	if c == nil {
		return
	}
	c.theme = th
	c.Chat.Theme = th
	c.Chat.BorderStyle = th.Border
	c.Chat.TextStyle = th.Foreground
	c.Chat.BottomRightLabel.Style = footer.PathLabelStyle(th)
	c.Chat.TopRightLabel.Style = th.IdentityOrSuccess()
	c.palette.Theme = th
	c.listPicker.Theme = th
	c.Tree.Theme = th
	c.mention.Theme = th
	c.slash.Theme = th
	c.question.Theme = th
	c.SyncBashBorder(c.Chat.Value)
}

// ApplyMentionResults updates the @ picker from async file search.
func (c *ComposerPane) ApplyMentionResults(msg controller.MentionResultsMsg) {
	if c == nil || msg.Gen != c.mentionGen || !c.mention.Open {
		return
	}
	if msg.ErrText != "" {
		c.mention.SetResults(nil, msg.ErrText)
		return
	}
	items := make([]mention.Item, 0, len(msg.Paths))
	for _, p := range msg.Paths {
		items = append(items, mention.Item{Path: p})
	}
	status := ""
	switch {
	case len(items) == 0:
		status = "No matching files"
	case msg.Truncated:
		// Say the list is partial, so a missing file reads as "narrow the
		// query" rather than "the file is not there".
		status = fmt.Sprintf("First %d matches — type more to narrow", len(items))
	}
	c.mention.SetResults(items, status)
}

// PreferredHeight reports the chat input area height.
func (c *ComposerPane) PreferredHeight(width int, method xui.WidthMethod) int {
	if c == nil {
		return 5
	}
	if c.Tree.Open {
		return c.Tree.PreferredHeight(width, method)
	}
	chatH := c.Chat.PreferredHeight(width, method)
	minChatH := 5
	if len(c.Chat.PendingSkills) > 0 {
		minChatH++
	}
	if chatH < minChatH {
		chatH = minChatH
	}
	return chatH
}

// DrawChat renders the chat input surface.
func (c *ComposerPane) DrawChat(ctx components.DrawContext, width, height int) components.Surface {
	if c == nil {
		return components.Surface{}
	}
	if c.Tree.Open {
		return c.Tree.Draw(ctx.WithConstraints(components.Size{}, components.Size{Width: width, Height: height}))
	}
	return c.Chat.Draw(
		ctx.WithConstraints(components.Size{}, components.Size{Width: width, Height: height}),
	)
}

// PickerOverlays returns slash and @ picker surfaces anchored above the composer.
func (c *ComposerPane) PickerOverlays(ctx components.DrawContext, listH, width int) []components.SubSurface {
	if c == nil {
		return nil
	}
	var out []components.SubSurface
	if c.slash.Open {
		c.slash.AnchorBottomY = listH
		c.slash.AnchorX = 0
		c.slash.AnchorWidth = width
		out = append(out, components.SubSurface{
			Origin:  components.Point{X: 0, Y: 0},
			Surface: c.slash.Draw(ctx),
			Z:       15,
		})
	}
	if c.question.Open {
		c.question.AnchorBottomY = listH
		c.question.AnchorX = 0
		c.question.AnchorWidth = width
		out = append(out, components.SubSurface{
			Origin:  components.Point{X: 0, Y: 0},
			Surface: c.question.Draw(ctx),
			Z:       15,
		})
	}
	if c.mention.Open {
		c.mention.AnchorBottomY = listH
		c.mention.AnchorX = 0
		c.mention.AnchorWidth = width
		out = append(out, components.SubSurface{
			Origin:  components.Point{X: 0, Y: 0},
			Surface: c.mention.Draw(ctx),
			Z:       15,
		})
	}
	return out
}

// PaletteOverlay returns the Ctrl+K palette surface when open.
func (c *ComposerPane) PaletteOverlay(ctx components.DrawContext) (components.SubSurface, bool) {
	if c == nil || !c.palette.Open {
		return components.SubSurface{}, false
	}
	return components.SubSurface{
		Origin:  components.Point{X: 0, Y: 0},
		Surface: c.palette.Draw(ctx),
		Z:       20,
	}, true
}

// Handle dispatches keyboard/mouse input to the composer area.
func (c *ComposerPane) Handle(ctx *components.EventContext, ev xui.Event) {
	if c == nil {
		return
	}
	switch ev := ev.(type) {
	case xui.FocusEvent:
		if c.overlayBlocksComposer != nil && c.overlayBlocksComposer() {
			if c.requestFocusEditor != nil {
				c.requestFocusEditor()
			}
		} else if c.Tree.Open {
			if c.requestFocusEditor != nil {
				c.requestFocusEditor()
			}
		} else if c.listPicker.Open {
			if c.requestFocus != nil {
				c.requestFocus(&c.listPicker)
			}
		} else if c.palette.Open {
			if c.requestFocus != nil {
				c.requestFocus(&c.palette)
			}
		} else {
			c.FocusChat()
		}
	case xui.KeyEvent:
		if ev.CtrlC() {
			if c.ctrlClose != nil {
				c.ctrlClose()
			}
			ctx.Quit = true
			return
		}
		if c.handlePermissionKey != nil && c.handlePermissionKey(ctx, ev) {
			return
		}
		if c.handleContinueKey != nil && c.handleContinueKey(ctx, ev) {
			return
		}
		if c.handleConfirmKey != nil && c.handleConfirmKey(ctx, ev) {
			return
		}
		if c.Tree.Open {
			c.Tree.Handle(ctx, ev)
			return
		}
		if c.handleCopyKey != nil && c.handleCopyKey(ctx, ev) {
			return
		}
		if ev.Press && ev.Code == xui.KeyEscape {
			if c.handleEscape(ctx) {
				return
			}
		}
		if c.listPicker.Open {
			c.listPicker.Handle(ctx, ev)
			if !c.listPicker.Open {
				c.FocusChat()
			}
			return
		}
		if ev.Press && ev.Mods.Has(xui.ModCtrl) && ev.Code == xui.KeyRune &&
			(ev.Rune == 'k' || ev.Rune == 'K') {
			if c.palette.Open {
				c.palette.Hide()
				c.FocusChat()
			} else {
				c.HideCompleters()
				c.palette.Show()
				if c.requestFocus != nil {
					c.requestFocus(&c.palette)
				}
			}
			ctx.ConsumeAndRedraw()
			return
		}
		if c.palette.Open {
			c.palette.Handle(ctx, ev)
			if !c.palette.Open {
				c.FocusChat()
			}
			return
		}
		if c.question.Open && mentionNavKey(ev) {
			c.question.Handle(ctx, ev)
			if !c.question.Open {
				c.Chat.QuestionOpen = false
			}
			return
		}
		if c.slash.Open && mentionNavKey(ev) {
			c.slash.Handle(ctx, ev)
			if !c.slash.Open {
				c.Chat.SlashOpen = false
			}
			return
		}
		if c.mention.Open && mentionNavKey(ev) {
			c.mention.Handle(ctx, ev)
			if !c.mention.Open {
				c.Chat.MentionOpen = false
				c.abandonMentionSearch()
			}
			return
		}
		if ev.Code == xui.KeyPageUp || ev.Code == xui.KeyPageDown {
			if c.transcript != nil {
				c.transcript.HandlePageKey(ctx, ev)
			}
			return
		}
		if ev.Press && ev.Mods.Has(xui.ModCtrl) && ev.Code == xui.KeyRune && (ev.Rune == 'v' || ev.Rune == 'V') {
			// Ctrl+V: attempt to attach an image from the system clipboard.
			if c.tryAttachClipboardImage(ctx) {
				return
			}
		}
		c.Chat.Handle(ctx, ev)
	case xui.MouseEvent:
		if c.Tree.Open {
			c.Tree.Handle(ctx, ev)
			return
		}
		if c.listPicker.Open {
			c.listPicker.Handle(ctx, ev)
			return
		}
		if c.palette.Open {
			c.palette.Handle(ctx, ev)
			return
		}
		if c.transcript != nil {
			c.transcript.HandleMouse(ctx, ev, c.FocusChat)
		}
	case xui.PasteEvent:
		if c.Tree.Open {
			c.Tree.Handle(ctx, ev)
			return
		}
		if c.listPicker.Open {
			c.listPicker.Handle(ctx, ev)
			return
		}
		if c.palette.Open {
			c.palette.Handle(ctx, ev)
			return
		}
		c.Chat.Handle(ctx, ev)
	}
}

// handleEscape closes the topmost open overlay, then falls back to
// canceling an in-flight stream or selection. Returns true when the
// Escape key was consumed. Priority order matters: pickers first, then
// the running stream, then a selection.
func (c *ComposerPane) handleEscape(ctx *components.EventContext) bool {
	if c.listPicker.Open {
		c.listPicker.Hide()
		c.FocusChat()
		ctx.ConsumeAndRedraw()
		return true
	}
	if c.palette.Open {
		c.palette.Hide()
		c.FocusChat()
		ctx.ConsumeAndRedraw()
		return true
	}
	if c.question.Open {
		c.question.Cancel()
		c.Chat.QuestionOpen = false
		ctx.ConsumeAndRedraw()
		return true
	}
	if c.slash.Open {
		c.slash.Cancel()
		c.Chat.SlashOpen = false
		ctx.ConsumeAndRedraw()
		return true
	}
	if c.mention.Open {
		c.mention.Cancel()
		c.Chat.MentionOpen = false
		c.abandonMentionSearch()
		ctx.ConsumeAndRedraw()
		return true
	}
	if c.submitter != nil && (c.submitter.RunningBash() || c.submitter.IsBusy()) {
		c.bus.Publish(controller.CancelStreamMsg{})
		if c.drainBus != nil {
			c.drainBus()
		}
		ctx.ConsumeAndRedraw()
		return true
	}
	if c.transcript != nil && c.transcript.SelectionActive() {
		c.transcript.ClearSelection()
		ctx.ConsumeAndRedraw()
		return true
	}
	return false
}

func (c *ComposerPane) onMentionChange(active bool, query string) {
	if c == nil {
		return
	}
	if !active {
		c.mention.Hide()
		c.Chat.MentionOpen = false
		c.abandonMentionSearch()
		return
	}
	if c.slash.Open || c.Chat.SlashOpen || c.question.Open || c.Chat.QuestionOpen {
		return
	}
	c.slash.Hide()
	c.Chat.SlashOpen = false
	c.mention.Show()
	c.Chat.MentionOpen = true
	if len(c.mention.Items) == 0 {
		c.mention.Status = "Searching…"
	}
	c.scheduleMentionSearch(query)
}

func (c *ComposerPane) onSlashChange(active bool, query string) {
	if c == nil {
		return
	}
	if !active {
		c.slash.Hide()
		c.Chat.SlashOpen = false
		return
	}
	c.question.Hide()
	c.Chat.QuestionOpen = false
	c.mention.Hide()
	c.Chat.MentionOpen = false
	c.abandonMentionSearch()
	var items []mention.Item
	if c.commands != nil {
		items = c.commands.FilterSlash(query)
	}
	status := ""
	if len(items) == 0 {
		status = "No matching commands"
	}
	c.slash.SetResults(items, status)
	c.slash.Show()
	c.Chat.SlashOpen = true
}

func (c *ComposerPane) onQuestionChange(active bool, query string) {
	if c == nil {
		return
	}
	if !active {
		c.question.Hide()
		c.Chat.QuestionOpen = false
		return
	}
	c.mention.Hide()
	c.Chat.MentionOpen = false
	c.abandonMentionSearch()
	c.slash.Hide()
	c.Chat.SlashOpen = false
	items := filterQuestionItems(query, questionShortcutItems())
	status := ""
	if len(items) == 0 {
		status = "No matching shortcuts"
	}
	c.question.SetResults(items, status)
	c.question.Show()
	c.Chat.QuestionOpen = true
}

// mentionSearchLimit is how many file rows the picker shows.
const mentionSearchLimit = 20

// abandonMentionSearch drops any result still in flight and stops the work
// behind it. Bumping the generation alone only makes the UI ignore the answer;
// the fd process keeps walking the tree. Every path that closes the picker or
// starts a new query goes through here, so the two always happen together.
func (c *ComposerPane) abandonMentionSearch() {
	c.mentionGen++
	if c.mentionCancel != nil {
		c.mentionCancel()
		c.mentionCancel = nil
	}
}

// scheduleMentionSearch debounces, then searches. Each call cancels the search
// in flight: without that, every keystroke leaves an fd process walking the
// tree, and on a large one they pile up faster than they finish.
func (c *ComposerPane) scheduleMentionSearch(query string) {
	if c == nil {
		return
	}
	c.abandonMentionSearch()
	gen := c.mentionGen
	cwd := c.cwd
	bus := c.bus

	ctx, cancel := context.WithCancel(context.Background())
	c.mentionCancel = cancel

	go func() {
		defer cancel()
		// Debounce. A newer keystroke cancels this before any work starts.
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return
		}

		searchCtx, searchCancel := context.WithTimeout(ctx, 3*time.Second)
		defer searchCancel()
		paths, truncated, err := filesearch.Search(searchCtx, cwd, query, mentionSearchLimit)

		// A superseded search has nothing useful to report.
		if ctx.Err() != nil {
			return
		}

		msg := controller.MentionResultsMsg{Gen: gen, Query: query, Paths: paths, Truncated: truncated}
		if err != nil {
			if errors.Is(err, filesearch.ErrTimeout) {
				msg.ErrText = "Search timed out — type more of the path"
			} else {
				msg.ErrText = err.Error()
			}
		}
		bus.Publish(msg)
	}()
}

func (c *ComposerPane) acceptMention(item mention.Item) {
	if c == nil {
		return
	}
	_, start, end, ok := chat.ActiveMention(c.Chat.Value, c.Chat.Cursor)
	if !ok {
		start, end = c.Chat.Cursor, c.Chat.Cursor
	}
	c.abandonMentionSearch()
	c.mention.Hide()
	c.Chat.MentionOpen = false

	// @mention path: try to load it as an image file first.
	abs := resolveMentionPath(c.cwd, item.Path)
	if res, err := imgutil.Load(abs); err == nil {
		if !c.imagesSupported() {
			c.warnImagesDisabled()
			// Keep a text path reference when the model cannot take images.
			c.Chat.ReplaceRange(start, end, "@"+item.Path+" ")
			return
		}
		// Image loaded successfully — add to the pending queue for submission.
		c.Chat.AddPendingImage(imgutil.AttachmentFromResult(imgutil.AttachmentLabel(abs), res))
		c.Chat.ReplaceRange(start, end, "")
		if c.onRedraw != nil {
			c.onRedraw()
		}
		return
	}

	c.Chat.ReplaceRange(start, end, "@"+item.Path+" ")
}

// imagesSupported is true when the active model opts into image attachments,
// or when no model callback is wired (tests / partial setup).
func (c *ComposerPane) imagesSupported() bool {
	return c == nil || c.imageEnabled == nil || c.imageEnabled()
}

func (c *ComposerPane) warnImagesDisabled() {
	c.showToast(
		"Current model does not support images (set image_enabled: true in config)",
		toast.ToastWarning,
		3*time.Second,
	)
}

func (c *ComposerPane) showToast(msg string, kind toast.ToastKind, d time.Duration) {
	if c == nil {
		return
	}
	c.bus.Publish(controller.ToastMsg{Message: msg, Kind: kind, Duration: d})
}

func (c *ComposerPane) tryAttachClipboardImage(ctx *components.EventContext) bool {
	if !c.imagesSupported() {
		c.warnImagesDisabled()
		ctx.ConsumeAndRedraw()
		return true
	}
	res, err := clipboard.ReadImageResult()
	if errors.Is(err, clipboard.ErrUnavailable) {
		return false
	}
	if err != nil {
		c.showToast(err.Error(), toast.ToastError, 3*time.Second)
		ctx.ConsumeAndRedraw()
		return true
	}
	// Build a sequential label (clipboard #1, #2, …) for the pending queue.
	label := fmt.Sprintf("clipboard #%d", len(c.Chat.PendingImages)+1)
	// Add the clipboard image to the pending queue for submission.
	c.Chat.AddPendingImage(imgutil.AttachmentFromResult(label, res))
	if c.onRedraw != nil {
		c.onRedraw()
	}
	ctx.ConsumeAndRedraw()
	return true
}

func resolveMentionPath(cwd, rel string) string {
	rel = strings.TrimSpace(rel)
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel)
	}
	return filepath.Join(cwd, rel)
}

func (c *ComposerPane) acceptQuestion(_ mention.Item) {
	if c == nil {
		return
	}
	_, start, end, ok := chat.ActiveQuestion(c.Chat.Value, c.Chat.Cursor)
	if ok {
		c.Chat.ReplaceRange(start, end, "")
	}
	c.question.Hide()
	c.Chat.QuestionOpen = false
}

func (c *ComposerPane) acceptSlash(item mention.Item) {
	if c == nil {
		return
	}
	_, start, end, ok := chat.ActiveSlash(c.Chat.Value, c.Chat.Cursor)
	if !ok {
		start, end = 0, c.Chat.Cursor
	}
	insert := ""
	if c.commands != nil {
		insert = c.commands.LookupInsert(item.Path)
	}
	if insert == "" {
		insert = "/" + item.Path
	}
	c.Chat.ReplaceRange(start, end, insert)
	c.slash.Hide()
	c.Chat.SlashOpen = false
	if !strings.HasSuffix(insert, " ") {
		c.bus.Publish(controller.SubmitMsg{Text: strings.TrimSpace(insert)})
		if c.drainBus != nil {
			c.drainBus()
		}
	}
}

func newChatInput(theme components.Theme, model, cwd string) chat.ChatInput {
	return chat.ChatInput{
		MinBodyRows:    3,
		MaxBodyRows:    8,
		UseBlockCursor: false,
		PaddingX:       1,
		Theme:          theme,
		BorderStyle:    theme.Border,
		TextStyle:      theme.Foreground,
		CursorStyle:    xui.Style{Reverse: true},
		TopRightLabel: layout.BorderLabel{
			Text:  model,
			Style: theme.IdentityOrSuccess(),
		},
		BottomRightLabel: layout.BorderLabel{
			Text:  pathutil.PathWithBranch(cwd),
			Style: footer.PathLabelStyle(theme),
		},
	}
}

func mentionNavKey(e xui.KeyEvent) bool {
	if !e.Press {
		return false
	}
	switch e.Code {
	case xui.KeyUp, xui.KeyDown, xui.KeyTab, xui.KeyEnter, xui.KeyEscape:
		return true
	case xui.KeyRune:
		if e.Mods.Has(xui.ModCtrl) && (e.Rune == 'n' || e.Rune == 'N' || e.Rune == 'p' || e.Rune == 'P') {
			return true
		}
	}
	return false
}

// Ensure ComposerPane implements Input.
var _ Input = (*ComposerPane)(nil)
