// Package code owns the full-screen source viewer pane (/code) as a runtime
// plugin, including its file list picker.
package code

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chat"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/components/listrows"
	"github.com/pulseaiclub/phi/internal/components/pane"
	uistoast "github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/tui/runtime"
	"github.com/pulseaiclub/phi/internal/util/filesearch"
)

// ToastSink shows a toast in the current frame; the root passes the toast
// plugin's method value. Defined here so plugins stay mutually independent.
type ToastSink interface {
	Show(message string, kind uistoast.ToastKind, d time.Duration)
}

// ListHost is the composer's shared list picker surface.
type ListHost interface {
	ShowList(items []listpicker.Item, cfg listpicker.ShowConfig, onAccept, onDelete func(listpicker.Item))
	SetListFocusReturn(w components.Widget)
}

// Deps carries the collaborators the root wires as method values.
type Deps struct {
	// AddRef drops a code-viewer reference into the composer (Editor).
	AddRef func(ref chat.Ref)
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

// Plugin wraps the source viewer pane.
type Plugin struct {
	code *pane.Code
	cwd  string
	deps Deps
}

// New builds the code plugin around its pane.
func New(theme components.Theme, cwd string, deps Deps) *Plugin {
	p := &Plugin{cwd: cwd, deps: deps}
	p.code = pane.NewCode(theme, cwd, deps.AddRef, p.publish)
	return p
}

func (p *Plugin) publish(msg string) { p.deps.Publish(msg) }

// Register wires the plugin into the runtime.
func (p *Plugin) Register(rt *runtime.Runtime) {
	rt.AddFullPane(p)
	rt.OnTheme(p.SetTheme)
}

// SetTheme switches the pane chrome.
func (p *Plugin) SetTheme(th components.Theme) { p.code.SetTheme(th) }

// Open runs the /code command. A ":line" suffix puts the cursor there; an "@"
// prefix expands to "./" so the shell can complete the path. Bare "/code"
// opens the file list picker.
func (p *Plugin) Open(args []string) {
	path := strings.TrimSpace(strings.Join(args, " "))
	if strings.HasPrefix(path, "@") {
		path = "./" + path[1:]
	}
	if path == "" {
		p.code.Close()
		p.showCodeFiles()
		return
	}
	line := 0
	if base, tail, ok := strings.Cut(path, ":"); ok {
		if n, err := strconv.Atoi(tail); err == nil && n > 0 {
			path, line = base, n
		}
	}
	p.code.OpenAt(path, line)
	p.focusPane()
}

// Close hides the pane (mutual exclusion is the shell's rule).
func (p *Plugin) Close() { p.code.Close() }

// Active reports whether the pane owns the frame.
func (p *Plugin) Active() bool { return p.code.Active() }

// Handle forwards keys and returns focus to the composer on close.
func (p *Plugin) Handle(ctx *components.EventContext, ev xui.Event) {
	p.focusPane()
	p.code.Handle(ctx, ev)
	if !p.code.Active() {
		p.deps.FocusChat()
	}
}

// Draw renders the pane.
func (p *Plugin) Draw(ctx components.DrawContext) components.Surface { return p.code.Draw(ctx) }

func (p *Plugin) focusPane() { p.deps.FocusPane() }

// showCodeFiles opens the file list picker over the workspace.
func (p *Plugin) showCodeFiles() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	paths, truncated, err := filesearch.Search(ctx, p.cwd, "", 10000)
	if err != nil {
		p.deps.Toast.Show("List files: "+err.Error(), uistoast.ToastError, 4*time.Second)
		p.deps.FocusChat()
		return
	}
	cfg := listrows.FileConfig()
	if truncated {
		cfg.Title = "Files (first 10000; use /code path for other files)"
	}
	p.deps.Lists.ShowList(listrows.FileItems(paths), cfg, func(item listpicker.Item) {
		// Picker IDs are literal paths, not the @path:line command syntax.
		p.code.OpenAt(item.ID, 0)
		p.deps.Lists.SetListFocusReturn(p.deps.FocusTarget)
		p.focusPane()
	}, nil)
}
