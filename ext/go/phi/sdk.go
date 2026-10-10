package phi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
)

// ExtensionAPI is the author-facing registration surface for a PXB extension binary.
type ExtensionAPI struct {
	Name    string
	Version string

	mu        sync.Mutex
	tools     []toolReg
	commands  []cmdReg
	events    []uint16
	intercept []uint16

	onToolCall            func(ext.ToolCallEvent) *ext.ToolCallResult
	onToolResult          func(ext.ToolResultEvent) *ext.ToolResultResult
	onBeforeAgentStart    func(ext.BeforeAgentStartEvent) *ext.BeforeAgentStartResult
	onSessionBeforeSwitch func(ext.SessionBeforeSwitchEvent) *ext.SessionBeforeSwitchResult
	onUserInput           func(ext.UserInputEvent) *ext.UserInputResult
	onTurnStopping        func(ext.TurnStoppingEvent) *ext.TurnStoppingResult
	onEvent               map[uint16]func(pxb.EventNotify)

	wr *pxb.Writer
	rd *pxb.Reader

	deferred      []pxb.Frame
	deferredBytes int
	shutdown      bool
	writeErr      error
	host          HelloInfo
	pendingSubmit string
	nextHostID    atomic.Uint32
}

type toolReg struct {
	def ext.Tool
}

type cmdReg struct {
	name string
	def  ext.Command
}

// HelloInfo is filled after hello_ack.
type HelloInfo struct {
	Cwd          string
	SessionID    string
	ExtensionDir string
	PhiVersion   string
}

// New constructs a module.
func New(name, version string) *ExtensionAPI {
	return &ExtensionAPI{
		Name:    name,
		Version: version,
		onEvent: make(map[uint16]func(pxb.EventNotify)),
	}
}

// Host returns metadata from the host handshake.
func (extension *ExtensionAPI) Host() HelloInfo {
	extension.mu.Lock()
	defer extension.mu.Unlock()
	return extension.host
}

// RegisterTool adds an LLM-callable tool.
func (extension *ExtensionAPI) RegisterTool(t ext.Tool) {
	if t.Name == "" || t.Execute == nil {
		return
	}
	extension.mu.Lock()
	defer extension.mu.Unlock()
	extension.tools = append(extension.tools, toolReg{def: t})
}

// RegisterCommand adds a slash command.
func (extension *ExtensionAPI) RegisterCommand(name string, cmd ext.Command) {
	if name == "" || cmd.Handler == nil {
		return
	}
	extension.mu.Lock()
	defer extension.mu.Unlock()
	extension.commands = append(extension.commands, cmdReg{name: name, def: cmd})
}

// OnToolCall registers a pre-gate intercept.
func (extension *ExtensionAPI) OnToolCall(fn func(ext.ToolCallEvent) *ext.ToolCallResult) {
	extension.mu.Lock()
	defer extension.mu.Unlock()
	extension.onToolCall = fn
	extension.intercept = appendUnique(extension.intercept, pxb.EvToolCall)
}

// OnToolResult registers a post-tool intercept.
func (extension *ExtensionAPI) OnToolResult(fn func(ext.ToolResultEvent) *ext.ToolResultResult) {
	extension.mu.Lock()
	defer extension.mu.Unlock()
	extension.onToolResult = fn
	extension.intercept = appendUnique(extension.intercept, pxb.EvToolResult)
}

// OnBeforeAgentStart may append system prompt text.
func (extension *ExtensionAPI) OnBeforeAgentStart(fn func(ext.BeforeAgentStartEvent) *ext.BeforeAgentStartResult) {
	extension.mu.Lock()
	defer extension.mu.Unlock()
	extension.onBeforeAgentStart = fn
	extension.intercept = appendUnique(extension.intercept, pxb.EvBeforeAgentStart)
}

// OnSessionBeforeSwitch may cancel a session switch.
func (extension *ExtensionAPI) OnSessionBeforeSwitch(
	fn func(ext.SessionBeforeSwitchEvent) *ext.SessionBeforeSwitchResult,
) {
	extension.mu.Lock()
	defer extension.mu.Unlock()
	extension.onSessionBeforeSwitch = fn
	extension.intercept = appendUnique(extension.intercept, pxb.EvSessionBeforeSwitch)
}

// OnUserInput may transform or swallow the user prompt before the agent loop.
func (extension *ExtensionAPI) OnUserInput(fn func(ext.UserInputEvent) *ext.UserInputResult) {
	extension.mu.Lock()
	defer extension.mu.Unlock()
	extension.onUserInput = fn
	extension.intercept = appendUnique(extension.intercept, pxb.EvUserInput)
}

// OnTurnStopping may steer another step when the model stops with no tools.
func (extension *ExtensionAPI) OnTurnStopping(fn func(ext.TurnStoppingEvent) *ext.TurnStoppingResult) {
	extension.mu.Lock()
	defer extension.mu.Unlock()
	extension.onTurnStopping = fn
	extension.intercept = appendUnique(extension.intercept, pxb.EvTurnStopping)
}

// Subscribe adds a fire-and-forget lifecycle listener (no payload).
func (extension *ExtensionAPI) Subscribe(event string, fn func()) {
	extension.SubscribeEvent(event, func(pxb.EventNotify) {
		if fn != nil {
			fn()
		}
	})
}

// SubscribeEvent adds a fire-and-forget listener with the wire payload.
func (extension *ExtensionAPI) SubscribeEvent(event string, fn func(pxb.EventNotify)) {
	code := pxb.EventCode(event)
	if code == 0 {
		return
	}
	extension.mu.Lock()
	defer extension.mu.Unlock()
	extension.events = appendUnique(extension.events, code)
	if fn != nil {
		extension.onEvent[code] = fn
	}
}

// Notify pushes a toast to the host (after Run has started).
func (extension *ExtensionAPI) Notify(level, message string) {
	if extension.wr == nil {
		return
	}
	extension.write(pxb.TypeNotify, 0, 0, pxb.EncodeNotify(pxb.NotifyMsg{Level: level, Message: message}))
}

// SetStatus updates the host footer extension status (empty clears).
func (extension *ExtensionAPI) SetStatus(text string) {
	if extension.wr == nil {
		return
	}
	extension.write(pxb.TypeNotify, 0, 0, pxb.EncodeNotify(pxb.NotifyMsg{Status: text, StatusSet: true}))
}

// Submit queues a prompt for the host to send after the current slash command returns.
func (extension *ExtensionAPI) Submit(text string) {
	extension.mu.Lock()
	extension.pendingSubmit = text
	extension.mu.Unlock()
}

// SendUserMessage asks the host to enqueue a user turn (fire-and-forget).
// Safe to call from command/tool handlers on the PXB read loop.
func (extension *ExtensionAPI) SendUserMessage(text string) {
	if extension.wr == nil || text == "" {
		return
	}
	extension.write(pxb.TypeHostRequest, 0, 0, pxb.EncodeHostRequest(pxb.HostRequest{
		Method: "send_user_message", Arg: text,
	}))
}

// Confirm shows a yes/no dialog on the host and waits for the answer.
// Must be called from a command/tool/intercept handler (nested read on the PXB loop).
func (extension *ExtensionAPI) Confirm(title, message string) bool {
	return extension.ConfirmOpts(ext.ConfirmRequest{Title: title, Message: message}).OK
}

// ConfirmOpts is Confirm with labels / danger styling.
func (extension *ExtensionAPI) ConfirmOpts(req ext.ConfirmRequest) ext.ConfirmReply {
	payload, _ := json.Marshal(req)
	res, ok := extension.hostCall("confirm", payload)
	if !ok {
		return ext.ConfirmReply{}
	}
	return ext.ConfirmReply{OK: res.OK}
}

// ShowPicker opens the host list picker — the overlay /branch and /sessions use
// — and waits for the user to pick a row or dismiss it. Must be called from a
// command/tool/intercept handler (nested read on the PXB loop).
//
// An empty Items list opens nothing and answers as dismissed, so an extension
// with nothing to offer never flashes an empty overlay.
func (extension *ExtensionAPI) ShowPicker(req ext.PickerRequest) ext.PickerReply {
	if len(req.Items) == 0 {
		return ext.PickerReply{}
	}
	payload, _ := json.Marshal(req)
	res, ok := extension.hostCall("picker", payload)
	if !ok {
		return ext.PickerReply{}
	}
	return ext.PickerReply{OK: res.OK, ID: res.Body}
}

// hostCall performs one HostRequest/HostResult round trip, deferring every
// other frame it reads. The boolean is false when no answer can arrive
// (shutdown, fatal transport error, deferred queue overflow); the handler is
// then unwound through hostCallStopped instead of resuming with a default reply.
func (extension *ExtensionAPI) hostCall(method string, payload []byte) (pxb.HostResult, bool) {
	if extension.wr == nil || extension.rd == nil || extension.stopped() {
		return pxb.HostResult{}, false
	}
	id := extension.nextHostID.Add(1)
	extension.write(pxb.TypeHostRequest, pxb.FlagHasID, id, pxb.EncodeHostRequest(pxb.HostRequest{
		Method: method, Arg: string(payload),
	}))
	if extension.stopped() {
		panic(hostCallStopped{})
	}
	for {
		fr, err := extension.rd.Read()
		if err != nil {
			extension.fail(err)
			panic(hostCallStopped{})
		}
		body := pxb.CloneBody(fr)
		switch fr.Type {
		case pxb.TypeHostResult:
			if fr.Flags&pxb.FlagHasID == 0 || fr.ID != id {
				continue
			}
			res, err := pxb.DecodeHostResult(body)
			if err != nil {
				extension.fail(err)
				panic(hostCallStopped{})
			}
			return res, true
		case pxb.TypeShutdown:
			extension.write(pxb.TypeShutdownAck, 0, 0, nil)
			extension.mu.Lock()
			extension.shutdown = true
			extension.mu.Unlock()
			panic(hostCallStopped{})
		case pxb.TypeToolInvoke, pxb.TypeToolDetailInvoke, pxb.TypeCommandInvoked, pxb.TypeIntercept,
			pxb.TypeEvent, pxb.TypeSessionMeta:
			if len(extension.deferred) >= maxDeferredFrames || len(body) > maxDeferredBytes-extension.deferredBytes {
				extension.fail(errors.New("phi: deferred request queue is full"))
				panic(hostCallStopped{})
			}
			fr.Body = body
			extension.deferred = append(extension.deferred, fr)
			extension.deferredBytes += len(body)
		}
		if extension.stopped() {
			panic(hostCallStopped{})
		}
	}
}

// Run speaks PXB on stdin/stdout until shutdown.
func (extension *ExtensionAPI) Run() error {
	return extension.run(os.Stdin, os.Stdout)
}

func (extension *ExtensionAPI) run(r io.Reader, w io.Writer) (err error) {
	defer func() {
		extension.deferred = nil
		extension.deferredBytes = 0
		if v := recover(); v != nil {
			if _, ok := v.(hostCallStopped); !ok {
				panic(v)
			}
			extension.mu.Lock()
			err = extension.writeErr
			extension.mu.Unlock()
		}
	}()
	extension.wr = pxb.NewWriter(w)
	extension.rd = pxb.NewReader(r)

	caps := uint32(0)
	extension.mu.Lock()
	if len(extension.commands) > 0 {
		caps |= pxb.CapCommands
	}
	if len(extension.tools) > 0 {
		caps |= pxb.CapTools
	}
	if len(extension.events) > 0 {
		caps |= pxb.CapEvents
	}
	if len(extension.intercept) > 0 {
		caps |= pxb.CapIntercept
	}
	hello := pxb.EncodeHello(pxb.Hello{
		Name: extension.Name, Version: extension.Version, Caps: caps, Protocol: pxb.ProtocolVersion,
	})
	extension.mu.Unlock()

	if err := extension.wr.Write(pxb.TypeHello, 0, 0, hello); err != nil {
		return err
	}

	f, err := extension.rd.Read()
	if err != nil {
		return err
	}
	if f.Type != pxb.TypeHelloAck {
		return errUnexpected("hello_ack", f.Type)
	}
	ack, err := pxb.DecodeHelloAck(pxb.CloneBody(f))
	if err != nil {
		return err
	}
	extension.mu.Lock()
	extension.host = HelloInfo{
		Cwd: ack.Cwd, SessionID: ack.SessionID,
		ExtensionDir: ack.ExtensionDir, PhiVersion: ack.PhiVersion,
	}
	tools := append([]toolReg(nil), extension.tools...)
	cmds := append([]cmdReg(nil), extension.commands...)
	events := append([]uint16(nil), extension.events...)
	intercept := append([]uint16(nil), extension.intercept...)
	extension.mu.Unlock()

	for _, t := range tools {
		schema, _ := json.Marshal(t.def.Parameters)
		reg := pxb.RegisterTool{
			Name: t.def.Name, Description: t.def.Description, SchemaJSON: schema,
			HasDetail: t.def.DetailFromArgs != nil,
			Readable:  t.def.Readable,
		}
		if t.def.TimeoutSec > 0 {
			reg.TimeoutSec = uint32(t.def.TimeoutSec) //nolint:gosec // G115: author-supplied seconds; host clamps
		}
		body := pxb.EncodeRegisterTool(reg)
		if err := extension.wr.Write(pxb.TypeRegisterTool, 0, 0, body); err != nil {
			return err
		}
	}
	for _, c := range cmds {
		body := pxb.EncodeRegisterCommand(pxb.RegisterCommand{
			Name: c.name, Description: c.def.Description, NeedsArgs: c.def.NeedsArgs,
		})
		if err := extension.wr.Write(pxb.TypeRegisterCommand, 0, 0, body); err != nil {
			return err
		}
	}
	if len(events) > 0 || len(intercept) > 0 {
		body := pxb.EncodeSubscribe(pxb.Subscribe{Events: events, Intercept: intercept})
		if err := extension.wr.Write(pxb.TypeSubscribe, 0, 0, body); err != nil {
			return err
		}
	}
	if err := extension.wr.Write(pxb.TypeReady, 0, 0, nil); err != nil {
		return err
	}

	toolByName := make(map[string]ext.Tool, len(tools))
	for _, t := range tools {
		toolByName[t.def.Name] = t.def
	}
	cmdByName := make(map[string]ext.Command, len(cmds))
	for _, c := range cmds {
		cmdByName[c.name] = c.def
	}

	for !extension.stopped() {
		fr, err := extension.nextFrame()
		if err != nil {
			return err
		}
		body := pxb.CloneBody(fr)
		switch fr.Type {
		case pxb.TypeShutdown:
			extension.write(pxb.TypeShutdownAck, 0, 0, nil)
			extension.mu.Lock()
			extension.shutdown = true
			extension.mu.Unlock()
		case pxb.TypeCommandInvoked:
			inv, err := pxb.DecodeCommandInvoked(body)
			if err != nil {
				return err
			}
			resp := pxb.CommandResponse{OK: true}
			if cmd, ok := cmdByName[inv.Name]; ok {
				if err := cmd.Handler(inv.Args, &ext.Context{
					Cwd:       extension.host.Cwd,
					SessionID: extension.host.SessionID,
					HasUI:     true,
					UI:        extensionUI{extensionAPI: extension},
				}); err != nil {
					resp.OK = false
					resp.Error = err.Error()
				}
			} else {
				resp.OK = false
				resp.Error = "unknown command"
			}
			extension.mu.Lock()
			resp.Submit = extension.pendingSubmit
			extension.pendingSubmit = ""
			extension.mu.Unlock()
			extension.write(pxb.TypeCommandResponse, fr.Flags, fr.ID, pxb.EncodeCommandResponse(resp))
		case pxb.TypeToolInvoke:
			inv, err := pxb.DecodeToolInvoke(body)
			if err != nil {
				return err
			}
			tr := pxb.ToolResultMsg{}
			if tool, ok := toolByName[inv.Name]; ok {
				res, err := tool.Execute(context.Background(), inv.Args)
				if err != nil {
					tr.IsError = true
					tr.Error = err.Error()
					tr.Content = err.Error()
				} else {
					tr.Content, tr.Detail, tr.Output, tr.Expanded = res.Content, res.Detail, res.Output, res.Expanded
				}
			} else {
				tr.IsError = true
				tr.Error = "unknown tool"
			}
			extension.write(pxb.TypeToolResult, fr.Flags, fr.ID, pxb.EncodeToolResult(tr))
		case pxb.TypeToolDetailInvoke:
			inv, err := pxb.DecodeToolInvoke(body)
			if err != nil {
				return err
			}
			var detail string
			if tool, ok := toolByName[inv.Name]; ok && tool.DetailFromArgs != nil {
				detail = tool.DetailFromArgs(inv.Args)
			}
			extension.write(
				pxb.TypeToolDetailResult,
				fr.Flags,
				fr.ID,
				pxb.EncodeToolDetailResult(pxb.ToolDetailResult{Detail: detail}),
			)
		case pxb.TypeIntercept:
			req, err := pxb.DecodeInterceptReq(body)
			if err != nil {
				return err
			}
			resp := extension.handleIntercept(req)
			extension.write(pxb.TypeInterceptResponse, fr.Flags, fr.ID, pxb.EncodeInterceptResp(resp))
		case pxb.TypeEvent:
			ev, err := pxb.DecodeEventNotify(body)
			if err != nil {
				return err
			}
			extension.mu.Lock()
			fn := extension.onEvent[ev.Event]
			extension.mu.Unlock()
			if fn != nil {
				fn(ev)
			}
		case pxb.TypeSessionMeta:
			meta, err := pxb.DecodeSessionMeta(body)
			if err != nil {
				return err
			}
			extension.mu.Lock()
			if meta.SessionID != "" {
				extension.host.SessionID = meta.SessionID
			}
			if meta.Cwd != "" {
				extension.host.Cwd = meta.Cwd
			}
			extension.mu.Unlock()
		}
	}
	extension.mu.Lock()
	defer extension.mu.Unlock()
	return extension.writeErr
}

func (extension *ExtensionAPI) handleIntercept(req pxb.InterceptReq) pxb.InterceptResp {
	switch req.Event {
	case pxb.EvToolCall:
		if extension.onToolCall == nil {
			return pxb.InterceptResp{}
		}
		r := extension.onToolCall(ext.ToolCallEvent{
			ToolName: req.ToolName, ToolCallID: req.ToolCallID, Input: req.Input,
		})
		if r == nil {
			return pxb.InterceptResp{}
		}
		return pxb.InterceptResp{Block: r.Block, Reason: r.Reason, Input: r.Input, Context: r.Context}
	case pxb.EvToolResult:
		if extension.onToolResult == nil {
			return pxb.InterceptResp{}
		}
		r := extension.onToolResult(ext.ToolResultEvent{
			ToolName: req.ToolName, ToolCallID: req.ToolCallID, Input: req.Input,
			Content: req.Content, IsError: req.IsError, Err: req.ErrText,
		})
		if r == nil {
			return pxb.InterceptResp{}
		}
		return pxb.InterceptResp{Content: r.Content, Context: r.Context, Stop: r.Stop, Reason: r.Reason}
	case pxb.EvBeforeAgentStart:
		if extension.onBeforeAgentStart == nil {
			return pxb.InterceptResp{}
		}
		r := extension.onBeforeAgentStart(ext.BeforeAgentStartEvent{Prompt: req.Prompt})
		if r == nil {
			return pxb.InterceptResp{}
		}
		return pxb.InterceptResp{SystemPromptAppend: r.SystemPromptAppend, Prompt: r.Prompt}
	case pxb.EvSessionBeforeSwitch:
		if extension.onSessionBeforeSwitch == nil {
			return pxb.InterceptResp{}
		}
		r := extension.onSessionBeforeSwitch(ext.SessionBeforeSwitchEvent{
			Reason: req.Reason, TargetSessionID: req.TargetID,
		})
		if r == nil {
			return pxb.InterceptResp{}
		}
		return pxb.InterceptResp{Cancel: r.Cancel, Reason: r.Reason, Toast: r.Toast}
	case pxb.EvUserInput:
		if extension.onUserInput == nil {
			return pxb.InterceptResp{}
		}
		r := extension.onUserInput(ext.UserInputEvent{Text: req.Prompt})
		if r == nil {
			return pxb.InterceptResp{}
		}
		return pxb.InterceptResp{Handled: r.Handled, Prompt: r.Text, Reason: r.Reason}
	case pxb.EvTurnStopping:
		if extension.onTurnStopping == nil {
			return pxb.InterceptResp{}
		}
		r := extension.onTurnStopping(ext.TurnStoppingEvent{TurnIndex: int(req.TurnIndex)})
		if r == nil {
			return pxb.InterceptResp{}
		}
		return pxb.InterceptResp{Continue: r.Continue, Prompt: r.Message, Reason: r.Reason}
	default:
		return pxb.InterceptResp{}
	}
}
