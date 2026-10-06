// Wire constants, frame types, and lifecycle event codes — mirrors
// ext/rust/src/pxb/types.rs and ext/go/pxb/types.go one-to-one.

/** Negotiated in Hello / HelloAck; bump only for incompatible renames. */
export const PROTOCOL_VERSION = 1;

/** Frame magic: P X B + version byte. */
export const MAGIC = Buffer.from("PXB\x01", "latin1");

/** Fixed frame header length. */
export const HEADER_SIZE = 16;

/** Maximum payload accepted from a peer (16 MiB). */
export const MAX_PAYLOAD = 16 << 20;

// Message types. Ext→Host are 1–99; Host→Ext are 100–199.
// Append-only: new types take the next number in their range.
export const TYPE_HELLO = 1;
export const TYPE_READY = 2;
export const TYPE_REGISTER_COMMAND = 3;
export const TYPE_REGISTER_TOOL = 4;
export const TYPE_SUBSCRIBE = 5;
export const TYPE_COMMAND_RESPONSE = 6;
export const TYPE_TOOL_RESULT = 7;
export const TYPE_INTERCEPT_RESPONSE = 8;
export const TYPE_SHUTDOWN_ACK = 9;
export const TYPE_NOTIFY = 10;
export const TYPE_HOST_REQUEST = 11;
export const TYPE_TOOL_DETAIL_RESULT = 12;

export const TYPE_HELLO_ACK = 100;
export const TYPE_COMMAND_INVOKED = 101;
export const TYPE_TOOL_INVOKE = 102;
export const TYPE_EVENT = 103;
export const TYPE_INTERCEPT = 104;
export const TYPE_SHUTDOWN = 105;
export const TYPE_HOST_RESULT = 106;
export const TYPE_SESSION_META = 107;
export const TYPE_TOOL_DETAIL_INVOKE = 108;

/** Flag bits in the header. */
export const FLAG_HAS_ID = 1 << 0; // id field is meaningful (RPC correlation)

/** Capability bits advertised in Hello. */
export const CAP_COMMANDS = 1 << 0;
export const CAP_TOOLS = 1 << 1;
export const CAP_EVENTS = 1 << 2;
export const CAP_INTERCEPT = 1 << 3;

/** Lifecycle event codes (compact on the wire; strings only at SDK edges).
 *
 * Append-only: never reuse a code. Unknown codes are ignored by peers that
 * did not subscribe to them. Values are plain numbers so unknown wire codes
 * pass through losslessly; the named members cover the known set.
 */
export const Event = {
  ToolCall: 1,
  ToolResult: 2,
  ToolExecStart: 3,
  ToolExecEnd: 4,
  SessionStart: 5,
  SessionShutdown: 6,
  SessionBeforeSwitch: 7,
  BeforeAgentStart: 8,
  AgentStart: 9,
  AgentEnd: 10,
  TurnStart: 11,
  TurnEnd: 12,
  UserInput: 13,
  TurnStopping: 14,
  SessionCompact: 15,
  PaneAction: 16,
} as const;

/** A lifecycle event wire code; named members live on {@link Event}. */
export type EventCode = number;

// Wire names for the Event members; the snake_case exceptions are spelled
// out because the public strings are protocol surface (see Go's EventName).
const EVENT_NAMES: Readonly<Record<number, string>> = {
  [Event.ToolCall]: "tool_call",
  [Event.ToolResult]: "tool_result",
  [Event.ToolExecStart]: "tool_execution_start",
  [Event.ToolExecEnd]: "tool_execution_end",
  [Event.SessionStart]: "session_start",
  [Event.SessionShutdown]: "session_shutdown",
  [Event.SessionBeforeSwitch]: "session_before_switch",
  [Event.BeforeAgentStart]: "before_agent_start",
  [Event.AgentStart]: "agent_start",
  [Event.AgentEnd]: "agent_end",
  [Event.TurnStart]: "turn_start",
  [Event.TurnEnd]: "turn_end",
  [Event.UserInput]: "user_input",
  [Event.TurnStopping]: "turn_stopping",
  [Event.SessionCompact]: "session_compact",
  [Event.PaneAction]: "pane_action",
};

const EVENT_CODES: Readonly<Record<string, number>> = Object.fromEntries(
  Object.entries(EVENT_NAMES).map(([code, name]) => [name, Number(code)]),
);

/** Public ext event name for a wire code, or `""` for unknown codes. */
export function eventName(code: EventCode): string {
  return EVENT_NAMES[code] ?? "";
}

/** Parses a public event name; unknown names yield 0 (the "no event" code). */
export function eventFromName(name: string): EventCode {
  return EVENT_CODES[name] ?? 0;
}
