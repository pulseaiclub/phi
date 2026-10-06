// Typed message payloads, encoded as tagged fields (see fields.ts) — a TS
// port of ext/rust/src/pxb/msg.rs.
//
// Every struct mirrors ext/go/pxb one-to-one so the SDKs interop at the byte
// level. Encode field order matches Go's writers — golden tests pin the bytes
// against ext/go/pxb/testdata/*.bin.
//
// Messages are declared once in a field table; a shared encode/decode pair
// fills the role of the Rust `pxb_message!` macro (the tables below are the
// single source of truth per message — do not hand-write encode switches).

import { PxbError } from "./codec.ts";
import { FieldReader, FieldWriter, WIRE_BYTES, WIRE_U64, walkFields } from "./fields.ts";

type FieldKind = "u64" | "bytes" | "string" | "u16s";

interface FieldSpec {
  readonly tag: number;
  readonly kind: FieldKind;
  readonly key: string;
  /** Booleans ride the `u64` wire kind; flag them so decode yields real
   * booleans and encode accepts `true`/`false`. */
  readonly bool?: boolean;
  /** Omit the field on the wire when it is zero (`u64` only; empties are
   * always omitted). Keeps old peers forward-compatible. */
  readonly opt?: boolean;
}

type WireMessage = Record<string, unknown>;

function encodeMsg(spec: readonly FieldSpec[], msg: object): Buffer {
  const src = msg as WireMessage;
  const fw = new FieldWriter();
  for (const f of spec) {
    const v = f.bool ? wireBool(src[f.key]) : src[f.key];
    switch (f.kind) {
      case "u64":
        if (!(f.opt && isZero(v))) fw.putU64(f.tag, num(v));
        break;
      case "string":
        fw.putString(f.tag, str(v));
        break;
      case "bytes":
        fw.putBytes(f.tag, bytes(v));
        break;
      case "u16s":
        fw.putU16s(f.tag, list(v));
        break;
    }
  }
  return fw.bytes();
}

function decodeMsg(spec: readonly FieldSpec[], b: Uint8Array): WireMessage {
  // Absent tags mean the zero value (Rust derives Default for this).
  const m: WireMessage = {};
  const byTag = new Map(spec.map((f) => [f.tag, f]));
  for (const f of spec) {
    switch (f.kind) {
      case "u64":
        m[f.key] = f.bool ? false : 0;
        break;
      case "string":
        m[f.key] = "";
        break;
      case "bytes":
        m[f.key] = new Uint8Array(0);
        break;
      case "u16s":
        m[f.key] = [];
        break;
    }
  }
  walkFields(asBuffer(b), (tag, kind, fr) => {
    const f = byTag.get(tag);
    if (!f) {
      fr.skip(kind); // unknown tags stay skippable — forward compatibility
      return;
    }
    // Consume the value per its actual kind, then reject mismatches —
    // the Rust port (wire.rs take_*) does the same.
    if (kind === WIRE_U64) {
      const v = fr.u64();
      if (f.kind !== "u64") {
        throw new PxbError("badWire", `bad wire kind for field ${f.key}`);
      }
      m[f.key] = f.bool ? v !== 0 : v;
      return;
    }
    const raw = fr.bytes();
    switch (f.kind) {
      case "string":
        m[f.key] = raw.toString("utf8"); // lossy, like Rust from_utf8_lossy
        return;
      case "bytes":
        m[f.key] = Buffer.from(raw); // copy: raw aliases the payload
        return;
      case "u16s":
        m[f.key] = decodeU16s(raw);
        return;
      default:
        throw new PxbError("badWire", `bad wire kind for field ${f.key}`);
    }
  });
  return m;
}

/** Inner packed-list format is plain: `u16 count` + u16 values (no tags). */
function decodeU16s(p: Buffer): number[] {
  if (p.length < 2) {
    throw new PxbError("truncated", "truncated payload");
  }
  const n = p.readUInt16LE(0);
  if (p.length < 2 + n * 2) {
    throw new PxbError("truncated", "truncated payload");
  }
  const out: number[] = [];
  for (let i = 0; i < n; i++) {
    out.push(p.readUInt16LE(2 + i * 2));
  }
  return out;
}

function asBuffer(b: Uint8Array): Buffer {
  return Buffer.isBuffer(b) ? b : Buffer.from(b.buffer, b.byteOffset, b.byteLength);
}

function wireBool(v: unknown): number {
  if (typeof v !== "boolean") {
    throw new TypeError(`pxb: expected boolean field, got ${JSON.stringify(v)}`);
  }
  return v ? 1 : 0;
}

function isZero(v: unknown): boolean {
  return v === 0 || v === false;
}

function num(v: unknown): number {
  if (typeof v !== "number" || !Number.isInteger(v) || v < 0 || v > Number.MAX_SAFE_INTEGER) {
    throw new TypeError(`pxb: expected non-negative integer field, got ${JSON.stringify(v)}`);
  }
  return v;
}

function str(v: unknown): string {
  if (typeof v !== "string") {
    throw new TypeError(`pxb: expected string field, got ${JSON.stringify(v)}`);
  }
  return v;
}

function bytes(v: unknown): Uint8Array {
  if (!(v instanceof Uint8Array)) {
    throw new TypeError(`pxb: expected Uint8Array field, got ${JSON.stringify(v)}`);
  }
  return v;
}

function list(v: unknown): readonly number[] {
  if (!Array.isArray(v)) {
    throw new TypeError(`pxb: expected number[] field, got ${JSON.stringify(v)}`);
  }
  return v;
}

// ---------------------------------------------------------------------------
// Message declarations. Field order is wire order; keep in sync with Go/Rust.

const HELLO = [
  { tag: 1, kind: "string", key: "name" },
  { tag: 2, kind: "string", key: "version" },
  { tag: 3, kind: "u64", key: "caps" },
  { tag: 4, kind: "u64", key: "protocol" },
] as const satisfies readonly FieldSpec[];

/** The first frame from an extension. */
export interface Hello {
  name: string;
  version: string;
  caps: number;
  protocol: number;
}

export function encodeHello(m: Hello): Buffer {
  return encodeMsg(HELLO, m);
}
export function decodeHello(b: Uint8Array): Hello {
  return decodeMsg(HELLO, b) as unknown as Hello;
}

const HELLO_ACK = [
  { tag: 1, kind: "u64", key: "protocol" },
  { tag: 2, kind: "string", key: "phiVersion" },
  { tag: 3, kind: "string", key: "cwd" },
  { tag: 4, kind: "string", key: "sessionId" },
  { tag: 5, kind: "string", key: "extensionDir" },
] as const satisfies readonly FieldSpec[];

/** The host reply to Hello. */
export interface HelloAck {
  protocol: number;
  phiVersion: string;
  cwd: string;
  sessionId: string;
  extensionDir: string;
}

export function encodeHelloAck(m: HelloAck): Buffer {
  return encodeMsg(HELLO_ACK, m);
}
export function decodeHelloAck(b: Uint8Array): HelloAck {
  return decodeMsg(HELLO_ACK, b) as unknown as HelloAck;
}

const REGISTER_COMMAND = [
  { tag: 1, kind: "string", key: "name" },
  { tag: 2, kind: "string", key: "description" },
  // Host leaves `/name ` in the composer on picker accept / bare submit.
  // `false` omits the wire field (backward compatible).
  { tag: 3, kind: "u64", key: "needsArgs", opt: true, bool: true },
] as const satisfies readonly FieldSpec[];

/** Registers a slash command. */
export interface RegisterCommand {
  name: string;
  description: string;
  needsArgs: boolean;
}

export function encodeRegisterCommand(m: RegisterCommand): Buffer {
  return encodeMsg(REGISTER_COMMAND, m);
}
export function decodeRegisterCommand(b: Uint8Array): RegisterCommand {
  return decodeMsg(REGISTER_COMMAND, b) as unknown as RegisterCommand;
}

const REGISTER_TOOL = [
  { tag: 1, kind: "string", key: "name" },
  { tag: 2, kind: "string", key: "description" },
  { tag: 3, kind: "bytes", key: "schemaJson" },
  // Host RPC wait for this tool's result, in seconds. `0` omits the field
  // (host default). Host clamps to a maximum.
  { tag: 4, kind: "u64", key: "timeoutSec", opt: true },
  // Extension can answer detail-from-args requests.
  { tag: 5, kind: "u64", key: "hasDetail", opt: true, bool: true },
  // Side-effect-free; the host may batch read-only calls concurrently.
  { tag: 6, kind: "u64", key: "readable", opt: true, bool: true },
] as const satisfies readonly FieldSpec[];

/** Registers an LLM tool; schemaJson is opaque JSON Schema bytes. */
export interface RegisterTool {
  name: string;
  description: string;
  schemaJson: Uint8Array;
  timeoutSec: number;
  hasDetail: boolean;
  readable: boolean;
}

export function encodeRegisterTool(m: RegisterTool): Buffer {
  return encodeMsg(REGISTER_TOOL, m);
}
export function decodeRegisterTool(b: Uint8Array): RegisterTool {
  return decodeMsg(REGISTER_TOOL, b) as unknown as RegisterTool;
}

const TOOL_DETAIL_RESULT = [{ tag: 1, kind: "string", key: "detail" }] as const satisfies readonly FieldSpec[];

/** Ext→host reply for a detail-from-args request. */
export interface ToolDetailResult {
  detail: string;
}

export function encodeToolDetailResult(m: ToolDetailResult): Buffer {
  return encodeMsg(TOOL_DETAIL_RESULT, m);
}
export function decodeToolDetailResult(b: Uint8Array): ToolDetailResult {
  return decodeMsg(TOOL_DETAIL_RESULT, b) as unknown as ToolDetailResult;
}

const SUBSCRIBE = [
  { tag: 1, kind: "u16s", key: "events" },
  { tag: 2, kind: "u16s", key: "intercept" },
] as const satisfies readonly FieldSpec[];

/** Declares event / intercept interests. */
export interface Subscribe {
  events: number[];
  intercept: number[];
}

export function encodeSubscribe(m: Subscribe): Buffer {
  return encodeMsg(SUBSCRIBE, m);
}
export function decodeSubscribe(b: Uint8Array): Subscribe {
  return decodeMsg(SUBSCRIBE, b) as unknown as Subscribe;
}

const COMMAND_INVOKED = [
  { tag: 1, kind: "string", key: "name" },
  { tag: 2, kind: "string", key: "args" },
] as const satisfies readonly FieldSpec[];

/** Host→ext when the user runs a slash command. */
export interface CommandInvoked {
  name: string;
  args: string;
}

export function encodeCommandInvoked(m: CommandInvoked): Buffer {
  return encodeMsg(COMMAND_INVOKED, m);
}
export function decodeCommandInvoked(b: Uint8Array): CommandInvoked {
  return decodeMsg(COMMAND_INVOKED, b) as unknown as CommandInvoked;
}

const COMMAND_RESPONSE = [
  { tag: 1, kind: "u64", key: "ok", bool: true },
  { tag: 2, kind: "string", key: "error" },
  { tag: 3, kind: "string", key: "notify" },
  { tag: 4, kind: "string", key: "submit" },
] as const satisfies readonly FieldSpec[];

/** Ext→host slash command outcome. */
export interface CommandResponse {
  ok: boolean;
  error: string;
  notify: string;
  submit: string;
}

export function encodeCommandResponse(m: CommandResponse): Buffer {
  return encodeMsg(COMMAND_RESPONSE, m);
}
export function decodeCommandResponse(b: Uint8Array): CommandResponse {
  return decodeMsg(COMMAND_RESPONSE, b) as unknown as CommandResponse;
}

const TOOL_INVOKE = [
  { tag: 1, kind: "string", key: "name" },
  { tag: 2, kind: "bytes", key: "args" },
] as const satisfies readonly FieldSpec[];

/** Host→ext for a registered tool. */
export interface ToolInvoke {
  name: string;
  args: Uint8Array;
}

export function encodeToolInvoke(m: ToolInvoke): Buffer {
  return encodeMsg(TOOL_INVOKE, m);
}
export function decodeToolInvoke(b: Uint8Array): ToolInvoke {
  return decodeMsg(TOOL_INVOKE, b) as unknown as ToolInvoke;
}

const TOOL_RESULT_MSG = [
  { tag: 1, kind: "string", key: "content" },
  { tag: 2, kind: "string", key: "detail" },
  { tag: 3, kind: "string", key: "output" },
  { tag: 4, kind: "u64", key: "isError", bool: true },
  { tag: 5, kind: "string", key: "error" },
  // TUI tool row starts expanded (user toggle still wins).
  { tag: 6, kind: "u64", key: "expanded", opt: true, bool: true },
] as const satisfies readonly FieldSpec[];

/** Ext→host tool outcome. */
export interface ToolResultMsg {
  content: string;
  detail: string;
  output: string;
  isError: boolean;
  error: string;
  expanded: boolean;
}

export function encodeToolResultMsg(m: ToolResultMsg): Buffer {
  return encodeMsg(TOOL_RESULT_MSG, m);
}
export function decodeToolResultMsg(b: Uint8Array): ToolResultMsg {
  return decodeMsg(TOOL_RESULT_MSG, b) as unknown as ToolResultMsg;
}

const INTERCEPT_REQ = [
  { tag: 1, kind: "u64", key: "event" },
  { tag: 2, kind: "string", key: "toolName" },
  { tag: 3, kind: "string", key: "toolCallId" },
  { tag: 4, kind: "bytes", key: "input" },
  { tag: 5, kind: "string", key: "content" },
  { tag: 6, kind: "u64", key: "isError", bool: true },
  { tag: 7, kind: "string", key: "errText" },
  { tag: 8, kind: "string", key: "prompt" },
  { tag: 9, kind: "string", key: "reason" },
  { tag: 10, kind: "string", key: "targetId" },
  { tag: 11, kind: "u64", key: "turnIndex" },
] as const satisfies readonly FieldSpec[];

/** Host→ext for a blocking decision point. */
export interface InterceptReq {
  event: number;
  toolName: string;
  toolCallId: string;
  input: Uint8Array;
  content: string;
  isError: boolean;
  errText: string;
  prompt: string;
  reason: string;
  targetId: string;
  turnIndex: number;
}

export function encodeInterceptReq(m: InterceptReq): Buffer {
  return encodeMsg(INTERCEPT_REQ, m);
}
export function decodeInterceptReq(b: Uint8Array): InterceptReq {
  return decodeMsg(INTERCEPT_REQ, b) as unknown as InterceptReq;
}

const INTERCEPT_RESP = [
  { tag: 1, kind: "u64", key: "block", bool: true },
  { tag: 2, kind: "u64", key: "stop", bool: true },
  { tag: 3, kind: "u64", key: "cancel", bool: true },
  { tag: 4, kind: "string", key: "reason" },
  { tag: 5, kind: "bytes", key: "input" },
  { tag: 6, kind: "string", key: "content" },
  { tag: 7, kind: "string", key: "context" },
  { tag: 8, kind: "string", key: "systemPromptAppend" },
  { tag: 9, kind: "string", key: "toast" },
  { tag: 10, kind: "u64", key: "handled", bool: true },
  { tag: 11, kind: "string", key: "prompt" },
  { tag: 12, kind: "u64", key: "continue", bool: true },
] as const satisfies readonly FieldSpec[];

/** Ext→host intercept reply. */
export interface InterceptResp {
  block: boolean;
  stop: boolean;
  cancel: boolean;
  reason: string;
  input: Uint8Array;
  content: string;
  context: string;
  systemPromptAppend: string;
  toast: string;
  handled: boolean;
  prompt: string;
  continue: boolean;
}

export function encodeInterceptResp(m: InterceptResp): Buffer {
  return encodeMsg(INTERCEPT_RESP, m);
}
export function decodeInterceptResp(b: Uint8Array): InterceptResp {
  return decodeMsg(INTERCEPT_RESP, b) as unknown as InterceptResp;
}

const EVENT_NOTIFY = [
  { tag: 1, kind: "u64", key: "event" },
  { tag: 2, kind: "string", key: "toolName" },
  { tag: 3, kind: "string", key: "toolCallId" },
  { tag: 4, kind: "bytes", key: "input" },
  { tag: 5, kind: "u64", key: "isError", bool: true },
  { tag: 6, kind: "string", key: "prompt" },
  { tag: 7, kind: "string", key: "reason" },
  { tag: 8, kind: "u64", key: "turnIndex" },
  { tag: 9, kind: "string", key: "sessionId" },
  { tag: 10, kind: "string", key: "previousSessionId" },
  { tag: 11, kind: "string", key: "targetSessionId" },
] as const satisfies readonly FieldSpec[];

/** Fire-and-forget host→ext lifecycle event. */
export interface EventNotify {
  event: number;
  toolName: string;
  toolCallId: string;
  input: Uint8Array;
  isError: boolean;
  prompt: string;
  reason: string;
  turnIndex: number;
  sessionId: string;
  previousSessionId: string;
  targetSessionId: string;
}

export function encodeEventNotify(m: EventNotify): Buffer {
  return encodeMsg(EVENT_NOTIFY, m);
}
export function decodeEventNotify(b: Uint8Array): EventNotify {
  return decodeMsg(EVENT_NOTIFY, b) as unknown as EventNotify;
}

const NOTIFY_MSG = [
  { tag: 1, kind: "string", key: "level" },
  { tag: 2, kind: "string", key: "message" },
  { tag: 3, kind: "string", key: "status" },
  { tag: 4, kind: "u64", key: "statusSet", bool: true },
] as const satisfies readonly FieldSpec[];

/** Ext→host UI toast / footer status. */
export interface NotifyMsg {
  level: string;
  message: string;
  status: string;
  statusSet: boolean;
}

export function encodeNotifyMsg(m: NotifyMsg): Buffer {
  return encodeMsg(NOTIFY_MSG, m);
}
export function decodeNotifyMsg(b: Uint8Array): NotifyMsg {
  return decodeMsg(NOTIFY_MSG, b) as unknown as NotifyMsg;
}

const HOST_REQUEST = [
  { tag: 1, kind: "string", key: "method" },
  { tag: 2, kind: "string", key: "arg" },
] as const satisfies readonly FieldSpec[];

/** Ext→host capability RPC. */
export interface HostRequest {
  method: string;
  arg: string;
}

export function encodeHostRequest(m: HostRequest): Buffer {
  return encodeMsg(HOST_REQUEST, m);
}
export function decodeHostRequest(b: Uint8Array): HostRequest {
  return decodeMsg(HOST_REQUEST, b) as unknown as HostRequest;
}

const HOST_RESULT = [
  { tag: 1, kind: "u64", key: "ok", bool: true },
  { tag: 2, kind: "string", key: "error" },
  { tag: 3, kind: "string", key: "body" },
] as const satisfies readonly FieldSpec[];

/** Host→ext reply to a HostRequest. */
export interface HostResult {
  ok: boolean;
  error: string;
  body: string;
}

export function encodeHostResult(m: HostResult): Buffer {
  return encodeMsg(HOST_RESULT, m);
}
export function decodeHostResult(b: Uint8Array): HostResult {
  return decodeMsg(HOST_RESULT, b) as unknown as HostResult;
}

const SESSION_META = [
  { tag: 1, kind: "string", key: "sessionId" },
  { tag: 2, kind: "string", key: "cwd" },
] as const satisfies readonly FieldSpec[];

/** Host→ext session identity push. */
export interface SessionMeta {
  sessionId: string;
  cwd: string;
}

export function encodeSessionMeta(m: SessionMeta): Buffer {
  return encodeMsg(SESSION_META, m);
}
export function decodeSessionMeta(b: Uint8Array): SessionMeta {
  return decodeMsg(SESSION_META, b) as unknown as SessionMeta;
}
