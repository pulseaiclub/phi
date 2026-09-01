package ext

import (
	"context"
	"errors"
	"maps"
	"sync"
)

// Factory is the entry point symbol every extension must export.
type Factory func(phi *API)

// API is passed to Extension(phi *ext.API). Methods collect registrations;
// action methods (GetActiveTools, Exec, …) work after the host binds them.
type API struct {
	mu sync.Mutex

	handlers map[string][]any
	tools    []Tool
	commands map[string]Command

	// Bound by host after load.
	ui           UI
	cwd          string
	sessionID    string
	hasUI        bool
	getActive    func() []string
	setActive    func([]string)
	getAll       func() []ToolInfo
	execFn       func(ctx context.Context, command string, args []string) (ExecResult, error)
	sendUserMsg  func(text string)
	refreshTools func()
}

// NewAPI builds an empty API for one extension factory invocation.
func NewAPI() *API {
	return &API{
		handlers: make(map[string][]any),
		commands: make(map[string]Command),
	}
}

// On registers an event handler. handler must match the event's expected signature
// (see doc/extensions.md). Unknown events are accepted and ignored at emit time.
func (a *API) On(event string, handler any) {
	if a == nil || event == "" || handler == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.handlers[event] = append(a.handlers[event], handler)
}

// RegisterTool adds an LLM-callable tool.
func (a *API) RegisterTool(t Tool) {
	if a == nil || t.Name == "" || t.Execute == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tools = append(a.tools, t)
}

// RegisterCommand adds a slash command (cannot override builtins at host level).
func (a *API) RegisterCommand(name string, cmd Command) {
	if a == nil || name == "" || cmd.Handler == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.commands[name]; exists {
		return
	}
	a.commands[name] = cmd
}

// Handlers returns a copy of handlers for event.
func (a *API) Handlers(event string) []any {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	hs := a.handlers[event]
	out := make([]any, len(hs))
	copy(out, hs)
	return out
}

// Tools returns registered tools.
func (a *API) Tools() []Tool {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Tool, len(a.tools))
	copy(out, a.tools)
	return out
}

// Commands returns registered slash commands.
func (a *API) Commands() map[string]Command {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return maps.Clone(a.commands)
}

// CommandEntries lists registered command names.
func (a *API) CommandEntries() []CommandEntry {
	cmds := a.Commands()
	out := make([]CommandEntry, 0, len(cmds))
	for name, c := range cmds {
		out = append(out, CommandEntry{Name: name, Description: c.Description})
	}
	return out
}

// BindHost attaches runtime actions after factories have run.
func (a *API) BindHost(opts HostOpts) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ui = opts.UI
	a.cwd = opts.Cwd
	a.sessionID = opts.SessionID
	a.hasUI = opts.HasUI
	a.getActive = opts.GetActiveTools
	a.setActive = opts.SetActiveTools
	a.getAll = opts.GetAllTools
	a.execFn = opts.Exec
	a.sendUserMsg = opts.SendUserMessage
	a.refreshTools = opts.RefreshTools
}

// HostOpts wires host capabilities into API action methods.
type HostOpts struct {
	UI              UI
	Cwd             string
	SessionID       string
	HasUI           bool
	GetActiveTools  func() []string
	SetActiveTools  func([]string)
	GetAllTools     func() []ToolInfo
	Exec            func(ctx context.Context, command string, args []string) (ExecResult, error)
	SendUserMessage func(text string)
	RefreshTools    func()
}

// NewContext builds a Context for handlers.
func (a *API) NewContext() *Context {
	if a == nil {
		return &Context{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return &Context{
		Cwd:       a.cwd,
		SessionID: a.sessionID,
		HasUI:     a.hasUI,
		UI:        a.ui,
	}
}

// GetActiveTools returns currently active tool names.
func (a *API) GetActiveTools() []string {
	if a == nil || a.getActive == nil {
		return nil
	}
	return a.getActive()
}

// SetActiveTools sets the active tool set by name.
func (a *API) SetActiveTools(names []string) {
	if a == nil || a.setActive == nil {
		return
	}
	a.setActive(names)
	if a.refreshTools != nil {
		a.refreshTools()
	}
}

// GetAllTools returns all configured tools.
func (a *API) GetAllTools() []ToolInfo {
	if a == nil || a.getAll == nil {
		return nil
	}
	return a.getAll()
}

// Exec runs a command via the host shell helper.
func (a *API) Exec(ctx context.Context, command string, args []string) (ExecResult, error) {
	if a == nil || a.execFn == nil {
		return ExecResult{}, errors.New("ext: Exec not bound")
	}
	return a.execFn(ctx, command, args)
}

// SendUserMessage queues a user message (triggers a turn when host supports it).
func (a *API) SendUserMessage(text string) {
	if a == nil || a.sendUserMsg == nil {
		return
	}
	a.sendUserMsg(text)
}
