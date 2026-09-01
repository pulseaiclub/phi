package pxb

// Typed payloads use tagged fields (see fields.go). Evolution rules:
//
//   - Assign a new tag number for every new field; never reuse a tag.
//   - Decoders must Skip unknown tags (Walk does this by default).
//   - Omitting a tag means the zero value (empty string / nil / false / 0).
//   - Event codes (Ev*) are append-only; never reuse a code.
//   - Incompatible renames bump ProtocolVersion and refuse old peers.

// Field tags are namespaced per message type. Ranges:
//
//	1–63   message-defined fields
//	64–127 reserved (cross-cutting; do not use in app messages yet)
//	128+   experimental / private (must still be skippable)

const (
	fHelloName     uint16 = 1
	fHelloVersion  uint16 = 2
	fHelloCaps     uint16 = 3
	fHelloProtocol uint16 = 4

	fAckProtocol   uint16 = 1
	fAckPhiVersion uint16 = 2
	fAckCwd        uint16 = 3
	fAckSessionID  uint16 = 4
	fAckExtDir     uint16 = 5

	fRegCmdName uint16 = 1
	fRegCmdDesc uint16 = 2

	fRegToolName   uint16 = 1
	fRegToolDesc   uint16 = 2
	fRegToolSchema uint16 = 3

	fSubEvents    uint16 = 1
	fSubIntercept uint16 = 2

	fCmdInvName uint16 = 1
	fCmdInvArgs uint16 = 2

	fCmdResOK     uint16 = 1
	fCmdResError  uint16 = 2
	fCmdResNotify uint16 = 3
	fCmdResSubmit uint16 = 4

	fToolInvName uint16 = 1
	fToolInvArgs uint16 = 2

	fToolResContent uint16 = 1
	fToolResDetail  uint16 = 2
	fToolResOutput  uint16 = 3
	fToolResIsError uint16 = 4
	fToolResError   uint16 = 5

	fIxReqEvent      uint16 = 1
	fIxReqToolName   uint16 = 2
	fIxReqToolCallID uint16 = 3
	fIxReqInput      uint16 = 4
	fIxReqContent    uint16 = 5
	fIxReqIsError    uint16 = 6
	fIxReqErrText    uint16 = 7
	fIxReqPrompt     uint16 = 8
	fIxReqReason     uint16 = 9
	fIxReqTargetID   uint16 = 10
	fIxReqTurnIndex  uint16 = 11

	fIxResBlock     uint16 = 1
	fIxResStop      uint16 = 2
	fIxResCancel    uint16 = 3
	fIxResReason    uint16 = 4
	fIxResInput     uint16 = 5
	fIxResContent   uint16 = 6
	fIxResContext   uint16 = 7
	fIxResSysAppend uint16 = 8
	fIxResToast     uint16 = 9
	fIxResHandled   uint16 = 10
	fIxResPrompt    uint16 = 11
	fIxResContinue  uint16 = 12

	fEvEvent             uint16 = 1
	fEvToolName          uint16 = 2
	fEvToolCallID        uint16 = 3
	fEvInput             uint16 = 4
	fEvIsError           uint16 = 5
	fEvPrompt            uint16 = 6
	fEvReason            uint16 = 7
	fEvTurnIndex         uint16 = 8
	fEvSessionID         uint16 = 9
	fEvPreviousSessionID uint16 = 10
	fEvTargetSessionID   uint16 = 11

	fNotifyLevel     uint16 = 1
	fNotifyMessage   uint16 = 2
	fNotifyStatus    uint16 = 3
	fNotifyStatusSet uint16 = 4

	fHostReqMethod uint16 = 1
	fHostReqArg    uint16 = 2

	fHostResOK    uint16 = 1
	fHostResError uint16 = 2
	fHostResBody  uint16 = 3

	fMetaSessionID uint16 = 1
	fMetaCwd       uint16 = 2
)

// Hello is the first frame from an extension.
type Hello struct {
	Name     string
	Version  string
	Caps     uint32
	Protocol uint16
}

func EncodeHello(h Hello) []byte {
	var fw FieldWriter
	fw.PutString(fHelloName, h.Name)
	fw.PutString(fHelloVersion, h.Version)
	fw.PutU32(fHelloCaps, h.Caps)
	fw.PutU16(fHelloProtocol, h.Protocol)
	return fw.Bytes()
}

func DecodeHello(b []byte) (Hello, error) {
	var h Hello
	err := Walk(b, func(tag uint16, kind uint8, fr *FieldReader) error {
		switch tag {
		case fHelloName:
			s, err := takeString(kind, fr)
			h.Name = s
			return err
		case fHelloVersion:
			s, err := takeString(kind, fr)
			h.Version = s
			return err
		case fHelloCaps:
			v, err := takeU64(kind, fr)
			h.Caps = uint32(v) //nolint:gosec // G115: caps bitmask is u32 by protocol
			return err
		case fHelloProtocol:
			v, err := takeU64(kind, fr)
			h.Protocol = uint16(v) //nolint:gosec // G115: protocol version is u16 by protocol
			return err
		default:
			return fr.Skip(kind)
		}
	})
	return h, err
}

// HelloAck is the host reply to Hello.
type HelloAck struct {
	Protocol     uint16
	PhiVersion   string
	Cwd          string
	SessionID    string
	ExtensionDir string
}

func EncodeHelloAck(h HelloAck) []byte {
	var fw FieldWriter
	fw.PutU16(fAckProtocol, h.Protocol)
	fw.PutString(fAckPhiVersion, h.PhiVersion)
	fw.PutString(fAckCwd, h.Cwd)
	fw.PutString(fAckSessionID, h.SessionID)
	fw.PutString(fAckExtDir, h.ExtensionDir)
	return fw.Bytes()
}

func DecodeHelloAck(b []byte) (HelloAck, error) {
	var h HelloAck
	err := Walk(b, func(tag uint16, kind uint8, fr *FieldReader) error {
		switch tag {
		case fAckProtocol:
			v, err := takeU64(kind, fr)
			h.Protocol = uint16(v) //nolint:gosec // G115: protocol version is u16 by protocol
			return err
		case fAckPhiVersion:
			s, err := takeString(kind, fr)
			h.PhiVersion = s
			return err
		case fAckCwd:
			s, err := takeString(kind, fr)
			h.Cwd = s
			return err
		case fAckSessionID:
			s, err := takeString(kind, fr)
			h.SessionID = s
			return err
		case fAckExtDir:
			s, err := takeString(kind, fr)
			h.ExtensionDir = s
			return err
		default:
			return fr.Skip(kind)
		}
	})
	return h, err
}

// RegisterCommand registers a slash command.
type RegisterCommand struct {
	Name        string
	Description string
}

func EncodeRegisterCommand(r RegisterCommand) []byte {
	var fw FieldWriter
	fw.PutString(fRegCmdName, r.Name)
	fw.PutString(fRegCmdDesc, r.Description)
	return fw.Bytes()
}

func DecodeRegisterCommand(b []byte) (RegisterCommand, error) {
	var r RegisterCommand
	err := Walk(b, func(tag uint16, kind uint8, fr *FieldReader) error {
		switch tag {
		case fRegCmdName:
			s, err := takeString(kind, fr)
			r.Name = s
			return err
		case fRegCmdDesc:
			s, err := takeString(kind, fr)
			r.Description = s
			return err
		default:
			return fr.Skip(kind)
		}
	})
	return r, err
}

// RegisterTool registers an LLM tool. SchemaJSON is opaque JSON Schema bytes.
type RegisterTool struct {
	Name        string
	Description string
	SchemaJSON  []byte
}

func EncodeRegisterTool(r RegisterTool) []byte {
	var fw FieldWriter
	fw.PutString(fRegToolName, r.Name)
	fw.PutString(fRegToolDesc, r.Description)
	fw.PutBytes(fRegToolSchema, r.SchemaJSON)
	return fw.Bytes()
}

func DecodeRegisterTool(b []byte) (RegisterTool, error) {
	var r RegisterTool
	err := Walk(b, func(tag uint16, kind uint8, fr *FieldReader) error {
		switch tag {
		case fRegToolName:
			s, err := takeString(kind, fr)
			r.Name = s
			return err
		case fRegToolDesc:
			s, err := takeString(kind, fr)
			r.Description = s
			return err
		case fRegToolSchema:
			p, err := takeBytes(kind, fr)
			r.SchemaJSON = append([]byte(nil), p...)
			return err
		default:
			return fr.Skip(kind)
		}
	})
	return r, err
}

// Subscribe declares event / intercept interests.
type Subscribe struct {
	Events    []uint16
	Intercept []uint16
}

func EncodeSubscribe(s Subscribe) []byte {
	var fw FieldWriter
	fw.PutU16s(fSubEvents, s.Events)
	fw.PutU16s(fSubIntercept, s.Intercept)
	return fw.Bytes()
}

func DecodeSubscribe(b []byte) (Subscribe, error) {
	var s Subscribe
	err := Walk(b, func(tag uint16, kind uint8, fr *FieldReader) error {
		switch tag {
		case fSubEvents:
			p, err := takeBytes(kind, fr)
			if err != nil {
				return err
			}
			s.Events, err = decodeU16s(p)
			return err
		case fSubIntercept:
			p, err := takeBytes(kind, fr)
			if err != nil {
				return err
			}
			s.Intercept, err = decodeU16s(p)
			return err
		default:
			return fr.Skip(kind)
		}
	})
	return s, err
}

func decodeU16s(p []byte) ([]uint16, error) {
	br := NewByteReader(p)
	return br.U16s()
}

// CommandInvoked is host→ext when the user runs a slash command.
type CommandInvoked struct {
	Name string
	Args string
}

func EncodeCommandInvoked(c CommandInvoked) []byte {
	var fw FieldWriter
	fw.PutString(fCmdInvName, c.Name)
	fw.PutString(fCmdInvArgs, c.Args)
	return fw.Bytes()
}

func DecodeCommandInvoked(b []byte) (CommandInvoked, error) {
	var c CommandInvoked
	err := Walk(b, func(tag uint16, kind uint8, fr *FieldReader) error {
		switch tag {
		case fCmdInvName:
			s, err := takeString(kind, fr)
			c.Name = s
			return err
		case fCmdInvArgs:
			s, err := takeString(kind, fr)
			c.Args = s
			return err
		default:
			return fr.Skip(kind)
		}
	})
	return c, err
}

// CommandResponse is ext→host.
type CommandResponse struct {
	OK     bool
	Error  string
	Notify string
	Submit string
}

func EncodeCommandResponse(c CommandResponse) []byte {
	var fw FieldWriter
	fw.PutBool(fCmdResOK, c.OK)
	fw.PutString(fCmdResError, c.Error)
	fw.PutString(fCmdResNotify, c.Notify)
	fw.PutString(fCmdResSubmit, c.Submit)
	return fw.Bytes()
}

func DecodeCommandResponse(b []byte) (CommandResponse, error) {
	var c CommandResponse
	err := Walk(b, func(tag uint16, kind uint8, fr *FieldReader) error {
		switch tag {
		case fCmdResOK:
			v, err := takeU64(kind, fr)
			c.OK = v != 0
			return err
		case fCmdResError:
			s, err := takeString(kind, fr)
			c.Error = s
			return err
		case fCmdResNotify:
			s, err := takeString(kind, fr)
			c.Notify = s
			return err
		case fCmdResSubmit:
			s, err := takeString(kind, fr)
			c.Submit = s
			return err
		default:
			return fr.Skip(kind)
		}
	})
	return c, err
}

// ToolInvoke is host→ext for a registered tool.
type ToolInvoke struct {
	Name string
	Args []byte
}

func EncodeToolInvoke(t ToolInvoke) []byte {
	var fw FieldWriter
	fw.PutString(fToolInvName, t.Name)
	fw.PutBytes(fToolInvArgs, t.Args)
	return fw.Bytes()
}

func DecodeToolInvoke(b []byte) (ToolInvoke, error) {
	var t ToolInvoke
	err := Walk(b, func(tag uint16, kind uint8, fr *FieldReader) error {
		switch tag {
		case fToolInvName:
			s, err := takeString(kind, fr)
			t.Name = s
			return err
		case fToolInvArgs:
			p, err := takeBytes(kind, fr)
			t.Args = append([]byte(nil), p...)
			return err
		default:
			return fr.Skip(kind)
		}
	})
	return t, err
}

// ToolResultMsg is ext→host tool outcome.
type ToolResultMsg struct {
	Content string
	Detail  string
	Output  string
	IsError bool
	Error   string
}

func EncodeToolResult(t ToolResultMsg) []byte {
	var fw FieldWriter
	fw.PutString(fToolResContent, t.Content)
	fw.PutString(fToolResDetail, t.Detail)
	fw.PutString(fToolResOutput, t.Output)
	fw.PutBool(fToolResIsError, t.IsError)
	fw.PutString(fToolResError, t.Error)
	return fw.Bytes()
}

func DecodeToolResult(b []byte) (ToolResultMsg, error) {
	var t ToolResultMsg
	err := Walk(b, func(tag uint16, kind uint8, fr *FieldReader) error {
		switch tag {
		case fToolResContent:
			s, err := takeString(kind, fr)
			t.Content = s
			return err
		case fToolResDetail:
			s, err := takeString(kind, fr)
			t.Detail = s
			return err
		case fToolResOutput:
			s, err := takeString(kind, fr)
			t.Output = s
			return err
		case fToolResIsError:
			v, err := takeU64(kind, fr)
			t.IsError = v != 0
			return err
		case fToolResError:
			s, err := takeString(kind, fr)
			t.Error = s
			return err
		default:
			return fr.Skip(kind)
		}
	})
	return t, err
}

// InterceptReq is host→ext for a blocking decision point.
type InterceptReq struct {
	Event      uint16
	ToolName   string
	ToolCallID string
	Input      []byte
	Content    string
	IsError    bool
	ErrText    string
	Prompt     string
	Reason     string
	TargetID   string
	TurnIndex  uint32
}

func EncodeInterceptReq(r InterceptReq) []byte {
	var fw FieldWriter
	fw.PutU16(fIxReqEvent, r.Event)
	fw.PutString(fIxReqToolName, r.ToolName)
	fw.PutString(fIxReqToolCallID, r.ToolCallID)
	fw.PutBytes(fIxReqInput, r.Input)
	fw.PutString(fIxReqContent, r.Content)
	fw.PutBool(fIxReqIsError, r.IsError)
	fw.PutString(fIxReqErrText, r.ErrText)
	fw.PutString(fIxReqPrompt, r.Prompt)
	fw.PutString(fIxReqReason, r.Reason)
	fw.PutString(fIxReqTargetID, r.TargetID)
	fw.PutU32(fIxReqTurnIndex, r.TurnIndex)
	return fw.Bytes()
}

func DecodeInterceptReq(b []byte) (InterceptReq, error) {
	var r InterceptReq
	err := Walk(b, func(tag uint16, kind uint8, fr *FieldReader) error {
		switch tag {
		case fIxReqEvent:
			v, err := takeU64(kind, fr)
			r.Event = uint16(v) //nolint:gosec // G115: event code is u16 by protocol
			return err
		case fIxReqToolName:
			s, err := takeString(kind, fr)
			r.ToolName = s
			return err
		case fIxReqToolCallID:
			s, err := takeString(kind, fr)
			r.ToolCallID = s
			return err
		case fIxReqInput:
			p, err := takeBytes(kind, fr)
			r.Input = append([]byte(nil), p...)
			return err
		case fIxReqContent:
			s, err := takeString(kind, fr)
			r.Content = s
			return err
		case fIxReqIsError:
			v, err := takeU64(kind, fr)
			r.IsError = v != 0
			return err
		case fIxReqErrText:
			s, err := takeString(kind, fr)
			r.ErrText = s
			return err
		case fIxReqPrompt:
			s, err := takeString(kind, fr)
			r.Prompt = s
			return err
		case fIxReqReason:
			s, err := takeString(kind, fr)
			r.Reason = s
			return err
		case fIxReqTargetID:
			s, err := takeString(kind, fr)
			r.TargetID = s
			return err
		case fIxReqTurnIndex:
			v, err := takeU64(kind, fr)
			r.TurnIndex = uint32(v) //nolint:gosec // G115: turn index is u32 by protocol
			return err
		default:
			return fr.Skip(kind)
		}
	})
	return r, err
}

// InterceptResp is ext→host.
type InterceptResp struct {
	Block              bool
	Stop               bool
	Cancel             bool
	Handled            bool
	Continue           bool
	Reason             string
	Input              []byte
	Content            string
	Context            string
	SystemPromptAppend string
	Toast              string
	Prompt             string // rewrite user prompt / steer message
}

func EncodeInterceptResp(r InterceptResp) []byte {
	var fw FieldWriter
	fw.PutBool(fIxResBlock, r.Block)
	fw.PutBool(fIxResStop, r.Stop)
	fw.PutBool(fIxResCancel, r.Cancel)
	fw.PutString(fIxResReason, r.Reason)
	fw.PutBytes(fIxResInput, r.Input)
	fw.PutString(fIxResContent, r.Content)
	fw.PutString(fIxResContext, r.Context)
	fw.PutString(fIxResSysAppend, r.SystemPromptAppend)
	fw.PutString(fIxResToast, r.Toast)
	fw.PutBool(fIxResHandled, r.Handled)
	fw.PutString(fIxResPrompt, r.Prompt)
	fw.PutBool(fIxResContinue, r.Continue)
	return fw.Bytes()
}

func DecodeInterceptResp(b []byte) (InterceptResp, error) {
	var r InterceptResp
	err := Walk(b, func(tag uint16, kind uint8, fr *FieldReader) error {
		switch tag {
		case fIxResBlock:
			v, err := takeU64(kind, fr)
			r.Block = v != 0
			return err
		case fIxResStop:
			v, err := takeU64(kind, fr)
			r.Stop = v != 0
			return err
		case fIxResCancel:
			v, err := takeU64(kind, fr)
			r.Cancel = v != 0
			return err
		case fIxResReason:
			s, err := takeString(kind, fr)
			r.Reason = s
			return err
		case fIxResInput:
			p, err := takeBytes(kind, fr)
			r.Input = append([]byte(nil), p...)
			return err
		case fIxResContent:
			s, err := takeString(kind, fr)
			r.Content = s
			return err
		case fIxResContext:
			s, err := takeString(kind, fr)
			r.Context = s
			return err
		case fIxResSysAppend:
			s, err := takeString(kind, fr)
			r.SystemPromptAppend = s
			return err
		case fIxResToast:
			s, err := takeString(kind, fr)
			r.Toast = s
			return err
		case fIxResHandled:
			v, err := takeU64(kind, fr)
			r.Handled = v != 0
			return err
		case fIxResPrompt:
			s, err := takeString(kind, fr)
			r.Prompt = s
			return err
		case fIxResContinue:
			v, err := takeU64(kind, fr)
			r.Continue = v != 0
			return err
		default:
			return fr.Skip(kind)
		}
	})
	return r, err
}

// EventNotify is a fire-and-forget host→ext lifecycle event.
type EventNotify struct {
	Event             uint16
	ToolName          string
	ToolCallID        string
	Input             []byte
	IsError           bool
	Prompt            string
	Reason            string
	TurnIndex         uint32
	SessionID         string
	PreviousSessionID string
	TargetSessionID   string
}

func EncodeEventNotify(e EventNotify) []byte {
	var fw FieldWriter
	fw.PutU16(fEvEvent, e.Event)
	fw.PutString(fEvToolName, e.ToolName)
	fw.PutString(fEvToolCallID, e.ToolCallID)
	fw.PutBytes(fEvInput, e.Input)
	fw.PutBool(fEvIsError, e.IsError)
	fw.PutString(fEvPrompt, e.Prompt)
	fw.PutString(fEvReason, e.Reason)
	fw.PutU32(fEvTurnIndex, e.TurnIndex)
	fw.PutString(fEvSessionID, e.SessionID)
	fw.PutString(fEvPreviousSessionID, e.PreviousSessionID)
	fw.PutString(fEvTargetSessionID, e.TargetSessionID)
	return fw.Bytes()
}

func DecodeEventNotify(b []byte) (EventNotify, error) {
	var e EventNotify
	err := Walk(b, func(tag uint16, kind uint8, fr *FieldReader) error {
		switch tag {
		case fEvEvent:
			v, err := takeU64(kind, fr)
			e.Event = uint16(v) //nolint:gosec // G115: event code is u16 by protocol
			return err
		case fEvToolName:
			s, err := takeString(kind, fr)
			e.ToolName = s
			return err
		case fEvToolCallID:
			s, err := takeString(kind, fr)
			e.ToolCallID = s
			return err
		case fEvInput:
			p, err := takeBytes(kind, fr)
			e.Input = append([]byte(nil), p...)
			return err
		case fEvIsError:
			v, err := takeU64(kind, fr)
			e.IsError = v != 0
			return err
		case fEvPrompt:
			s, err := takeString(kind, fr)
			e.Prompt = s
			return err
		case fEvReason:
			s, err := takeString(kind, fr)
			e.Reason = s
			return err
		case fEvTurnIndex:
			v, err := takeU64(kind, fr)
			e.TurnIndex = uint32(v) //nolint:gosec // G115: turn index is u32 by protocol
			return err
		case fEvSessionID:
			s, err := takeString(kind, fr)
			e.SessionID = s
			return err
		case fEvPreviousSessionID:
			s, err := takeString(kind, fr)
			e.PreviousSessionID = s
			return err
		case fEvTargetSessionID:
			s, err := takeString(kind, fr)
			e.TargetSessionID = s
			return err
		default:
			return fr.Skip(kind)
		}
	})
	return e, err
}

// NotifyMsg is ext→host UI toast/status.
type NotifyMsg struct {
	Level     string
	Message   string
	Status    string
	StatusSet bool
}

func EncodeNotify(n NotifyMsg) []byte {
	var fw FieldWriter
	fw.PutString(fNotifyLevel, n.Level)
	fw.PutString(fNotifyMessage, n.Message)
	fw.PutString(fNotifyStatus, n.Status)
	fw.PutBool(fNotifyStatusSet, n.StatusSet)
	return fw.Bytes()
}

func DecodeNotify(b []byte) (NotifyMsg, error) {
	var n NotifyMsg
	err := Walk(b, func(tag uint16, kind uint8, fr *FieldReader) error {
		switch tag {
		case fNotifyLevel:
			s, err := takeString(kind, fr)
			n.Level = s
			return err
		case fNotifyMessage:
			s, err := takeString(kind, fr)
			n.Message = s
			return err
		case fNotifyStatus:
			s, err := takeString(kind, fr)
			n.Status = s
			return err
		case fNotifyStatusSet:
			v, err := takeU64(kind, fr)
			n.StatusSet = v != 0
			return err
		default:
			return fr.Skip(kind)
		}
	})
	return n, err
}

// HostRequest is ext→host capability RPC.
type HostRequest struct {
	Method string // send_user_message
	Arg    string
}

func EncodeHostRequest(r HostRequest) []byte {
	var fw FieldWriter
	fw.PutString(fHostReqMethod, r.Method)
	fw.PutString(fHostReqArg, r.Arg)
	return fw.Bytes()
}

func DecodeHostRequest(b []byte) (HostRequest, error) {
	var r HostRequest
	err := Walk(b, func(tag uint16, kind uint8, fr *FieldReader) error {
		switch tag {
		case fHostReqMethod:
			s, err := takeString(kind, fr)
			r.Method = s
			return err
		case fHostReqArg:
			s, err := takeString(kind, fr)
			r.Arg = s
			return err
		default:
			return fr.Skip(kind)
		}
	})
	return r, err
}

// HostResult is host→ext reply.
type HostResult struct {
	OK    bool
	Error string
	Body  string
}

func EncodeHostResult(r HostResult) []byte {
	var fw FieldWriter
	fw.PutBool(fHostResOK, r.OK)
	fw.PutString(fHostResError, r.Error)
	fw.PutString(fHostResBody, r.Body)
	return fw.Bytes()
}

func DecodeHostResult(b []byte) (HostResult, error) {
	var r HostResult
	err := Walk(b, func(tag uint16, kind uint8, fr *FieldReader) error {
		switch tag {
		case fHostResOK:
			v, err := takeU64(kind, fr)
			r.OK = v != 0
			return err
		case fHostResError:
			s, err := takeString(kind, fr)
			r.Error = s
			return err
		case fHostResBody:
			s, err := takeString(kind, fr)
			r.Body = s
			return err
		default:
			return fr.Skip(kind)
		}
	})
	return r, err
}

// SessionMeta is host→ext session identity push.
type SessionMeta struct {
	SessionID string
	Cwd       string
}

func EncodeSessionMeta(m SessionMeta) []byte {
	var fw FieldWriter
	fw.PutString(fMetaSessionID, m.SessionID)
	fw.PutString(fMetaCwd, m.Cwd)
	return fw.Bytes()
}

func DecodeSessionMeta(b []byte) (SessionMeta, error) {
	var m SessionMeta
	err := Walk(b, func(tag uint16, kind uint8, fr *FieldReader) error {
		switch tag {
		case fMetaSessionID:
			s, err := takeString(kind, fr)
			m.SessionID = s
			return err
		case fMetaCwd:
			s, err := takeString(kind, fr)
			m.Cwd = s
			return err
		default:
			return fr.Skip(kind)
		}
	})
	return m, err
}

func takeU64(kind uint8, fr *FieldReader) (uint64, error) {
	if kind != WireU64 {
		_ = fr.Skip(kind)
		return 0, ErrBadWire
	}
	return fr.U64()
}

func takeBytes(kind uint8, fr *FieldReader) ([]byte, error) {
	if kind != WireBytes {
		_ = fr.Skip(kind)
		return nil, ErrBadWire
	}
	return fr.Bytes()
}

func takeString(kind uint8, fr *FieldReader) (string, error) {
	p, err := takeBytes(kind, fr)
	if err != nil {
		return "", err
	}
	return string(p), nil
}
