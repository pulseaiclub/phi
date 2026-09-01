package pxb

// ProtocolVersion is negotiated in Hello / HelloAck.
const ProtocolVersion uint16 = 1

// Frame magic: P X B + version byte.
var Magic = [4]byte{'P', 'X', 'B', 0x01}

// HeaderSize is the fixed frame header length.
const HeaderSize = 16

// Maximum payload accepted from a peer (16 MiB).
const MaxPayload = 16 << 20

// Message types. Ext→Host are 1–99; Host→Ext are 100–199.
const (
	TypeHello             uint16 = 1
	TypeReady             uint16 = 2
	TypeRegisterCommand   uint16 = 3
	TypeRegisterTool      uint16 = 4
	TypeSubscribe         uint16 = 5
	TypeCommandResponse   uint16 = 6
	TypeToolResult        uint16 = 7
	TypeInterceptResponse uint16 = 8
	TypeShutdownAck       uint16 = 9
	TypeNotify            uint16 = 10
	TypeHostRequest       uint16 = 11 // ext→host RPC (send_user_message, …)

	TypeHelloAck       uint16 = 100
	TypeCommandInvoked uint16 = 101
	TypeToolInvoke     uint16 = 102
	TypeEvent          uint16 = 103
	TypeIntercept      uint16 = 104
	TypeShutdown       uint16 = 105
	TypeHostResult     uint16 = 106 // host→ext reply to TypeHostRequest
	TypeSessionMeta    uint16 = 107 // host→ext session/cwd push
)

// Flag bits in the header.
const (
	FlagHasID uint16 = 1 << 0 // id field is meaningful (RPC correlation)
)

// Capability bits advertised in Hello.
const (
	CapCommands  uint32 = 1 << 0
	CapTools     uint32 = 1 << 1
	CapEvents    uint32 = 1 << 2
	CapIntercept uint32 = 1 << 3
)

// Event name codes (compact on the wire; strings only at SDK edges).
// Append-only: never reuse a code. Unknown codes are ignored by peers
// that did not Subscribe to them.
const (
	EvToolCall            uint16 = 1
	EvToolResult          uint16 = 2
	EvToolExecStart       uint16 = 3
	EvToolExecEnd         uint16 = 4
	EvSessionStart        uint16 = 5
	EvSessionShutdown     uint16 = 6
	EvSessionBeforeSwitch uint16 = 7
	EvBeforeAgentStart    uint16 = 8
	EvAgentStart          uint16 = 9
	EvAgentEnd            uint16 = 10
	EvTurnStart           uint16 = 11
	EvTurnEnd             uint16 = 12
	EvUserInput           uint16 = 13
	EvTurnStopping        uint16 = 14
	EvSessionCompact      uint16 = 15
	EvPaneAction          uint16 = 16
)

// EventName maps a wire code to the public ext event string.
func EventName(code uint16) string {
	switch code {
	case EvToolCall:
		return "tool_call"
	case EvToolResult:
		return "tool_result"
	case EvToolExecStart:
		return "tool_execution_start"
	case EvToolExecEnd:
		return "tool_execution_end"
	case EvSessionStart:
		return "session_start"
	case EvSessionShutdown:
		return "session_shutdown"
	case EvSessionBeforeSwitch:
		return "session_before_switch"
	case EvBeforeAgentStart:
		return "before_agent_start"
	case EvAgentStart:
		return "agent_start"
	case EvAgentEnd:
		return "agent_end"
	case EvTurnStart:
		return "turn_start"
	case EvTurnEnd:
		return "turn_end"
	case EvUserInput:
		return "user_input"
	case EvTurnStopping:
		return "turn_stopping"
	case EvSessionCompact:
		return "session_compact"
	case EvPaneAction:
		return "pane_action"
	default:
		return ""
	}
}

// EventCode maps a public event string to a wire code (0 = unknown).
func EventCode(name string) uint16 {
	switch name {
	case "tool_call":
		return EvToolCall
	case "tool_result":
		return EvToolResult
	case "tool_execution_start":
		return EvToolExecStart
	case "tool_execution_end":
		return EvToolExecEnd
	case "session_start":
		return EvSessionStart
	case "session_shutdown":
		return EvSessionShutdown
	case "session_before_switch":
		return EvSessionBeforeSwitch
	case "before_agent_start":
		return EvBeforeAgentStart
	case "agent_start":
		return EvAgentStart
	case "agent_end":
		return EvAgentEnd
	case "turn_start":
		return EvTurnStart
	case "turn_end":
		return EvTurnEnd
	case "user_input":
		return EvUserInput
	case "turn_stopping":
		return EvTurnStopping
	case "session_compact":
		return EvSessionCompact
	case "pane_action":
		return EvPaneAction
	default:
		return 0
	}
}
