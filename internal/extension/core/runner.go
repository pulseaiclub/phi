package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/pulseaiclub/phi/internal/extension/loader"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/debuglog"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
	"github.com/pulseaiclub/phi/internal/util"
)

const maxContextBytes = 4 * 1024

// NewRunner returns an empty runner rooted at logDir (where extension child
// logs are written). Fibers are added by the lifecycle package.
func NewRunner(logDir string) *Runner {
	return &Runner{
		fibers: make(map[string]*Fiber),
		logDir: logDir,
	}
}

// AddFiber registers an already-loaded fiber. Used by the lifecycle package
// after a successful start; the caller owns the handoff.
func (r *Runner) AddFiber(f *Fiber) {
	if r == nil || f == nil {
		return
	}
	r.mu.Lock()
	r.fibers[f.ID] = f
	r.invalidateLocked()
	r.mu.Unlock()
	r.notifyChange()
}

// Runner holds loaded extensions and dispatches events.
// A nil *Runner is safe and is a no-op.
//
// mu guards the topology (fibers, warns, host opts, tool config) and the cached
// read model in view. Mutating operations that block on a subprocess (start,
// close, handshake) hold only the fiber's own operation lock, never mu — the
// write paths below snapshot what they need and do the I/O outside the lock.
type Runner struct {
	mu      sync.RWMutex
	fibers  map[string]*Fiber // key: extension name
	view    *model            // cached read model; nil = rebuild from fibers
	ui      ext.UI
	cwd     string
	session string
	hasUI   bool
	logDir  string

	baseTools   []tooldef.Tool
	activeNames map[string]bool // nil = all active
	host        ext.HostOpts

	onChange atomic.Pointer[func()]
}

// SetOnChange registers a hook fired after the extension topology changes —
// load, unload or hot swap. It runs on whichever goroutine performed the
// change (a UI action) and must not block: hosts use it to re-derive tool
// schemas and refresh the UI.
func (r *Runner) SetOnChange(fn func()) {
	if r == nil {
		return
	}
	if fn == nil {
		r.onChange.Store(nil)
		return
	}
	r.onChange.Store(&fn)
}

// notifyChange runs the topology hook, if any.
func (r *Runner) notifyChange() {
	if r == nil {
		return
	}
	if p := r.onChange.Load(); p != nil && *p != nil {
		(*p)()
	}
}

// Close shuts down every extension subprocess.
func (r *Runner) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	fibers := r.fiberListLocked()
	r.fibers = nil
	r.invalidateLocked()
	r.mu.Unlock()
	for _, f := range fibers {
		_ = f.Unload()
	}
}

// Loaded returns discovered extensions that were loaded, in name order.
func (r *Runner) Loaded() []loader.Discovered {
	if r == nil {
		return nil
	}
	names := r.Names()
	out := make([]loader.Discovered, 0, len(names))
	for _, name := range names {
		if f := r.Fiber(name); f != nil {
			out = append(out, f.Discovered)
		}
	}
	return out
}

// Bind attaches host capabilities to every extension API.
func (r *Runner) Bind(opts ext.HostOpts) {
	if r == nil {
		return
	}
	if opts.GetActiveTools == nil {
		opts.GetActiveTools = func() []string {
			return toolNames(r.snapshot().activeTools)
		}
	}
	if opts.SetActiveTools == nil {
		opts.SetActiveTools = r.SetActiveTools
	}
	if opts.GetAllTools == nil {
		opts.GetAllTools = func() []ext.ToolInfo {
			return r.snapshot().tools
		}
	}

	r.mu.Lock()
	r.ui = opts.UI
	r.cwd = opts.Cwd
	r.session = opts.SessionID
	r.hasUI = opts.HasUI
	r.host = opts
	fibers := r.fiberListLocked()
	r.mu.Unlock()

	for _, f := range fibers {
		r.wire(f, opts)
	}
}

// wire connects the host side of one fiber: the API receives the host opts and
// the process receives the frame sink that forwards Notify / host requests.
// Every path that produces a live process — initial load, hot swap, resume —
// funnels through here, so a swapped-in process can never come up unwired.
func (r *Runner) wire(f *Fiber, opts ext.HostOpts) {
	if f == nil {
		return
	}
	if api := f.API(); api != nil {
		api.BindHost(opts)
	}
	if proc := f.Proc(); proc != nil {
		proc.SetHostSink(procSink{runner: r, proc: proc, ui: opts.UI, sendUser: opts.SendUserMessage})
	}
}

// hostOpts snapshots the bound host options.
func (r *Runner) hostOpts() ext.HostOpts {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.host
}

// SetActiveTools restricts the tool set exposed to the agent (nil = all).
func (r *Runner) SetActiveTools(names []string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activeNames = make(map[string]bool, len(names))
	for _, n := range names {
		r.activeNames[n] = true
	}
	r.invalidateLocked()
}

func toolNames(infos []ext.ToolInfo) []string {
	out := make([]string, len(infos))
	for i, info := range infos {
		out[i] = info.Name
	}
	return out
}

// procSink is the sink Proc uses to hand spontaneous frames back to the
// host: notifications turn into UI effects, host requests get answered.
type procSink struct {
	runner   *Runner
	proc     *Proc
	ui       ext.UI
	sendUser func(string)
}

func (s procSink) Notify(n pxb.NotifyMsg) {
	if s.ui == nil {
		return
	}
	if n.Message != "" {
		kind := n.Level
		if kind == "" {
			kind = "info"
		}
		s.ui.Notify(n.Message, kind)
	}
	if n.StatusSet {
		s.ui.SetStatus("", n.Status)
	}
}

func (s procSink) HostRequest(id uint32, hasID bool, req pxb.HostRequest) {
	s.runner.handleHostRequest(s.proc, id, hasID, req, s.ui, s.sendUser)
}

func (*Runner) handleHostRequest(
	p *Proc,
	id uint32,
	hasID bool,
	req pxb.HostRequest,
	ui ext.UI,
	sendUser func(string),
) {
	switch req.Method {
	case "send_user_message":
		if sendUser != nil && req.Arg != "" {
			sendUser(req.Arg)
		}
		if hasID {
			p.ReplyHost(id, pxb.HostResult{OK: true})
		}
	case "confirm":
		go func() {
			reply := ext.ConfirmReply{}
			if ui != nil {
				var cr ext.ConfirmRequest
				if req.Arg != "" {
					_ = json.Unmarshal([]byte(req.Arg), &cr)
				}
				if cr.Title == "" && cr.Message == "" {
					cr.Message = req.Arg
				}
				reply = ui.ConfirmOpts(cr)
			}
			if hasID {
				p.ReplyHost(id, pxb.HostResult{OK: reply.OK})
			}
		}()
	default:
		debuglog.Logf("extension: unknown host request %q", req.Method)
		if hasID {
			p.ReplyHost(id, pxb.HostResult{OK: false, Error: "unknown method"})
		}
	}
}

// SetMeta updates cwd/session on bound APIs and pushes to subprocesses.
func (r *Runner) SetMeta(sessionID, cwd string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.session = sessionID
	r.cwd = cwd
	r.host.SessionID = sessionID
	r.host.Cwd = cwd
	opts := r.host
	fibers := r.fiberListLocked()
	r.mu.Unlock()

	// Pipe writes stay outside the lock: a wedged child must not stall the
	// read paths.
	for _, f := range fibers {
		r.wire(f, opts)
		if proc := f.Proc(); proc != nil {
			proc.PushSessionMeta(sessionID, cwd)
		}
	}
}

// SetBaseTools records built-in tools for GetAllTools / active filtering.
func (r *Runner) SetBaseTools(base []tooldef.Tool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.baseTools = append([]tooldef.Tool(nil), base...)
	r.invalidateLocked()
}

// ExtensionTools converts registered extension tools to tooldef.Tool.
// The result is cached and must be treated as read-only.
func (r *Runner) ExtensionTools() []tooldef.Tool {
	if r == nil {
		return nil
	}
	return r.snapshot().extTools
}

func toolFromDef(def ext.Tool) tooldef.Tool {
	params := schemaFromMap(def.Parameters)
	exec := def.Execute
	return tooldef.Tool{
		Definition: llm.ToolDefinition{
			Name:        def.Name,
			Description: def.Description,
			Params:      params,
			Readable:    def.Readable,
		},
		DetailFromArgs: def.DetailFromArgs,
		Run: func(ctx context.Context, input json.RawMessage) (tooldef.Result, error) {
			res, err := exec(ctx, input)
			if err != nil {
				return tooldef.Result{}, err
			}
			out := res.Output
			if out == "" {
				out = res.Content
			}
			return tooldef.Result{Content: res.Content, Detail: res.Detail, Output: out, Expanded: res.Expanded}, nil
		},
	}
}

func schemaFromMap(m map[string]any) *llm.FunctionParameters {
	if m == nil {
		return &llm.FunctionParameters{Type: "object", Properties: llm.Object{}}
	}
	fp := &llm.FunctionParameters{Type: "object", Properties: llm.Object{}}
	if t, ok := m["type"].(string); ok && t != "" {
		fp.Type = t
	}
	if props, ok := m["properties"].(map[string]any); ok {
		fp.Properties = props
	}
	switch req := m["required"].(type) {
	case []string:
		fp.Required = req
	case []any:
		for _, v := range req {
			if s, ok := v.(string); ok {
				fp.Required = append(fp.Required, s)
			}
		}
	}
	return fp
}

// CommandEntries lists slash commands from all extensions.
// The result is cached and must be treated as read-only.
func (r *Runner) CommandEntries() []ext.CommandEntry {
	if r == nil {
		return nil
	}
	return r.snapshot().commands
}

// CommandOutcome is returned from RunCommand for host UI side effects.
type CommandOutcome struct {
	Submit string
}

// RunCommand invokes a registered slash command.
func (r *Runner) RunCommand(name, args string) (CommandOutcome, error) {
	if r == nil {
		return CommandOutcome{}, errors.New("extension: no runner")
	}
	fibers := r.fiberList()
	for _, f := range fibers {
		proc := f.Proc()
		if proc != nil && proc.HasCommand(name) {
			resp, err := proc.CallCommand(context.Background(), name, args)
			if err != nil {
				return CommandOutcome{}, err
			}
			if !resp.OK {
				if resp.Error != "" {
					return CommandOutcome{}, errors.New(resp.Error)
				}
				return CommandOutcome{}, fmt.Errorf("extension command %q failed", name)
			}
			return CommandOutcome{Submit: resp.Submit}, nil
		}
	}

	for _, f := range fibers {
		api := f.API()
		if api == nil {
			continue
		}
		cmds := api.Commands()
		cmd, ok := cmds[name]
		if !ok {
			continue
		}
		return CommandOutcome{}, cmd.Handler(args, api.NewContext())
	}
	return CommandOutcome{}, fmt.Errorf("extension: command %q not found", name)
}

// PreTool runs tool_call handlers serially. First block wins; input rewrites chain.
func (r *Runner) PreTool(
	ctx context.Context,
	toolName, toolCallID string,
	input json.RawMessage,
) (json.RawMessage, bool, string, string) {
	if r == nil {
		return input, false, "", ""
	}
	ev := ext.ToolCallEvent{ToolName: toolName, ToolCallID: toolCallID, Input: input}
	var contexts []string
	for _, h := range r.handlers(ext.EventToolCall) {
		if ctx.Err() != nil {
			break
		}
		res := callToolCall(h, ev, r.context())
		if res == nil {
			continue
		}
		if len(res.Input) > 0 {
			ev.Input = res.Input
		}
		if res.Context != "" {
			contexts = append(contexts, res.Context)
		}
		if res.Block {
			return ev.Input, true, res.Reason, util.TruncateBytes(strings.Join(contexts, "\n"), maxContextBytes, "")
		}
	}
	return ev.Input, false, "", util.TruncateBytes(strings.Join(contexts, "\n"), maxContextBytes, "")
}

// PostTool runs tool_result handlers.
func (r *Runner) PostTool(
	ctx context.Context,
	toolName, toolCallID string,
	input json.RawMessage,
	content string,
	isError bool,
	errText string,
) (string, string, bool, string) {
	if r == nil {
		return content, "", false, ""
	}
	ev := ext.ToolResultEvent{
		ToolName:   toolName,
		ToolCallID: toolCallID,
		Input:      input,
		Content:    content,
		IsError:    isError,
		Err:        errText,
	}
	var (
		contexts []string
		stop     bool
		reason   string
		out      = content
	)
	for _, h := range r.handlers(ext.EventToolResult) {
		if ctx.Err() != nil {
			break
		}
		res := callToolResult(h, ev, r.context())
		if res == nil {
			continue
		}
		if res.Content != "" {
			out = res.Content
			ev.Content = out
		}
		if res.Context != "" {
			contexts = append(contexts, res.Context)
		}
		if res.Stop {
			stop = true
			reason = res.Reason
		}
	}
	return out, util.TruncateBytes(strings.Join(contexts, "\n"), maxContextBytes, ""), stop, reason
}

// EmitSessionStart notifies session_start handlers.
func (r *Runner) EmitSessionStart(ev ext.SessionStartEvent) ext.SessionEffects {
	return r.emitSession(ext.EventSessionStart, ev)
}

// EmitSessionShutdown notifies session_shutdown handlers.
func (r *Runner) EmitSessionShutdown(ev ext.SessionShutdownEvent) ext.SessionEffects {
	return r.emitSession(ext.EventSessionShutdown, ev)
}

// EmitSessionBeforeSwitch runs before_switch; cancel if any handler cancels.
func (r *Runner) EmitSessionBeforeSwitch(ev ext.SessionBeforeSwitchEvent) ext.SessionEffects {
	if r == nil {
		return ext.SessionEffects{}
	}
	var effects ext.SessionEffects
	for _, h := range r.handlers(ext.EventSessionBeforeSwitch) {
		res := callSessionBeforeSwitch(h, ev, r.context())
		if res == nil {
			continue
		}
		if res.Toast != "" {
			effects.Toast = res.Toast
		}
		if res.Cancel {
			effects.Denied = true
			effects.Reason = res.Reason
			return effects
		}
	}
	return effects
}

func (r *Runner) emitSession(event string, payload any) ext.SessionEffects {
	if r == nil {
		return ext.SessionEffects{}
	}
	var effects ext.SessionEffects
	for _, h := range r.handlers(event) {
		callNotify(h, payload, r.context())
	}
	return effects
}

// EmitAgentStart / EmitAgentEnd / EmitTurn* / EmitToolExecution* are fire-and-forget.
func (r *Runner) EmitAgentStart() {
	r.emitNotify(ext.EventAgentStart, ext.AgentStartEvent{})
}

func (r *Runner) EmitAgentEnd() {
	r.emitNotify(ext.EventAgentEnd, ext.AgentEndEvent{})
}

func (r *Runner) EmitTurnStart(idx int) {
	r.emitNotify(ext.EventTurnStart, ext.TurnStartEvent{TurnIndex: idx})
}

func (r *Runner) EmitTurnEnd(idx int) {
	r.emitNotify(ext.EventTurnEnd, ext.TurnEndEvent{TurnIndex: idx})
}

func (r *Runner) EmitToolExecutionStart(toolName, id string, args json.RawMessage) {
	r.emitNotify(
		ext.EventToolExecutionStart,
		ext.ToolExecutionStartEvent{ToolName: toolName, ToolCallID: id, Args: args},
	)
}

func (r *Runner) EmitToolExecutionEnd(toolName, id string, isError bool) {
	r.emitNotify(
		ext.EventToolExecutionEnd,
		ext.ToolExecutionEndEvent{ToolName: toolName, ToolCallID: id, IsError: isError},
	)
}

// EmitBeforeAgentStart runs before_agent_start; returns prompt rewrite + append text.
func (r *Runner) EmitBeforeAgentStart(prompt string) (newPrompt, appendText string) {
	if r == nil {
		return prompt, ""
	}
	ev := ext.BeforeAgentStartEvent{Prompt: prompt}
	out := prompt
	var appends []string
	for _, h := range r.handlers(ext.EventBeforeAgentStart) {
		res := callBeforeAgentStart(h, ev, r.context())
		if res == nil {
			continue
		}
		if res.Prompt != "" {
			out = res.Prompt
			ev.Prompt = out
		}
		if res.SystemPromptAppend != "" {
			appends = append(appends, res.SystemPromptAppend)
		}
	}
	return out, strings.Join(appends, "\n")
}

// EmitUserInput runs user_input intercepts. handled=true means skip the agent loop.
func (r *Runner) EmitUserInput(text string) (out string, handled bool) {
	if r == nil {
		return text, false
	}
	ev := ext.UserInputEvent{Text: text}
	out = text
	for _, h := range r.handlers(ext.EventUserInput) {
		res := callUserInput(h, ev, r.context())
		if res == nil {
			continue
		}
		if res.Text != "" {
			out = res.Text
			ev.Text = out
		}
		if res.Handled {
			return out, true
		}
	}
	return out, false
}

// EmitTurnStopping asks extensions whether to steer another step.
func (r *Runner) EmitTurnStopping(idx int) (continueTurn bool, message string) {
	if r == nil {
		return false, ""
	}
	ev := ext.TurnStoppingEvent{TurnIndex: idx}
	for _, h := range r.handlers(ext.EventTurnStopping) {
		res := callTurnStopping(h, ev, r.context())
		if res == nil {
			continue
		}
		if res.Continue {
			return true, res.Message
		}
	}
	return false, ""
}

// EmitSessionCompact notifies listeners that compaction ran.
func (r *Runner) EmitSessionCompact(reason string) {
	r.emitNotify(ext.EventSessionCompact, ext.SessionCompactEvent{Reason: reason})
}

func (r *Runner) emitNotify(event string, payload any) {
	if r == nil {
		return
	}
	for _, h := range r.handlers(event) {
		callNotify(h, payload, r.context())
	}
}

// handlers returns the cached handler list for event. Registration is
// write-once per extension and the cache is dropped on every topology change,
// so this is a map lookup instead of a walk over all fibers.
func (r *Runner) handlers(event string) []any {
	if r == nil {
		return nil
	}
	return r.snapshot().handlers[event]
}

func (r *Runner) context() *ext.Context {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return &ext.Context{Cwd: r.cwd, SessionID: r.session, HasUI: r.hasUI, UI: r.ui}
}

func callToolCall(h any, ev ext.ToolCallEvent, ctx *ext.Context) *ext.ToolCallResult {
	switch fn := h.(type) {
	case func(ext.ToolCallEvent, *ext.Context) *ext.ToolCallResult:
		return fn(ev, ctx)
	case func(ext.ToolCallEvent, *ext.Context) (*ext.ToolCallResult, error):
		res, err := fn(ev, ctx)
		if err != nil {
			debuglog.Logf("extension: tool_call: %v", err)
		}
		return res
	default:
		return callViaReflect[ext.ToolCallResult](h, ev, ctx)
	}
}

func callToolResult(h any, ev ext.ToolResultEvent, ctx *ext.Context) *ext.ToolResultResult {
	switch fn := h.(type) {
	case func(ext.ToolResultEvent, *ext.Context) *ext.ToolResultResult:
		return fn(ev, ctx)
	default:
		return callViaReflect[ext.ToolResultResult](h, ev, ctx)
	}
}

func callSessionBeforeSwitch(h any, ev ext.SessionBeforeSwitchEvent, ctx *ext.Context) *ext.SessionBeforeSwitchResult {
	switch fn := h.(type) {
	case func(ext.SessionBeforeSwitchEvent, *ext.Context) *ext.SessionBeforeSwitchResult:
		return fn(ev, ctx)
	default:
		return callViaReflect[ext.SessionBeforeSwitchResult](h, ev, ctx)
	}
}

func callBeforeAgentStart(h any, ev ext.BeforeAgentStartEvent, ctx *ext.Context) *ext.BeforeAgentStartResult {
	switch fn := h.(type) {
	case func(ext.BeforeAgentStartEvent, *ext.Context) *ext.BeforeAgentStartResult:
		return fn(ev, ctx)
	default:
		return callViaReflect[ext.BeforeAgentStartResult](h, ev, ctx)
	}
}

func callUserInput(h any, ev ext.UserInputEvent, ctx *ext.Context) *ext.UserInputResult {
	switch fn := h.(type) {
	case func(ext.UserInputEvent, *ext.Context) *ext.UserInputResult:
		return fn(ev, ctx)
	default:
		return callViaReflect[ext.UserInputResult](h, ev, ctx)
	}
}

func callTurnStopping(h any, ev ext.TurnStoppingEvent, ctx *ext.Context) *ext.TurnStoppingResult {
	switch fn := h.(type) {
	case func(ext.TurnStoppingEvent, *ext.Context) *ext.TurnStoppingResult:
		return fn(ev, ctx)
	default:
		return callViaReflect[ext.TurnStoppingResult](h, ev, ctx)
	}
}

func callNotify(h, payload any, ctx *ext.Context) {
	v := reflect.ValueOf(h)
	if v.Kind() != reflect.Func {
		return
	}
	t := v.Type()
	if t.NumIn() < 2 {
		return
	}
	args := []reflect.Value{reflect.ValueOf(payload), reflect.ValueOf(ctx)}
	// Coerce payload type if needed.
	if args[0].Type() != t.In(0) && args[0].Type().ConvertibleTo(t.In(0)) {
		args[0] = args[0].Convert(t.In(0))
	}
	if args[0].Type() != t.In(0) {
		// Build zero of expected and try assign from interface.
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			debuglog.Logf("extension: handler panic: %v", rec)
		}
	}()
	v.Call(args)
}

func callViaReflect[T any](h, payload any, ctx *ext.Context) *T {
	v := reflect.ValueOf(h)
	if v.Kind() != reflect.Func {
		return nil
	}
	defer func() {
		if rec := recover(); rec != nil {
			debuglog.Logf("extension: handler panic: %v", rec)
		}
	}()
	outs := v.Call([]reflect.Value{reflect.ValueOf(payload), reflect.ValueOf(ctx)})
	if len(outs) == 0 || !outs[0].IsValid() || outs[0].IsNil() {
		return nil
	}
	if res, ok := reflect.TypeAssert[*T](outs[0]); ok {
		return res
	}
	return nil
}
