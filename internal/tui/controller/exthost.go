package controller

import (
	"errors"
	"strings"
	"time"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/extension/loader"
)

// This file binds the extension runner to the UI: host callbacks, hot
// reload/unload plumbing, and the session lifecycle events extensions
// observe. Runner storage itself lives in the Assembly.

func (c *EngineController) Extensions() *extension.Runner {
	return c.asm.Ext()
}

func (c *EngineController) ReloadExtensions() (loaded int, warns []loader.Warning, err error) {
	r, warns, err := c.asm.LoadExtensions()
	if err != nil {
		return 0, warns, err
	}
	c.swapExtensionRunner(r)
	c.engine.SetExtensions(r)
	if r == nil {
		return 0, warns, nil
	}
	return len(r.Loaded()), warns, nil
}

func (c *EngineController) swapExtensionRunner(r *extension.Runner) {
	if prev := c.asm.SwapExt(r); prev != nil {
		prev.Close()
	}
	c.bindExtensionHost(r)
	// Previous runner's UI state died with its subprocesses.
	c.publish(ExtSessionEffectsMsg{Status: "", StatusSet: true})
}

func (c *EngineController) ListExtensions() ([]loader.Discovered, []loader.Warning, error) {
	return loader.Discover(c.proj.Global().ExtensionsDir(), c.proj.ExtensionsDir())
}

// ReloadExtension hot-reloads a single extension by name.
func (c *EngineController) ReloadExtension(name string) error {
	r := c.asm.Ext()
	if r == nil {
		return errors.New("no extension runner loaded")
	}
	return r.ReloadExtension(name)
}

// UnloadExtension unloads a single extension by name.
func (c *EngineController) UnloadExtension(name string) error {
	r := c.asm.Ext()
	if r == nil {
		return errors.New("no extension runner loaded")
	}
	return r.UnloadExtension(name)
}

// ListExtensionStates returns all extension states.
func (c *EngineController) ListExtensionStates() map[string]extension.FiberState {
	r := c.asm.Ext()
	if r == nil {
		return nil
	}
	return r.ListExtensions()
}

// bindExtensionHost wires the UI-side host callbacks onto the runner: toasts,
// status, confirms, tool refresh, and extension-initiated user messages.
func (c *EngineController) bindExtensionHost(r *extension.Runner) {
	if r == nil {
		return
	}
	cwd, sessionID := "", ""
	if c.engine != nil {
		cwd = c.engine.SessionCwd()
		sessionID = c.engine.SessionID()
	} else {
		// NewController binds the host before the engine exists.
		cwd = c.proj.Root()
	}
	r.Bind(ext.HostOpts{
		UI: extension.BusUI{
			NotifyFn: func(message, kind string) {
				toastKind := toast.ToastSuccess
				switch strings.ToLower(kind) {
				case "warning":
					toastKind = toast.ToastWarning
				case "error":
					toastKind = toast.ToastError
				}
				c.publish(ToastMsg{Message: message, Kind: toastKind, Duration: 3 * time.Second})
			},
			SetStatusFn: func(_, text string) {
				c.publish(ExtSessionEffectsMsg{Status: text, StatusSet: true})
			},
			ConfirmFn: c.askExtConfirm,
		},
		Cwd:       cwd,
		SessionID: sessionID,
		HasUI:     true,
		// RefreshTools is called by an extension, on its own reader goroutine.
		// Rebinding the engine's tools from there would race the agent loop, so
		// it takes the same route as any other topology change: a message the
		// UI goroutine applies.
		RefreshTools: func() { c.publish(ExtTopologyChangedMsg{}) },
		SendUserMessage: func(text string) {
			go c.StartPrompt(text, nil, nil)
		},
	})

	// Topology changes (extension-initiated reload/unload) happen off the
	// UI goroutine, so they arrive as a message instead of a direct call.
	r.SetOnChange(func() { c.publish(ExtTopologyChangedMsg{}) })
}

// SyncExtensionTools re-derives the agent's tool list from the current runner.
// Call it on the UI goroutine: it rebinds the engine's client and executor, so
// an extension reader goroutine must not.
func (c *EngineController) SyncExtensionTools() {
	if c.engine == nil {
		return
	}
	c.engine.SetExtensions(c.asm.Ext())
}

func (c *EngineController) sessionBeforeSwitch(reason, fromID, targetID string) ext.SessionEffects {
	r := c.Extensions()
	if r == nil {
		return ext.SessionEffects{}
	}
	r.SetMeta(fromID, c.cwd)
	return r.EmitSessionBeforeSwitch(ext.SessionBeforeSwitchEvent{
		Reason:          reason,
		TargetSessionID: targetID,
	})
}

func (c *EngineController) sessionShutdown(reason, sessionID string) {
	r := c.Extensions()
	if r == nil {
		return
	}
	r.SetMeta(sessionID, c.cwd)
	c.publishSessionEffects(r.EmitSessionShutdown(ext.SessionShutdownEvent{Reason: reason}))
}

func (c *EngineController) emitSessionStart(reason, sessionID, previousID string) {
	r := c.Extensions()
	if r == nil {
		return
	}
	r.SetMeta(sessionID, c.cwd)
	c.publishSessionEffects(r.EmitSessionStart(ext.SessionStartEvent{
		Reason:            reason,
		PreviousSessionID: previousID,
	}))
}

func (c *EngineController) publishSessionEffects(out ext.SessionEffects) {
	if out.Toast == "" && !out.StatusSet {
		return
	}
	c.publish(ExtSessionEffectsMsg{
		Toast:     out.Toast,
		Status:    out.Status,
		StatusSet: out.StatusSet,
	})
}
