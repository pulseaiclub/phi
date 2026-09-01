/** PXB protocol constants — must match ext/pxb/types.go. */

export const ProtocolVersion = 1;

export const Magic = new Uint8Array([0x50, 0x58, 0x42, 0x01]); // PXB\x01

export const HeaderSize = 16;

export const MaxPayload = 16 << 20;

export const TypeHello = 1;
export const TypeReady = 2;
export const TypeRegisterCommand = 3;
export const TypeRegisterTool = 4;
export const TypeSubscribe = 5;
export const TypeCommandResponse = 6;
export const TypeToolResult = 7;
export const TypeInterceptResponse = 8;
export const TypeShutdownAck = 9;
export const TypeNotify = 10;
export const TypeHostRequest = 11;

export const TypeHelloAck = 100;
export const TypeCommandInvoked = 101;
export const TypeToolInvoke = 102;
export const TypeEvent = 103;
export const TypeIntercept = 104;
export const TypeShutdown = 105;
export const TypeHostResult = 106;
export const TypeSessionMeta = 107;

export const FlagHasID = 1 << 0;

export const CapCommands = 1 << 0;
export const CapTools = 1 << 1;
export const CapEvents = 1 << 2;
export const CapIntercept = 1 << 3;

export const EvToolCall = 1;
export const EvToolResult = 2;
export const EvToolExecStart = 3;
export const EvToolExecEnd = 4;
export const EvSessionStart = 5;
export const EvSessionShutdown = 6;
export const EvSessionBeforeSwitch = 7;
export const EvBeforeAgentStart = 8;
export const EvAgentStart = 9;
export const EvAgentEnd = 10;
export const EvTurnStart = 11;
export const EvTurnEnd = 12;
export const EvUserInput = 13;
export const EvTurnStopping = 14;
export const EvSessionCompact = 15;
export const EvPaneAction = 16;

const eventNames: Record<number, string> = {
  [EvToolCall]: "tool_call",
  [EvToolResult]: "tool_result",
  [EvToolExecStart]: "tool_execution_start",
  [EvToolExecEnd]: "tool_execution_end",
  [EvSessionStart]: "session_start",
  [EvSessionShutdown]: "session_shutdown",
  [EvSessionBeforeSwitch]: "session_before_switch",
  [EvBeforeAgentStart]: "before_agent_start",
  [EvAgentStart]: "agent_start",
  [EvAgentEnd]: "agent_end",
  [EvTurnStart]: "turn_start",
  [EvTurnEnd]: "turn_end",
  [EvUserInput]: "user_input",
  [EvTurnStopping]: "turn_stopping",
  [EvSessionCompact]: "session_compact",
  [EvPaneAction]: "pane_action",
};

const eventCodes: Record<string, number> = Object.fromEntries(
  Object.entries(eventNames).map(([k, v]) => [v, Number(k)]),
);

export function eventName(code: number): string {
  return eventNames[code] ?? "";
}

export function eventCode(name: string): number {
  return eventCodes[name] ?? 0;
}

export const WireU64 = 1;
export const WireBytes = 2;
