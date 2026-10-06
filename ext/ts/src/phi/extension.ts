// Author-facing SDK for a Phi PXB extension — a TS port of
// ext/rust/src/phi/mod.rs. Build an `Extension`, register tools / slash
// commands / event handlers, then `await ext.run()` to speak PXB on
// stdin/stdout until the host shuts down.
//
// The run loop is a single background frame pump. Handlers are async; while a
// command handler waits (e.g. on `ctx.confirm`), the pump keeps reading: it
// resolves the pending host RPC and defers work frames into a bounded inbox
// that the loop replays in order once the handler returns — the same serial
// semantics the Rust SDK enforces with the borrow checker.

import { ByteSource, PxbError, ioError, readFrame, writeFrame, type Frame } from "../pxb/codec.ts";
import {
  decodeCommandInvoked,
  decodeEventNotify,
  decodeHelloAck,
  decodeHostResult,
  decodeInterceptReq,
  decodeSessionMeta,
  decodeToolInvoke,
  encodeCommandResponse,
  encodeHello,
  encodeHostRequest,
  encodeInterceptResp,
  encodeNotifyMsg,
  encodeRegisterCommand,
  encodeRegisterTool,
  encodeSubscribe,
  encodeToolDetailResult,
  encodeToolResultMsg,
  type CommandResponse,
  type EventNotify,
  type InterceptReq,
  type InterceptResp,
  type ToolResultMsg,
} from "../pxb/msg.ts";
import {
  CAP_COMMANDS,
  CAP_EVENTS,
  CAP_INTERCEPT,
  CAP_TOOLS,
  Event,
  FLAG_HAS_ID,
  MAX_PAYLOAD,
  PROTOCOL_VERSION,
  TYPE_COMMAND_INVOKED,
  TYPE_COMMAND_RESPONSE,
  TYPE_EVENT,
  TYPE_HELLO,
  TYPE_HELLO_ACK,
  TYPE_HOST_REQUEST,
  TYPE_HOST_RESULT,
  TYPE_INTERCEPT,
  TYPE_INTERCEPT_RESPONSE,
  TYPE_NOTIFY,
  TYPE_READY,
  TYPE_REGISTER_COMMAND,
  TYPE_REGISTER_TOOL,
  TYPE_SESSION_META,
  TYPE_SHUTDOWN,
  TYPE_SHUTDOWN_ACK,
  TYPE_SUBSCRIBE,
  TYPE_TOOL_DETAIL_INVOKE,
  TYPE_TOOL_DETAIL_RESULT,
  TYPE_TOOL_INVOKE,
  TYPE_TOOL_RESULT,
} from "../pxb/types.ts";
import { schemaBytes, type SchemaInput } from "./schema.ts";
import type { Writable } from "node:stream";

/** Host metadata filled by the hello handshake (and refreshed by
 * SessionMeta pushes). */
export interface HostInfo {
  cwd: string;
  sessionId: string;
  extensionDir: string;
  phiVersion: string;
}

/** Outcome of a tool execution; absent fields mean empty/false. */
export interface ToolResult {
  content?: string;
  detail?: string;
  output?: string;
  /** Ask the TUI to start the tool row open (user toggle still wins). */
  expanded?: boolean;
}

export type ToolExecute = (args: Uint8Array) => ToolResult | Promise<ToolResult>;

/** An LLM-callable tool. Plain data — build it literally or with the
 * `tool()` helper:
 *
 * ```ts
 * m.registerTool({
 *   name: "fetch",
 *   description: "GET a URL",
 *   schema: Schema.object().property("url", Schema.string()),
 *   execute: async (args) => ({ content: await fetchLen(args) }),
 *   timeoutSec: 60,      // host wait; 0 = host default (30s)
 *   readable: true,      // side-effect-free: host may batch calls
 *   detailFromArgs: (args) => oneLineTuiDetail(args),
 * });
 * ```
 */
export interface Tool {
  name: string;
  description: string;
  schema: SchemaInput;
  /** Args are raw JSON; may be async — the run loop awaits it. */
  execute: ToolExecute;
  /** Host RPC wait for `execute`, in seconds. 0 = host default. */
  timeoutSec?: number;
  /** Side-effect-free: the host may run a batch of readable calls
   * concurrently. */
  readable?: boolean;
  /** Optional one-line TUI detail from raw JSON args (before execute). */
  detailFromArgs?: (args: Uint8Array) => string;
}

/** Identity constructor; keeps tool literals honest without a builder. */
export function tool(t: Tool): Tool {
  return t;
}

/** Handler context handed to command handlers. */
export interface CommandHandler {
  (args: string, ctx: Context): void | Promise<void>;
}

/** A slash command. Throwing inside `handler` fails the command with the
 * error's message. */
export interface Command {
  description: string;
  handler: CommandHandler;
  /** Leave `/name ` in the composer on picker accept / bare submit. */
  needsArgs?: boolean;
}

/** The user's choice for a confirm dialog. */
export interface ConfirmReply {
  ok: boolean;
}

/** Modal yes/no dialog shown by the host; missing labels use host defaults. */
export interface ConfirmRequest {
  title?: string;
  message?: string;
  /** Host default: "Yes". */
  yes?: string;
  /** Host default: "No". */
  no?: string;
  danger?: boolean;
}

export interface ToolCallEvent {
  toolName: string;
  toolCallId: string;
  input: Uint8Array;
}

export interface ToolCallResult {
  block?: boolean;
  reason?: string;
  /** Present rewrites the tool input; absent keeps it. */
  input?: Uint8Array;
  context?: string;
}

export interface ToolResultEvent {
  toolName: string;
  toolCallId: string;
  input: Uint8Array;
  content: string;
  isError: boolean;
  err: string;
}

export interface ToolResultResult {
  /** Present rewrites the tool output; absent keeps it. */
  content?: string;
  context?: string;
  /** Ends the agent loop. */
  stop?: boolean;
  reason?: string;
}

export interface BeforeAgentStartEvent {
  prompt: string;
}

export interface BeforeAgentStartResult {
  /** Present replaces the user prompt. */
  prompt?: string;
  systemPromptAppend?: string;
}

export interface SessionBeforeSwitchEvent {
  reason: string;
  targetSessionId: string;
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
  /** `true` swallows the prompt (no agent loop). */
  handled?: boolean;
  /** Present replaces the prompt text. */
  text?: string;
  reason?: string;
}

export interface TurnStoppingEvent {
  turnIndex: number;
}

export interface TurnStoppingResult {
  /** Forces another agent step. */
  continue?: boolean;
  /** Injected as a user message when continuing. */
  message?: string;
  reason?: string;
}

type EventSink = (ev: EventNotify) => void;

interface InterceptHandlers {
  toolCall?: (ev: ToolCallEvent) => ToolCallResult | null | void;
  toolResult?: (ev: ToolResultEvent) => ToolResultResult | null | void;
  beforeAgentStart?: (ev: BeforeAgentStartEvent) => BeforeAgentStartResult | null | void;
  sessionBeforeSwitch?: (ev: SessionBeforeSwitchEvent) => SessionBeforeSwitchResult | null | void;
  userInput?: (ev: UserInputEvent) => UserInputResult | null | void;
  turnStopping?: (ev: TurnStoppingEvent) => TurnStoppingResult | null | void;
}

// Bound both frame overhead and payload memory while a command waits on the
// host — same caps as the Rust SDK.
const MAX_DEFERRED_FRAMES = 32;

/** Mutable state owned by the run loop; handlers reach it through `Context`
 * only. */
interface Runtime {
  src: ByteSource;
  out: Writable;
  host: HostInfo;
  pendingSubmit: string | null;
  nextHostId: number;
  /** In-flight host RPCs (confirm), id → resolver. */
  pending: Map<number, (r: { ok: boolean } | null) => void>;
  /** Work frames deferred while a handler waits on the host. */
  inbox: Frame[];
  inboxBytes: number;
  inboxWaiter: (() => void) | null;
  /** True while the serve loop is inside a dispatch (Rust: the loop is not
   * at a read point, so a shutdown must queue behind the in-flight reply). */
  draining: boolean;
  /** First terminal condition wins: clean shutdown (`ok`) or a fatal error. */
  terminal: { ok: boolean; error: unknown } | null;
}

/** Everything the serve loop reads; captured once from an Extension so the
 * free-standing helpers never touch private state. */
interface Registry {
  name: string;
  version: string;
  tools: Tool[];
  commands: Map<string, Command>;
  eventCodes: number[];
  interceptCodes: number[];
  eventHandlers: Map<number, EventSink>;
  intercept: InterceptHandlers;
}

/** The author-facing registration surface for a PXB extension. */
export class Extension {
  readonly name: string;
  readonly version: string;

  private readonly reg: Registry;

  constructor(name: string, version: string) {
    if (name === "") {
      throw new TypeError("pxb extension: name must not be empty");
    }
    this.name = name;
    this.version = version;
    this.reg = {
      name,
      version,
      tools: [],
      commands: new Map(),
      eventCodes: [],
      interceptCodes: [],
      eventHandlers: new Map(),
      intercept: {},
    };
  }

  /** Adds an LLM-callable tool. Empty names are ignored. */
  registerTool(t: Tool): this {
    if (t.name !== "") {
      this.reg.tools.push(t);
    }
    return this;
  }

  /** Adds a slash command; the first registration of a name wins (builtins
   * cannot be overridden at host level either). */
  registerCommand(name: string, cmd: Command): this {
    if (name !== "" && !this.reg.commands.has(name)) {
      this.reg.commands.set(name, cmd);
    }
    return this;
  }

  /** Registers a pre-gate tool_call intercept. */
  onToolCall(f: (ev: ToolCallEvent) => ToolCallResult | null | void): this {
    this.reg.intercept.toolCall = f;
    pushUnique(this.reg.interceptCodes, Event.ToolCall);
    return this;
  }

  /** Registers a post-tool tool_result intercept. */
  onToolResult(f: (ev: ToolResultEvent) => ToolResultResult | null | void): this {
    this.reg.intercept.toolResult = f;
    pushUnique(this.reg.interceptCodes, Event.ToolResult);
    return this;
  }

  /** May append system prompt text before the agent loop starts. */
  onBeforeAgentStart(
    f: (ev: BeforeAgentStartEvent) => BeforeAgentStartResult | null | void,
  ): this {
    this.reg.intercept.beforeAgentStart = f;
    pushUnique(this.reg.interceptCodes, Event.BeforeAgentStart);
    return this;
  }

  /** May cancel a session switch. */
  onSessionBeforeSwitch(
    f: (ev: SessionBeforeSwitchEvent) => SessionBeforeSwitchResult | null | void,
  ): this {
    this.reg.intercept.sessionBeforeSwitch = f;
    pushUnique(this.reg.interceptCodes, Event.SessionBeforeSwitch);
    return this;
  }

  /** May transform or swallow the user prompt before the agent loop. */
  onUserInput(f: (ev: UserInputEvent) => UserInputResult | null | void): this {
    this.reg.intercept.userInput = f;
    pushUnique(this.reg.interceptCodes, Event.UserInput);
    return this;
  }

  /** May steer another agent step when the model stops with no tools. */
  onTurnStopping(f: (ev: TurnStoppingEvent) => TurnStoppingResult | null | void): this {
    this.reg.intercept.turnStopping = f;
    pushUnique(this.reg.interceptCodes, Event.TurnStopping);
    return this;
  }

  /** Adds a fire-and-forget lifecycle listener; the payload is the wire
   * EventNotify. Unknown codes are ignored. */
  subscribe(event: number, f: EventSink): this {
    if (event === 0) {
      return this;
    }
    pushUnique(this.reg.eventCodes, event);
    this.reg.eventHandlers.set(event, f);
    return this;
  }

  /** Speaks PXB on stdin/stdout until the host shuts down. Resolves after
   * the shutdown handshake; rejects on protocol or I/O failure. The
   * extension process is expected to exit after `run()` settles — stdin is
   * unreferenced so Node does not linger on a host-held pipe. */
  async run(): Promise<void> {
    const rt: Runtime = {
      src: new ByteSource(process.stdin),
      out: process.stdout,
      host: { cwd: "", sessionId: "", extensionDir: "", phiVersion: "" },
      pendingSubmit: null,
      nextHostId: 0,
      pending: new Map(),
      inbox: [],
      inboxBytes: 0,
      inboxWaiter: null,
      draining: false,
      terminal: null,
    };

    await handshake(this.reg, rt);
    await register(this.reg, rt);

    void pump(rt); // ends on its own: EOF, terminal, or process exit
    try {
      await serve(this.reg, rt);
    } finally {
      releaseStdin();
    }
  }
}

/** Lets the process exit while the host still holds its stdin open. */
function releaseStdin(): void {
  const stdin = process.stdin as { unref?: () => void };
  if (typeof stdin.unref === "function") {
    stdin.unref();
  }
}

async function handshake(reg: Registry, rt: Runtime): Promise<void> {
  let caps = 0;
  if (reg.commands.size > 0) caps |= CAP_COMMANDS;
  if (reg.tools.length > 0) caps |= CAP_TOOLS;
  if (reg.eventCodes.length > 0) caps |= CAP_EVENTS;
  if (reg.interceptCodes.length > 0) caps |= CAP_INTERCEPT;

  const hello = encodeHello({
    name: reg.name,
    version: reg.version,
    caps,
    protocol: PROTOCOL_VERSION,
  });
  await writeFrame(rt.out, TYPE_HELLO, 0, 0, hello);

  const f = await readFrame(rt.src);
  if (f.header.type !== TYPE_HELLO_ACK) {
    throw new PxbError("unexpectedFrame", `expected hello_ack frame, got ${f.header.type}`);
  }
  const ack = decodeHelloAck(f.body);
  rt.host = {
    cwd: ack.cwd,
    sessionId: ack.sessionId,
    extensionDir: ack.extensionDir,
    phiVersion: ack.phiVersion,
  };
}

/** Announces tools, commands, and subscription interest, then signals READY. */
async function register(reg: Registry, rt: Runtime): Promise<void> {
  for (const t of reg.tools) {
    const body = encodeRegisterTool({
      name: t.name,
      description: t.description,
      schemaJson: schemaBytes(t.schema),
      timeoutSec: t.timeoutSec ?? 0,
      hasDetail: t.detailFromArgs !== undefined,
      readable: t.readable ?? false,
    });
    await writeFrame(rt.out, TYPE_REGISTER_TOOL, 0, 0, body);
  }
  for (const [name, cmd] of reg.commands) {
    const body = encodeRegisterCommand({
      name,
      description: cmd.description,
      needsArgs: cmd.needsArgs ?? false,
    });
    await writeFrame(rt.out, TYPE_REGISTER_COMMAND, 0, 0, body);
  }
  if (reg.eventCodes.length > 0 || reg.interceptCodes.length > 0) {
    const body = encodeSubscribe({ events: reg.eventCodes, intercept: reg.interceptCodes });
    await writeFrame(rt.out, TYPE_SUBSCRIBE, 0, 0, body);
  }
  await writeFrame(rt.out, TYPE_READY, 0, 0, Buffer.alloc(0));
}

/** Reads frames forever and routes them: host RPC replies wake pending
 * waiters, work frames queue for the serve loop, shutdown ends the run. */
async function pump(rt: Runtime): Promise<void> {
  try {
    for (;;) {
      const f = await readFrame(rt.src);
      route(rt, f);
    }
  } catch (e) {
    setTerminal(rt, { ok: false, error: e });
  }
}

function route(rt: Runtime, f: Frame): void {
  const { type, flags, id } = f.header;
  if (type === TYPE_HOST_RESULT) {
    if ((flags & FLAG_HAS_ID) !== 0) {
      const resolve = rt.pending.get(id);
      if (resolve) {
        rt.pending.delete(id);
        let result: { ok: boolean };
        try {
          result = decodeHostResult(f.body);
        } catch {
          result = { ok: false };
        }
        resolve(result);
      }
    }
    // Unmatched or flagless results are dropped, like the Rust serve loop.
    return;
  }
  if (type === TYPE_SHUTDOWN) {
    // Mirror the Rust loop's single-threaded ordering: ack immediately when
    // idle or while a confirm wait needs waking, but never ahead of an
    // in-flight dispatch's reply (the host kills us after the ack).
    if (rt.pending.size > 0 || !rt.draining) {
      // Terminal only after the ack is flushed, so the process cannot exit
      // before the host sees it.
      void writeFrame(rt.out, TYPE_SHUTDOWN_ACK, 0, 0, Buffer.alloc(0)).then(
        () => setTerminal(rt, { ok: true, error: null }),
        (e: unknown) => setTerminal(rt, { ok: false, error: e }),
      );
      return;
    }
    defer(rt, f); // the serve loop acks after the current reply
    return;
  }
  switch (type) {
    case TYPE_COMMAND_INVOKED:
    case TYPE_TOOL_INVOKE:
    case TYPE_TOOL_DETAIL_INVOKE:
    case TYPE_INTERCEPT:
    case TYPE_EVENT:
    case TYPE_SESSION_META:
      defer(rt, f);
      return;
    default:
      return; // unknown frame types are consumed by length; ignore
  }
}

function defer(rt: Runtime, f: Frame): void {
  if (rt.inbox.length >= MAX_DEFERRED_FRAMES || rt.inboxBytes + f.body.length > MAX_PAYLOAD) {
    setTerminal(rt, {
      ok: false,
      error: new PxbError("io", "confirm deferred queue full; reduce host request backlog"),
    });
    return;
  }
  rt.inboxBytes += f.body.length;
  rt.inbox.push(f);
  const wake = rt.inboxWaiter;
  if (wake) {
    rt.inboxWaiter = null;
    wake();
  }
}

function popInbox(rt: Runtime): Promise<Frame | null> {
  const f = rt.inbox.shift();
  if (f) {
    rt.inboxBytes -= f.body.length;
    return Promise.resolve(f);
  }
  if (rt.terminal) {
    return Promise.resolve(null);
  }
  return new Promise<Frame | null>((resolve) => {
    // The waker only unblocks the loop; it re-polls the queue / terminal.
    rt.inboxWaiter = () => resolve(null);
  });
}

function setTerminal(rt: Runtime, t: { ok: boolean; error: unknown }): void {
  if (rt.terminal) return; // first terminal condition wins
  rt.terminal = t;
  for (const resolve of rt.pending.values()) {
    resolve(null); // waiters give up with a default reply
  }
  rt.pending.clear();
  const wake = rt.inboxWaiter;
  if (wake) {
    rt.inboxWaiter = null;
    wake();
  }
}

/** Dispatches frames until the host shuts down, one handler at a time —
 * handlers are serial, so state cannot race. */
async function serve(reg: Registry, rt: Runtime): Promise<void> {
  for (;;) {
    if (rt.terminal) {
      return failTerminal(rt);
    }
    const f = await popInbox(rt);
    if (!f) {
      if (rt.terminal) {
        return failTerminal(rt);
      }
      continue;
    }
    rt.draining = true;
    try {
      await dispatch(reg, rt, f);
    } finally {
      rt.draining = false;
    }
  }
}

/** Resolves on clean shutdown; rethrows the fatal error otherwise. */
function failTerminal(rt: Runtime): void {
  const t = rt.terminal;
  if (t && !t.ok) {
    throw ioError(t.error);
  }
}

async function dispatch(reg: Registry, rt: Runtime, f: Frame): Promise<void> {
  switch (f.header.type) {
    case TYPE_SHUTDOWN:
      return serveShutdown(rt);
    case TYPE_COMMAND_INVOKED:
      return serveCommand(reg, rt, f);
    case TYPE_TOOL_INVOKE:
      return serveTool(reg, rt, f);
    case TYPE_TOOL_DETAIL_INVOKE:
      return serveToolDetail(reg, rt, f);
    case TYPE_INTERCEPT:
      return serveIntercept(reg, rt, f);
    case TYPE_EVENT:
      return serveEvent(reg, f);
    case TYPE_SESSION_META:
      return applySessionMeta(rt, f);
    default:
      return;
  }
}

/** Acknowledges a shutdown that queued behind an in-flight dispatch. */
async function serveShutdown(rt: Runtime): Promise<void> {
  await writeFrame(rt.out, TYPE_SHUTDOWN_ACK, 0, 0, Buffer.alloc(0));
  setTerminal(rt, { ok: true, error: null });
}

/** Invokes a registered slash-command handler and replies with its outcome.
 * An unknown command fails with "unknown command". */
async function serveCommand(reg: Registry, rt: Runtime, frame: Frame): Promise<void> {
  const inv = decodeCommandInvoked(frame.body);
  const resp: CommandResponse = { ok: true, error: "", notify: "", submit: "" };
  const cmd = reg.commands.get(inv.name);
  if (cmd) {
    const ctx = new Context(rt, rt.host.cwd, rt.host.sessionId);
    try {
      await cmd.handler(inv.args, ctx);
    } catch (e) {
      resp.ok = false;
      resp.error = e instanceof Error ? e.message : String(e);
    }
  } else {
    resp.ok = false;
    resp.error = "unknown command";
  }
  if (rt.terminal) {
    return; // shutdown (or failure) while the handler ran; nothing to reply
  }
  resp.submit = rt.pendingSubmit ?? "";
  rt.pendingSubmit = null;
  await writeFrame(
    rt.out,
    TYPE_COMMAND_RESPONSE,
    frame.header.flags,
    frame.header.id,
    encodeCommandResponse(resp),
  );
}

/** Executes a tool and replies with its result, or an error result when the
 * tool is unknown or its handler failed. */
async function serveTool(reg: Registry, rt: Runtime, frame: Frame): Promise<void> {
  const inv = decodeToolInvoke(frame.body);
  const t = reg.tools.find((t) => t.name === inv.name);
  let tr: ToolResultMsg;
  if (!t) {
    tr = toolError("unknown tool");
  } else {
    try {
      const r = await t.execute(inv.args);
      tr = {
        content: r.content ?? "",
        detail: r.detail ?? "",
        output: r.output ?? "",
        isError: false,
        error: "",
        expanded: r.expanded ?? false,
      };
    } catch (e) {
      tr = toolError(e instanceof Error ? e.message : String(e));
    }
  }
  let body = encodeToolResultMsg(tr);
  if (body.length > MAX_PAYLOAD) {
    body = encodeToolResultMsg(
      toolError(
        `tool response exceeds PXB payload limit (${MAX_PAYLOAD} bytes); reduce tool output`,
      ),
    );
  }
  await writeFrame(rt.out, TYPE_TOOL_RESULT, frame.header.flags, frame.header.id, body);
}

/** Returns a one-line TUI detail for raw tool args (or empty when
 * unset/unknown). */
async function serveToolDetail(reg: Registry, rt: Runtime, frame: Frame): Promise<void> {
  const inv = decodeToolInvoke(frame.body);
  const t = reg.tools.find((t) => t.name === inv.name);
  let detail = "";
  try {
    detail = t?.detailFromArgs ? t.detailFromArgs(inv.args) : "";
  } catch {
    detail = "";
  }
  await writeFrame(
    rt.out,
    TYPE_TOOL_DETAIL_RESULT,
    frame.header.flags,
    frame.header.id,
    encodeToolDetailResult({ detail }),
  );
}

/** Replies to one intercept request with the registered handler's result. */
async function serveIntercept(reg: Registry, rt: Runtime, frame: Frame): Promise<void> {
  const req = decodeInterceptReq(frame.body);
  const resp = handleIntercept(reg, req);
  await writeFrame(
    rt.out,
    TYPE_INTERCEPT_RESPONSE,
    frame.header.flags,
    frame.header.id,
    encodeInterceptResp(resp),
  );
}

/** Dispatches one intercept request to the registered handler. A missing
 * handler (or one returning null) yields an empty response — the host treats
 * that as "no change". */
function handleIntercept(reg: Registry, req: InterceptReq): InterceptResp {
  const resp: InterceptResp = {
    block: false,
    stop: false,
    cancel: false,
    reason: "",
    input: new Uint8Array(0),
    content: "",
    context: "",
    systemPromptAppend: "",
    toast: "",
    handled: false,
    prompt: "",
    continue: false,
  };
  const h = reg.intercept;
  switch (req.event) {
    case Event.ToolCall: {
      if (!h.toolCall) return resp;
      const r = h.toolCall({
        toolName: req.toolName,
        toolCallId: req.toolCallId,
        input: req.input,
      });
      if (!r) return resp;
      resp.block = r.block ?? false;
      resp.reason = r.reason ?? "";
      resp.context = r.context ?? "";
      if (r.input !== undefined) resp.input = r.input;
      return resp;
    }
    case Event.ToolResult: {
      if (!h.toolResult) return resp;
      const r = h.toolResult({
        toolName: req.toolName,
        toolCallId: req.toolCallId,
        input: req.input,
        content: req.content,
        isError: req.isError,
        err: req.errText,
      });
      if (!r) return resp;
      resp.context = r.context ?? "";
      resp.stop = r.stop ?? false;
      resp.reason = r.reason ?? "";
      if (r.content !== undefined) resp.content = r.content;
      return resp;
    }
    case Event.BeforeAgentStart: {
      if (!h.beforeAgentStart) return resp;
      const r = h.beforeAgentStart({ prompt: req.prompt });
      if (!r) return resp;
      resp.systemPromptAppend = r.systemPromptAppend ?? "";
      if (r.prompt !== undefined) resp.prompt = r.prompt;
      return resp;
    }
    case Event.SessionBeforeSwitch: {
      if (!h.sessionBeforeSwitch) return resp;
      const r = h.sessionBeforeSwitch({
        reason: req.reason,
        targetSessionId: req.targetId,
      });
      if (!r) return resp;
      resp.cancel = r.cancel ?? false;
      resp.reason = r.reason ?? "";
      resp.toast = r.toast ?? "";
      return resp;
    }
    case Event.UserInput: {
      if (!h.userInput) return resp;
      const r = h.userInput({ text: req.prompt });
      if (!r) return resp;
      resp.handled = r.handled ?? false;
      resp.reason = r.reason ?? "";
      if (r.text !== undefined) resp.prompt = r.text;
      return resp;
    }
    case Event.TurnStopping: {
      if (!h.turnStopping) return resp;
      const r = h.turnStopping({ turnIndex: req.turnIndex });
      if (!r) return resp;
      resp.continue = r.continue ?? false;
      resp.prompt = r.message ?? "";
      resp.reason = r.reason ?? "";
      return resp;
    }
    default:
      return resp;
  }
}

function serveEvent(reg: Registry, frame: Frame): void {
  let ev: EventNotify;
  try {
    ev = decodeEventNotify(frame.body);
  } catch {
    return; // malformed event push must not kill the run loop
  }
  reg.eventHandlers.get(ev.event)?.(ev);
}

/** Applies a session-meta push to host info; empty fields mean "no change". */
function applySessionMeta(rt: Runtime, frame: Frame): void {
  try {
    const meta = decodeSessionMeta(frame.body);
    if (meta.sessionId !== "") rt.host.sessionId = meta.sessionId;
    if (meta.cwd !== "") rt.host.cwd = meta.cwd;
  } catch {
    return;
  }
}

/** An error tool result: the message goes to both `error` and `content` so
 * the host surfaces it whichever field it renders. */
function toolError(message: string): ToolResultMsg {
  return { content: message, detail: "", output: "", isError: true, error: message, expanded: false };
}

/** Interaction surface handed to command handlers. Host traffic goes over
 * the same PXB pipe the run loop owns — awaiting `confirm` parks the
 * handler while the pump keeps the pipe serviced. */
export class Context {
  readonly cwd: string;
  readonly sessionId: string;
  readonly hasUI = true;

  private readonly rt: Runtime;

  constructor(rt: Runtime, cwd: string, sessionId: string) {
    this.rt = rt;
    this.cwd = cwd;
    this.sessionId = sessionId;
  }

  /** Pushes a toast to the host (`level`: `info` | `warning` | `error`).
   * Write failures are swallowed, matching the Rust SDK. */
  async notify(level: string, message: string): Promise<void> {
    await this.push(
      TYPE_NOTIFY,
      0,
      0,
      encodeNotifyMsg({ level, message, status: "", statusSet: false }),
    );
  }

  /** Updates the host footer extension status (empty text clears). */
  async setStatus(text: string): Promise<void> {
    await this.push(
      TYPE_NOTIFY,
      0,
      0,
      encodeNotifyMsg({ level: "", message: "", status: text, statusSet: true }),
    );
  }

  /** Queues a prompt for the host to send after the current slash command
   * returns. */
  submit(text: string): void {
    this.rt.pendingSubmit = text;
  }

  /** Asks the host to enqueue a user turn (fire-and-forget). */
  async sendUserMessage(text: string): Promise<void> {
    if (text === "") return;
    await this.push(
      TYPE_HOST_REQUEST,
      0,
      0,
      encodeHostRequest({ method: "send_user_message", arg: text }),
    );
  }

  /** Shows a yes/no dialog on the host and waits for the answer. */
  async confirm(title: string, message: string): Promise<ConfirmReply> {
    return this.confirmOpts({ title, message });
  }

  /** {@link confirm} with labels / danger styling. */
  async confirmOpts(req: ConfirmRequest): Promise<ConfirmReply> {
    if (this.rt.terminal) {
      return { ok: false };
    }
    this.rt.nextHostId = (this.rt.nextHostId + 1) >>> 0; // wrapping add
    const id = this.rt.nextHostId;
    // Register the waiter before the bytes go out: a reply cannot be missed,
    // no matter how fast the host answers.
    const reply = new Promise<{ ok: boolean } | null>((resolve) => {
      this.rt.pending.set(id, resolve);
    });
    try {
      await writeFrame(
        this.rt.out,
        TYPE_HOST_REQUEST,
        FLAG_HAS_ID,
        id,
        encodeHostRequest({ method: "confirm", arg: confirmRequestJson(req) }),
      );
    } catch (e) {
      this.rt.pending.delete(id);
      setTerminal(this.rt, { ok: false, error: e });
      return { ok: false };
    }
    // The pump resolves `reply`; a terminal condition wakes it with null.
    const result = await reply;
    return { ok: result?.ok ?? false };
  }

  /** Fire-and-forget frame write; failures end the run quietly. */
  private async push(type: number, flags: number, id: number, body: Uint8Array): Promise<void> {
    try {
      await writeFrame(this.rt.out, type, flags, id, Buffer.from(body));
    } catch (e) {
      setTerminal(this.rt, { ok: false, error: e });
    }
  }
}

/** Serializes a ConfirmRequest as the JSON the host parses. The host
 * unmarshals into Go's ext.ConfirmRequest (Title/Message/Yes/No/Danger), so
 * key names and order are pinned. */
export function confirmRequestJson(req: ConfirmRequest): string {
  return JSON.stringify({
    Title: req.title ?? "",
    Message: req.message ?? "",
    Yes: req.yes ?? "",
    No: req.no ?? "",
    Danger: req.danger ?? false,
  });
}

function pushUnique(xs: number[], v: number): void {
  if (!xs.includes(v)) {
    xs.push(v);
  }
}




