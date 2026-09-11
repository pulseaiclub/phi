//! Wire constants, frame types, and lifecycle event codes.

/// Negotiated in Hello / HelloAck; bump only for incompatible renames.
pub const PROTOCOL_VERSION: u16 = 1;

/// Frame magic: `P X B` + version byte.
pub const MAGIC: [u8; 4] = *b"PXB\x01";

/// Fixed frame header length.
pub const HEADER_SIZE: usize = 16;

/// Maximum payload accepted from a peer (16 MiB).
pub const MAX_PAYLOAD: usize = 16 << 20;

// Message types. Ext→Host are 1–99; Host→Ext are 100–199.
// Append-only: new types take the next number in their range.
pub const TYPE_HELLO: u16 = 1;
pub const TYPE_READY: u16 = 2;
pub const TYPE_REGISTER_COMMAND: u16 = 3;
pub const TYPE_REGISTER_TOOL: u16 = 4;
pub const TYPE_SUBSCRIBE: u16 = 5;
pub const TYPE_COMMAND_RESPONSE: u16 = 6;
pub const TYPE_TOOL_RESULT: u16 = 7;
pub const TYPE_INTERCEPT_RESPONSE: u16 = 8;
pub const TYPE_SHUTDOWN_ACK: u16 = 9;
pub const TYPE_NOTIFY: u16 = 10;
pub const TYPE_HOST_REQUEST: u16 = 11;
pub const TYPE_TOOL_DETAIL_RESULT: u16 = 12;

pub const TYPE_HELLO_ACK: u16 = 100;
pub const TYPE_COMMAND_INVOKED: u16 = 101;
pub const TYPE_TOOL_INVOKE: u16 = 102;
pub const TYPE_EVENT: u16 = 103;
pub const TYPE_INTERCEPT: u16 = 104;
pub const TYPE_SHUTDOWN: u16 = 105;
pub const TYPE_HOST_RESULT: u16 = 106;
pub const TYPE_SESSION_META: u16 = 107;
pub const TYPE_TOOL_DETAIL_INVOKE: u16 = 108;

/// Flag bits in the header.
pub const FLAG_HAS_ID: u16 = 1 << 0; // id field is meaningful (RPC correlation)

/// Capability bits advertised in Hello.
pub const CAP_COMMANDS: u32 = 1 << 0;
pub const CAP_TOOLS: u32 = 1 << 1;
pub const CAP_EVENTS: u32 = 1 << 2;
pub const CAP_INTERCEPT: u32 = 1 << 3;

/// Frame types with an explicit [`Unknown`](FrameType::Unknown) arm so peers
/// that do not understand a type still skip the frame by length.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum FrameType {
    Hello,
    Ready,
    RegisterCommand,
    RegisterTool,
    Subscribe,
    CommandResponse,
    ToolResult,
    InterceptResponse,
    ShutdownAck,
    Notify,
    HostRequest,
    ToolDetailResult,
    HelloAck,
    CommandInvoked,
    ToolInvoke,
    Event,
    Intercept,
    Shutdown,
    HostResult,
    SessionMeta,
    ToolDetailInvoke,
    Unknown(u16),
}

impl FrameType {
    pub fn from_u16(v: u16) -> Self {
        match v {
            TYPE_HELLO => Self::Hello,
            TYPE_READY => Self::Ready,
            TYPE_REGISTER_COMMAND => Self::RegisterCommand,
            TYPE_REGISTER_TOOL => Self::RegisterTool,
            TYPE_SUBSCRIBE => Self::Subscribe,
            TYPE_COMMAND_RESPONSE => Self::CommandResponse,
            TYPE_TOOL_RESULT => Self::ToolResult,
            TYPE_INTERCEPT_RESPONSE => Self::InterceptResponse,
            TYPE_SHUTDOWN_ACK => Self::ShutdownAck,
            TYPE_NOTIFY => Self::Notify,
            TYPE_HOST_REQUEST => Self::HostRequest,
            TYPE_TOOL_DETAIL_RESULT => Self::ToolDetailResult,
            TYPE_HELLO_ACK => Self::HelloAck,
            TYPE_COMMAND_INVOKED => Self::CommandInvoked,
            TYPE_TOOL_INVOKE => Self::ToolInvoke,
            TYPE_EVENT => Self::Event,
            TYPE_INTERCEPT => Self::Intercept,
            TYPE_SHUTDOWN => Self::Shutdown,
            TYPE_HOST_RESULT => Self::HostResult,
            TYPE_SESSION_META => Self::SessionMeta,
            TYPE_TOOL_DETAIL_INVOKE => Self::ToolDetailInvoke,
            other => Self::Unknown(other),
        }
    }

    pub fn code(self) -> u16 {
        match self {
            Self::Hello => TYPE_HELLO,
            Self::Ready => TYPE_READY,
            Self::RegisterCommand => TYPE_REGISTER_COMMAND,
            Self::RegisterTool => TYPE_REGISTER_TOOL,
            Self::Subscribe => TYPE_SUBSCRIBE,
            Self::CommandResponse => TYPE_COMMAND_RESPONSE,
            Self::ToolResult => TYPE_TOOL_RESULT,
            Self::InterceptResponse => TYPE_INTERCEPT_RESPONSE,
            Self::ShutdownAck => TYPE_SHUTDOWN_ACK,
            Self::Notify => TYPE_NOTIFY,
            Self::HostRequest => TYPE_HOST_REQUEST,
            Self::ToolDetailResult => TYPE_TOOL_DETAIL_RESULT,
            Self::HelloAck => TYPE_HELLO_ACK,
            Self::CommandInvoked => TYPE_COMMAND_INVOKED,
            Self::ToolInvoke => TYPE_TOOL_INVOKE,
            Self::Event => TYPE_EVENT,
            Self::Intercept => TYPE_INTERCEPT,
            Self::Shutdown => TYPE_SHUTDOWN,
            Self::HostResult => TYPE_HOST_RESULT,
            Self::SessionMeta => TYPE_SESSION_META,
            Self::ToolDetailInvoke => TYPE_TOOL_DETAIL_INVOKE,
            Self::Unknown(v) => v,
        }
    }
}

/// Lifecycle event codes (compact on the wire; strings only at SDK edges).
/// Append-only: never reuse a code. Unknown codes are ignored by peers that
/// did not Subscribe to them.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Event {
    ToolCall,
    ToolResult,
    ToolExecStart,
    ToolExecEnd,
    SessionStart,
    SessionShutdown,
    SessionBeforeSwitch,
    BeforeAgentStart,
    AgentStart,
    AgentEnd,
    TurnStart,
    TurnEnd,
    UserInput,
    TurnStopping,
    SessionCompact,
    PaneAction,
    Unknown(u16),
}

impl Event {
    /// Maps a wire code to an [`Event`]; unrecognized codes stay
    /// [`Unknown`](Event::Unknown).
    pub fn from_code(code: u16) -> Self {
        match code {
            1 => Self::ToolCall,
            2 => Self::ToolResult,
            3 => Self::ToolExecStart,
            4 => Self::ToolExecEnd,
            5 => Self::SessionStart,
            6 => Self::SessionShutdown,
            7 => Self::SessionBeforeSwitch,
            8 => Self::BeforeAgentStart,
            9 => Self::AgentStart,
            10 => Self::AgentEnd,
            11 => Self::TurnStart,
            12 => Self::TurnEnd,
            13 => Self::UserInput,
            14 => Self::TurnStopping,
            15 => Self::SessionCompact,
            16 => Self::PaneAction,
            other => Self::Unknown(other),
        }
    }

    pub fn code(self) -> u16 {
        match self {
            Self::ToolCall => 1,
            Self::ToolResult => 2,
            Self::ToolExecStart => 3,
            Self::ToolExecEnd => 4,
            Self::SessionStart => 5,
            Self::SessionShutdown => 6,
            Self::SessionBeforeSwitch => 7,
            Self::BeforeAgentStart => 8,
            Self::AgentStart => 9,
            Self::AgentEnd => 10,
            Self::TurnStart => 11,
            Self::TurnEnd => 12,
            Self::UserInput => 13,
            Self::TurnStopping => 14,
            Self::SessionCompact => 15,
            Self::PaneAction => 16,
            Self::Unknown(v) => v,
        }
    }

    /// Public `ext` event name, or `""` for unknown codes.
    pub fn name(self) -> &'static str {
        match self {
            Self::ToolCall => "tool_call",
            Self::ToolResult => "tool_result",
            Self::ToolExecStart => "tool_execution_start",
            Self::ToolExecEnd => "tool_execution_end",
            Self::SessionStart => "session_start",
            Self::SessionShutdown => "session_shutdown",
            Self::SessionBeforeSwitch => "session_before_switch",
            Self::BeforeAgentStart => "before_agent_start",
            Self::AgentStart => "agent_start",
            Self::AgentEnd => "agent_end",
            Self::TurnStart => "turn_start",
            Self::TurnEnd => "turn_end",
            Self::UserInput => "user_input",
            Self::TurnStopping => "turn_stopping",
            Self::SessionCompact => "session_compact",
            Self::PaneAction => "pane_action",
            Self::Unknown(_) => "",
        }
    }

    /// Parses a public event name; unknown names yield
    /// [`Unknown(0)`](Event::Unknown) (the "no event" code).
    pub fn from_name(name: &str) -> Self {
        match name {
            "tool_call" => Self::ToolCall,
            "tool_result" => Self::ToolResult,
            "tool_execution_start" => Self::ToolExecStart,
            "tool_execution_end" => Self::ToolExecEnd,
            "session_start" => Self::SessionStart,
            "session_shutdown" => Self::SessionShutdown,
            "session_before_switch" => Self::SessionBeforeSwitch,
            "before_agent_start" => Self::BeforeAgentStart,
            "agent_start" => Self::AgentStart,
            "agent_end" => Self::AgentEnd,
            "turn_start" => Self::TurnStart,
            "turn_end" => Self::TurnEnd,
            "user_input" => Self::UserInput,
            "turn_stopping" => Self::TurnStopping,
            "session_compact" => Self::SessionCompact,
            "pane_action" => Self::PaneAction,
            _ => Self::Unknown(0),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn frame_type_roundtrip() {
        for v in 1..=19 {
            assert_eq!(FrameType::from_u16(v).code(), v);
        }
        assert_eq!(FrameType::from_u16(999), FrameType::Unknown(999));
    }

    #[test]
    fn event_name_and_code_roundtrip() {
        for code in 1..=16 {
            let ev = Event::from_code(code);
            assert_eq!(ev.code(), code);
            assert_eq!(Event::from_name(ev.name()), ev);
        }
        assert_eq!(Event::from_code(0), Event::Unknown(0));
        assert_eq!(Event::from_name("nope"), Event::Unknown(0));
    }
}
