/** Author-facing shared types (mirrors github.com/pulseaiclub/phi/ext). */

export interface ToolCallEvent {
  toolName: string;
  toolCallID: string;
  input: Uint8Array;
}

export interface ToolCallResult {
  block?: boolean;
  reason?: string;
  input?: Uint8Array;
  context?: string;
}

export interface ToolResultEvent {
  toolName: string;
  toolCallID: string;
  input: Uint8Array;
  content: string;
  isError: boolean;
  err: string;
}

export interface ToolResultResult {
  content?: string;
  context?: string;
  stop?: boolean;
  reason?: string;
}

export interface BeforeAgentStartEvent {
  prompt: string;
}

export interface BeforeAgentStartResult {
  prompt?: string;
  systemPromptAppend?: string;
}

export interface SessionBeforeSwitchEvent {
  reason: string;
  targetSessionID: string;
}

export interface SessionBeforeSwitchResult {
  cancel?: boolean;
  reason?: string;
  toast?: string;
}

export interface UserInputEvent {
  text: string;
}

export interface UserInputResult {
  handled?: boolean;
  text?: string;
  reason?: string;
}

export interface TurnStoppingEvent {
  turnIndex: number;
}

export interface TurnStoppingResult {
  continue?: boolean;
  message?: string;
  reason?: string;
}

export interface ToolResult {
  content?: string;
  detail?: string;
  output?: string;
}

export interface Tool {
  name: string;
  label?: string;
  description: string;
  parameters?: Record<string, unknown>;
  execute: (args: Uint8Array) => Promise<ToolResult> | ToolResult;
}

export interface ExtensionContext {
  cwd: string;
  sessionID: string;
  hasUI: boolean;
}

export interface Command {
  description: string;
  handler: (args: string, ctx: ExtensionContext) => Promise<void> | void;
}

export interface ConfirmRequest {
  title: string;
  message: string;
  yes?: string;
  no?: string;
  danger?: boolean;
}

export interface ConfirmReply {
  ok: boolean;
}

export const EventToolCall = "tool_call";
export const EventToolResult = "tool_result";
export const EventToolExecutionStart = "tool_execution_start";
export const EventToolExecutionEnd = "tool_execution_end";
export const EventSessionStart = "session_start";
export const EventSessionShutdown = "session_shutdown";
export const EventSessionBeforeSwitch = "session_before_switch";
export const EventBeforeAgentStart = "before_agent_start";
export const EventAgentStart = "agent_start";
export const EventAgentEnd = "agent_end";
export const EventTurnStart = "turn_start";
export const EventTurnEnd = "turn_end";
export const EventUserInput = "user_input";
export const EventTurnStopping = "turn_stopping";
export const EventSessionCompact = "session_compact";
