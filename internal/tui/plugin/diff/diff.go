// Package diff owns the full-screen git diff review pane (/diff) as a runtime
// plugin, including its file list picker round-trip.
package diff

import (
	"context"
	"time"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/tui/commands"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/changelist"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/components/pane"
	uistoast "github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/tui/runtime"
	"github.com/pulseaiclub/phi/internal/util/gitx"
)

// ToastSink shows a toast in the current frame; the root passes the toast
// plugin's method value. Defined here so plugins stay mutually independent.
type ToastSink interface {
	Show(message string, kind uistoast.ToastKind, d time.Duration)
}

// ListHost is the composer's shared list picker surface.
type ListHost interface {
	ShowList(items []listpicker.Item, cfg listpicker.ShowConfig, onAccept, onDelete func(listpicker.Item))
	RestoreListState(state listpicker.State)
	ListState() listpicker.State
	SetListFocusReturn(w components.Widget)
}

// Deps carries the collaborators the root wires as method values.
type Deps struct {
	// Registry receives the /diff slash command.
	Registry *commands.CommandRegistry
	// Submit feeds diff-review edits through the normal submit path.
	Submit func(text string)
	// Copy adapts xui's error-returning clipboard to the pane's bool contract.
	Copy func(text string) bool
	// Toast shows a toast in the same frame (picker error paths).
	Toast ToastSink
	// Publish publishes a success ToastMsg: the toast lands on the next
	// drain, matching the pre-plugin pane behavior.
	Publish func(msg string)
	// Lists is the composer's shared list picker host.
	Lists ListHost
	// FocusPane pulls keyboard focus to the shell while the pane is open.
	FocusPane func()
	// FocusChat returns keyboard focus to the composer on close.
	FocusChat func()
	// FocusTarget is the widget the list picker returns focus to (the shell).
	FocusTarget components.Widget
}

// Plugin wraps the diff review pane.
type Plugin struct {
	diff      *pane.Diff
	cwd       string
	deps      Deps
	rt        *runtime.Runtime
	listState listpicker.State
}

// New builds the diff plugin around its pane and registers /diff.
func New(theme components.Theme, cwd string, deps Deps) *Plugin {
	p := &Plugin{cwd: cwd, deps: deps}
	p.diff = pane.NewDiff(theme, cwd, deps.Submit, deps.Copy, p.publish)
	p.registerCommand()
	return p
}

func (p *Plugin) publish(msg string) { p.deps.Publish(msg) }

func (p *Plugin) registerCommand() {
	if p.deps.Registry == nil {
		return
	}
	p.deps.Registry.Register(commands.Command{
		Name:        "diff",
		Description: "Browse changed files — /diff, /diff staged, /diff HEAD, /diff @file",
		Slash:       true,
		Insert:      "/diff ",
		Run: func(_ commands.Context, args []string) {
			p.Open(args)
		},
	})
}

// Register wires the plugin into the runtime.
func (p *Plugin) Register(rt *runtime.Runtime) {
	p.rt = rt
	rt.Panes.AddFull(p)
	rt.Theme.On(p.SetTheme)
}

func (p *Plugin) closeSiblings() {
	if p.rt != nil {
		p.rt.Panes.CloseOthers(p)
	}
}

// SetTheme switches the pane chrome.
func (p *Plugin) SetTheme(th components.Theme) { p.diff.SetTheme(th) }

// Open runs the /diff command: bare "/diff" opens the change list, arguments
// open a specific spec.
func (p *Plugin) Open(args []string) {
	p.closeSiblings()
	if len(args) == 0 {
		p.diff.Close()
		p.listState = listpicker.State{}
		p.showChanges(false)
		return
	}
	p.diff.OpenGit(p.cwd, args)
	p.focusPane()
}

// Close hides the pane (mutual exclusion is the shell's rule).
func (p *Plugin) Close() { p.diff.Close() }

// Active reports whether the pane owns the frame.
func (p *Plugin) Active() bool { return p.diff.Active() }

// Handle forwards keys and restores the pane's own follow-up on close.
func (p *Plugin) Handle(ctx *components.EventContext, ev xui.Event) {
	p.focusPane()
	p.diff.Handle(ctx, ev)
	if !p.diff.Active() {
		if p.diff.ReturnToList() {
			p.showChanges(true)
		} else {
			p.deps.FocusChat()
		}
	}
}

// Draw renders the pane.
func (p *Plugin) Draw(ctx components.DrawContext) components.Surface { return p.diff.Draw(ctx) }

func (p *Plugin) focusPane() { p.deps.FocusPane() }

// showChanges opens the change list picker, optionally restoring the caller's
// previous place in it.
func (p *Plugin) showChanges(restore bool) {
	root, changes, err := gitx.ChangedFiles(context.Background(), p.cwd)
	if err != nil {
		p.deps.Toast.Show(err.Error(), uistoast.ToastError, 4*time.Second)
		p.deps.FocusChat()
		return
	}
	if len(changes) == 0 {
		p.deps.Toast.Show("No changes", uistoast.ToastSuccess, 2*time.Second)
		p.deps.FocusChat()
		return
	}
	byID := make(map[string]gitx.Change, len(changes))
	for _, change := range changes {
		byID[change.Path] = change
	}
	p.deps.Lists.ShowList(changelist.Items(changes), changelist.Config(), func(item listpicker.Item) {
		p.listState = p.deps.Lists.ListState()
		p.diff.OpenChange(root, byID[item.ID])
		p.deps.Lists.SetListFocusReturn(p.deps.FocusTarget)
		p.focusPane()
	}, nil)
	if restore {
		p.deps.Lists.RestoreListState(p.listState)
		if _, exists := byID[p.listState.SelectedID]; !exists {
			p.deps.Toast.Show(
				"Selected change is no longer available; list refreshed",
				uistoast.ToastWarning,
				3*time.Second,
			)
		}
	}
}

// OpenText loads raw diff input without git (tests, programmatic opens).
func (p *Plugin) OpenText(input string, spec []string) { p.diff.OpenText(input, spec) }
