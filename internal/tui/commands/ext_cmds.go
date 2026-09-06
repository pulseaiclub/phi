package commands

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/debuglog"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

type extComposer interface {
	SetPaletteCommands([]palette.PaletteCommand)
}

type extFooter interface {
	SetExtensionStatus(status string)
}

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
	CommandCtx func() CommandContext

	gen     atomic.Uint64
	running atomic.Bool
}

// Running reports whether an extension command is currently executing.
func (h *ExtCommands) Running() bool { return h != nil && h.running.Load() }

func (h *ExtCommands) showToast(msg string, kind toast.ToastKind) {
	if h == nil {
		return
	}
	h.Bus.Publish(controller.ToastMsg{Message: msg, Kind: kind, Duration: 3 * time.Second})
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
	ctx := CommandContext{}
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
		Run: func(ctx CommandContext) error {
			if h.running.Load() {
				ctx.toast("An extension command is already running", toast.ToastWarning, 3*time.Second)
				return nil
			}
			args := strings.TrimSpace(strings.Join(ctx.Args, " "))
			go h.run(name, args)
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
		h.showToast(msg.Err, toast.ToastError)
		return
	}
	if msg.StatusSet {
		h.Footer.SetExtensionStatus(msg.Status)
	}
	if msg.Toast != "" {
		h.showToast(msg.Toast, toast.ToastSuccess)
	}
	if msg.Submit != "" {
		if h.Submitter.IsBusy() {
			h.showToast("Cannot submit while a reply is running", toast.ToastWarning)
			return
		}
		h.Submitter.Submit(msg.Submit)
	}
}
