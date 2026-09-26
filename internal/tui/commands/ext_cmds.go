package commands

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/debuglog"
	"github.com/pulseaiclub/phi/internal/extension/manifest"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// extComposer updates the palette command list.
type extComposer interface {
	SetPaletteCommands([]palette.PaletteCommand)
}

// extFooter updates the extension status indicator.
type extFooter interface {
	SetExtensionStatus(status string)
}

// extSubmitter submits text to the agent and reports busy state.
type extSubmitter interface {
	IsBusy() bool
	Submit(text string)
}

// ExtCommands owns slash commands registered from extensions.
type ExtCommands struct {
	Registry   *CommandRegistry
	Ctrl       *controller.EngineController
	Composer   extComposer
	Footer     extFooter
	Submitter  extSubmitter
	Bus        *controller.Bus
	CommandCtx func() Context

	gen     atomic.Uint64
	running atomic.Bool
}

// Sync replaces extension-sourced slash commands from the current Runner.
func (h *ExtCommands) Sync() {
	if h == nil || h.Registry == nil {
		return
	}
	h.gen.Add(1)
	h.Registry.clearExtCommands()
	if h.Ctrl != nil {
		for _, entry := range h.Ctrl.Extensions().CommandEntries() {
			name := entry.Name
			desc := entry.Description
			if desc == "" {
				desc = "extension command"
			}
			if !h.Registry.registerExt(h.slashCommand(name, desc, entry.NeedsArgs)) {
				debuglog.Logf("extension: command %q skipped (name already registered)", name)
			}
		}
	}
	var ctx Context
	if h.CommandCtx != nil {
		ctx = h.CommandCtx()
	}
	h.Composer.SetPaletteCommands(h.Registry.BuildPalette(ctx))
}

func (h *ExtCommands) slashCommand(name, desc string, needsArgs bool) Command {
	return Command{
		Name:        name,
		Description: desc,
		Slash:       true,
		NeedsArgs:   needsArgs,
		Run: func(ctx Context, args []string) error {
			if h.running.Load() {
				ctx.Toast("An extension command is already running", toast.ToastWarning, 3*time.Second)
				return nil
			}
			text := strings.TrimSpace(strings.Join(args, " "))
			go h.run(name, text)
			return nil
		},
	}
}

func (h *ExtCommands) run(name, args string) {
	if h == nil {
		return
	}
	if !h.running.CompareAndSwap(false, true) {
		h.Bus.Publish(controller.ExtCommandResultMsg{
			Gen: h.gen.Load(),
			Err: "An extension command is already running",
		})
		return
	}
	defer h.running.Store(false)

	gen := h.gen.Load()
	if h.Ctrl == nil || h.Ctrl.Extensions() == nil {
		h.Bus.Publish(controller.ExtCommandResultMsg{Gen: gen, Err: "extensions are not loaded"})
		return
	}
	out, err := h.Ctrl.Extensions().RunCommand(name, args)
	if gen != h.gen.Load() {
		return
	}
	if err != nil {
		h.Bus.Publish(controller.ExtCommandResultMsg{Gen: gen, Err: err.Error()})
		return
	}
	h.Bus.Publish(controller.ExtCommandResultMsg{Gen: gen, Submit: out.Submit})
}

// Apply delivers a finished extension command onto the UI goroutine.
func (h *ExtCommands) Apply(msg controller.ExtCommandResultMsg) {
	if h == nil || msg.Gen != h.gen.Load() {
		return
	}
	if msg.Err != "" {
		publishToast(h.Bus, msg.Err, toast.ToastError, 3*time.Second)
		return
	}
	if msg.StatusSet {
		h.Footer.SetExtensionStatus(msg.Status)
	}
	if msg.Toast != "" {
		publishToast(h.Bus, msg.Toast, toast.ToastSuccess, 3*time.Second)
	}
	if msg.Submit != "" {
		if h.Submitter.IsBusy() {
			publishToast(h.Bus, "Cannot submit while a reply is running", toast.ToastWarning, 3*time.Second)
			return
		}
		h.Submitter.Submit(msg.Submit)
	}
}

// Register wires the extensions palette command (list / reload).
func (h *ExtCommands) Register(r *CommandRegistry) {
	if h == nil || r == nil {
		return
	}
	r.Register(Command{
		Name: "extensions",
		Build: func(ctx Context) palette.PaletteCommand {
			var push func(string, []palette.PaletteCommand)
			if ctx != nil {
				push = ctx.PushSubmenu
			}
			return buildExtensionsPalette(h.ListEntries, h.Reload, push)
		},
	})
}

// ListEntries builds disabled palette rows from discovery results + warnings.
func (h *ExtCommands) ListEntries() []palette.PaletteCommand {
	if h == nil || h.Ctrl == nil {
		return ExtensionListEntries(nil, nil, nil)
	}
	found, warns, err := h.Ctrl.ListExtensions()
	return ExtensionListEntries(found, warns, err)
}

// Reload rescans extensions and refreshes the slash/palette surface.
func (h *ExtCommands) Reload() {
	if h == nil || h.Ctrl == nil {
		return
	}
	n, warns, err := h.Ctrl.ReloadExtensions()
	if err != nil {
		publishToast(h.Bus, "Extensions reload: "+err.Error(), toast.ToastError, 3*time.Second)
		return
	}
	h.Sync()
	if len(warns) > 0 {
		h.Bus.Publish(controller.ToastMsg{
			Message:  fmt.Sprintf("Extensions: reloaded %d (%d warning(s))", n, len(warns)),
			Kind:     toast.ToastWarning,
			Duration: 3 * time.Second,
		})
		return
	}
	h.Bus.Publish(controller.ToastMsg{
		Message:  fmt.Sprintf("Extensions: reloaded %d", n),
		Kind:     toast.ToastSuccess,
		Duration: 2 * time.Second,
	})
}

func buildExtensionsPalette(
	listFn func() []palette.PaletteCommand,
	reload func(),
	push func(string, []palette.PaletteCommand),
) palette.PaletteCommand {
	return palette.PaletteCommand{
		ID:           "extensions",
		Noun:         "extensions",
		Verb:         "manage",
		Keywords:     []string{"extension", "plugin", "pxb", "reload", "list"},
		SubmenuTitle: "Extensions",
		Submenu: []palette.PaletteCommand{
			{
				ID:       "extensions-list",
				Verb:     "list",
				Keywords: []string{"show", "status", "loaded"},
				Run: func() {
					cmds := []palette.PaletteCommand{{
						ID:       "extensions-list-empty",
						Verb:     "No extensions found",
						Disabled: true,
					}}
					if listFn != nil {
						if built := listFn(); len(built) > 0 {
							cmds = built
						}
					}
					if push != nil {
						push("Extensions on disk", cmds)
					}
				},
			},
			{
				ID:       "extensions-reload",
				Verb:     "reload",
				Keywords: []string{"refresh", "rescan", "discover"},
				Run: func() {
					if reload != nil {
						reload()
					}
				},
			},
		},
	}
}

// ExtensionListEntries builds disabled palette rows from discovery results + warnings.
func ExtensionListEntries(found []manifest.Discovered, warns []manifest.Warning, err error) []palette.PaletteCommand {
	if err != nil {
		return []palette.PaletteCommand{{
			ID:       "extensions-list-err",
			Verb:     "error: " + err.Error(),
			Disabled: true,
		}}
	}
	out := make([]palette.PaletteCommand, 0, len(found)+len(warns)+1)
	if len(found) == 0 && len(warns) == 0 {
		out = append(out, palette.PaletteCommand{
			ID:       "extensions-list-empty",
			Verb:     "No extensions found",
			Disabled: true,
		})
		return out
	}
	for _, d := range found {
		out = append(out, palette.PaletteCommand{
			ID:       "ext-" + d.ID,
			Verb:     manifest.FormatDiscovered(d),
			Keywords: []string{d.ID, d.Source},
			Disabled: true,
		})
	}
	for i, w := range warns {
		out = append(out, palette.PaletteCommand{
			ID:       fmt.Sprintf("extensions-warn-%d", i),
			Verb:     "warn: " + w.String(),
			Keywords: []string{"warning", "error"},
			Disabled: true,
		})
	}
	return out
}
