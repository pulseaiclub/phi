// Author-facing model registration types — a TS port of
// ext/rust/src/phi/model.rs. A Model is either direct-connect (the host dials
// baseUrl like a config.yaml entry) or extension-served (`conn: "ext"` plus a
// stream handler that answers every ModelInvoke itself).
//
// Message and usage interfaces keep the host's JSON shapes (snake_case keys)
// so they pass through JSON.parse/JSON.stringify losslessly — the same shapes
// the Go SDK's ext.ChatMessage / ext.Usage marshal.

import { CONN_EXT } from "../pxb/types.ts";

/** OpenAI-style function call fragment inside a chat message or delta. */
export interface FunctionSpec {
  name?: string;
  arguments?: string;
}

/** Tool call attached to an assistant message, or a streaming delta
 * fragment (first fragment carries id/type/name, later ones append
 * arguments). */
export interface ToolCallSpec {
  index?: number;
  id?: string;
  type?: string;
  function?: FunctionSpec;
}

/** Base64 image attached to a user message. */
export interface MessageImage {
  data?: string;
  /** Wire key is `mimeType` (Go tag exception), not snake_case. */
  mimeType?: string;
}

/** One conversation message as the host serializes it. */
export interface ChatMessage {
  role?: string;
  content?: string;
  reasoning_content?: string;
  tool_calls?: ToolCallSpec[];
  tool_call_id?: string;
  images?: MessageImage[];
  provider_state?: unknown;
}

/** Tool schema forwarded from the host (JSON Schema `parameters`). */
export interface ModelTool {
  name?: string;
  description?: string;
  parameters?: unknown;
}

export interface PromptTokensDetails {
  cached_tokens?: number;
  cache_write_tokens?: number;
}

/** Token accounting; the host keeps the last usage seen on a stream. */
export interface Usage {
  completion_tokens?: number;
  prompt_tokens?: number;
  total_tokens?: number;
  prompt_tokens_details?: PromptTokensDetails;
}

/** One chat completion request, decoded from a ModelInvoke frame. */
export interface ModelRequest {
  model: string;
  /** Host-rendered system prompt for this turn. */
  systemPrompt: string;
  messages: ChatMessage[];
  tools: ModelTool[];
  /** Thinking level for this turn ("" when the model has no think modes). */
  think: string;
}

/** One stream fragment the handler emits. The host accumulates content and
 * tool calls; deltas only — never send the accumulated text. */
export interface ModelEvent {
  content?: string;
  reasoning?: string;
  toolCalls?: ToolCallSpec[];
  usage?: Usage;
}

/** Pushes one fragment to the host; rejects when the pipe is gone (treat it
 * as "stop streaming"). */
export type ModelEmit = (ev: ModelEvent) => void | Promise<void>;

/** Served-model handler. Throw/reject to fail the stream (the SDK sends the
 * error terminal frame); returning normally sends done. `signal` fires when
 * the host sends ModelCancel — abort upstream work on it. */
export type ModelStreamHandler = (
  req: ModelRequest,
  emit: ModelEmit,
  signal: AbortSignal,
) => void | Promise<void>;

/** A model the host can switch to. Plain data — build it literally or with
 * {@link directModel} / {@link servedModel}:

 * ```ts
 * m.registerModel({
 *   name: "gw", api: "Anthropic", baseUrl: "https://gw.internal", apiKey: "k",
 *   contextWindow: 272000,
 * });
 * ```
 */
export interface Model {
  name: string;
  /** "" (direct-connect) or "ext" (extension-served). Default "". */
  conn?: string;
  /** Provider API id for direct-connect (e.g. "OpenAI", "Anthropic"). */
  api?: string;
  baseUrl?: string;
  apiKey?: string;
  /** Config preset the entry mimics; "" = none. */
  preset?: string;
  contextWindow?: number;
  imageEnabled?: boolean;
  thinkEnabled?: boolean;
  thinkLevel?: string;
  /** Required when `conn` is "ext"; ignored for direct-connect. */
  stream?: ModelStreamHandler;
}

/** Direct-connect model literal helper (host dials `baseUrl`). */
export function directModel(name: string): Model {
  return { name };
}

/** Extension-served model literal helper: every request arrives at
 * `stream` as a {@link ModelRequest}. */
export function servedModel(name: string, stream: ModelStreamHandler): Model {
  return { name, conn: CONN_EXT, stream };
}
