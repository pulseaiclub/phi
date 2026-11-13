package commands

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pulseaiclub/phi/internal/extension/loader"

	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/debuglog"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// extComposer updates the palette command list.
type extComposer interface {
	SetPaletteCommands([]palette.PaletteCommand)
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
	SetStatus  func(string)
	Submitter  extSubmitter
	Bus        *controller.Bus
	CommandCtx Context

	gen     atomic.Uint64
	running atomic.Bool
}

// Sync replaces extension-sourced slash commands from the current Runner.
func (h *ExtCommands) Sync() {
	h.gen.Add(1)
	h.Registry.clearExtCommands()
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
	h.RefreshPalette()
}

// RefreshPalette rebuilds commands from live settings without resyncing extensions.
func (h *ExtCommands) RefreshPalette() {
	h.Composer.SetPaletteCommands(h.Registry.BuildPalette(h.CommandCtx))
}

func (h *ExtCommands) slashCommand(name, desc string, needsArgs bool) Command {
	return Command{
		Name:        name,
		Description: desc,
		Slash:       true,
		NeedsArgs:   needsArgs,
		Run: func(ctx Context, args []string) {
			if h.running.Load() {
				ctx.Toast("An extension command is already running", toast.ToastWarning, 3*time.Second)
				return
			}
			text := strings.TrimSpace(strings.Join(args, " "))
			go h.run(name, text)
		},
	}
}

func (h *ExtCommands) run(name, args string) {
	if !h.running.CompareAndSwap(false, true) {
		h.Bus.Publish(controller.ExtCommandResultMsg{
			Gen: h.gen.Load(),
			Err: "An extension command is already running",
		})
		return
	}
	defer h.running.Store(false)

	gen := h.gen.Load()
	// A nil extension runner is legitimate runtime state (nothing loaded yet).
	if h.Ctrl.Extensions() == nil {
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
	if msg.Gen != h.gen.Load() {
		return
	}
	if msg.Err != "" {
		publishToast(h.Bus, msg.Err, toast.ToastError, 3*time.Second)
		return
	}
	if msg.StatusSet && h.SetStatus != nil {
		h.SetStatus(msg.Status)
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
	r.Register(Command{
		Name: "extensions",
		Build: func(ctx Context) palette.PaletteCommand {
			var push func(string, []palette.PaletteCommand)
			if ctx != nil {
				push = ctx.PushSubmenu
			}
			return h.buildExtensionsPalette(push)
		},
	})
}

// Reload rescans extensions and refreshes the slash/palette surface.
func (h *ExtCommands) Reload() {
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

// ReloadOne hot-reloads a single extension by name.
func (h *ExtCommands) ReloadOne(name string) {
	if err := h.Ctrl.ReloadExtension(name); err != nil {
		publishToast(h.Bus, "Failed to reload "+name+": "+err.Error(), toast.ToastError, 3*time.Second)
		return
	}
	h.Sync()
	publishToast(h.Bus, "Reloaded extension: "+name, toast.ToastSuccess, 2*time.Second)
}

// UnloadOne unloads a single extension by name.
func (h *ExtCommands) UnloadOne(name string) {
	if err := h.Ctrl.UnloadExtension(name); err != nil {
		publishToast(h.Bus, "Failed to unload "+name+": "+err.Error(), toast.ToastError, 3*time.Second)
		return
	}
	h.Sync()
	publishToast(h.Bus, "Unloaded extension: "+name, toast.ToastSuccess, 2*time.Second)
}

// ListStates returns extension names with their current states.
func (h *ExtCommands) ListStates() map[string]string {
	states := h.Ctrl.ListExtensionStates()
	result := make(map[string]string, len(states))
	for name, state := range states {
		result[name] = state.String()
	}
	return result
}

func (h *ExtCommands) buildExtensionsPalette(
	push func(string, []palette.PaletteCommand),
) palette.PaletteCommand {
	return ExtensionsPalette(h.buildExtensionList, h.Reload, push)
}

// ExtensionsPalette builds the "extensions" palette row from its actions, which
// keeps the row testable without a controller.
func ExtensionsPalette(
	list func() []palette.PaletteCommand,
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
					cmds := list()
					if push != nil {
						push("Extensions", cmds)
					}
				},
			},
			{
				ID:       "extensions-reload",
				Verb:     "reload all",
				Keywords: []string{"refresh", "rescan", "discover"},
				Run:      reload,
			},
		},
	}
}

func (h *ExtCommands) buildExtensionList() []palette.PaletteCommand {
	found, warns, err := h.Ctrl.ListExtensions()
	states := h.ListStates()

	if err != nil {
		return []palette.PaletteCommand{{
			ID:       "extensions-list-err",
			Verb:     "error: " + err.Error(),
			Disabled: true,
		}}
	}

	if len(found) == 0 && len(warns) == 0 {
		return []palette.PaletteCommand{{
			ID:       "extensions-list-empty",
			Verb:     "No extensions found",
			Disabled: true,
		}}
	}

	out := make([]palette.PaletteCommand, 0, len(found)+len(warns))

	// List extensions with state and actions
	for _, d := range found {
		state := "unknown"
		if s, ok := states[d.Manifest.Name]; ok {
			state = s
		}

		verb := fmt.Sprintf("%s [%s]", loader.FormatDiscovered(d), state)
		if d.Manifest.Enabled != nil && !*d.Manifest.Enabled {
			verb += " (disabled)"
		}

		extCmd := palette.PaletteCommand{
			ID:           "ext-" + d.ID,
			Verb:         verb,
			Keywords:     []string{d.ID, d.Source, state},
			SubmenuTitle: d.Manifest.Name,
			Submenu: []palette.PaletteCommand{
				{
					ID:       fmt.Sprintf("ext-%s-reload", d.ID),
					Verb:     "reload",
					Keywords: []string{"hot", "swap", "restart"},
					Run: func() {
						name := d.Manifest.Name
						h.ReloadOne(name)
					},
				},
				{
					ID:       fmt.Sprintf("ext-%s-unload", d.ID),
					Verb:     "unload",
					Keywords: []string{"stop", "remove", "disable"},
					Run: func() {
						name := d.Manifest.Name
						h.UnloadOne(name)
					},
				},
			},
		}
		out = append(out, extCmd)
	}

	// Add warnings
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
