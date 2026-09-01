import { stdin as defaultStdin, stdout as defaultStdout } from "node:process";
import type { Readable, Writable } from "node:stream";

import {
  CapCommands,
  CapEvents,
  CapIntercept,
  CapTools,
  EvBeforeAgentStart,
  EvSessionBeforeSwitch,
  EvToolCall,
  EvToolResult,
  EvTurnStopping,
  EvUserInput,
  FlagHasID,
  FrameReader,
  FrameWriter,
  ProtocolVersion,
  TypeCommandInvoked,
  TypeCommandResponse,
  TypeEvent,
  TypeHello,
  TypeHelloAck,
  TypeHostRequest,
  TypeHostResult,
  TypeIntercept,
  TypeInterceptResponse,
  TypeNotify,
  TypeReady,
  TypeRegisterCommand,
  TypeRegisterTool,
  TypeSessionMeta,
  TypeShutdown,
  TypeShutdownAck,
  TypeSubscribe,
  TypeToolInvoke,
  TypeToolResult,
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
  encodeNotify,
  encodeRegisterCommand,
  encodeRegisterTool,
  encodeSubscribe,
  encodeToolResult,
  eventCode,
  type EventNotify,
  type InterceptReq,
  type InterceptResp,
  utf8,
} from "./pxb/index.js";
import type {
  BeforeAgentStartEvent,
  BeforeAgentStartResult,
  Command,
  ConfirmReply,
  ConfirmRequest,
  ExtensionContext,
  SessionBeforeSwitchEvent,
  SessionBeforeSwitchResult,
  Tool,
  ToolCallEvent,
  ToolCallResult,
  ToolResultEvent,
  ToolResultResult,
  TurnStoppingEvent,
  TurnStoppingResult,
  UserInputEvent,
  UserInputResult,
} from "./types.js";

export interface HelloInfo {
  cwd: string;
  sessionID: string;
  extensionDir: string;
  phiVersion: string;
}

export interface ExtensionOptions {
  stdin?: Readable;
  stdout?: Writable;
}

/**
 * Author-facing PXB extension (mirrors github.com/pulseaiclub/phi/ext/phi).
 */
export class Extension {
  readonly name: string;
  readonly version: string;

  private tools: Tool[] = [];
  private commands: { name: string; def: Command }[] = [];
  private events: number[] = [];
  private intercept: number[] = [];

  private onToolCallFn?: (ev: ToolCallEvent) => ToolCallResult | null | undefined;
  private onToolResultFn?: (ev: ToolResultEvent) => ToolResultResult | null | undefined;
  private onBeforeAgentStartFn?: (
    ev: BeforeAgentStartEvent,
  ) => BeforeAgentStartResult | null | undefined;
  private onSessionBeforeSwitchFn?: (
    ev: SessionBeforeSwitchEvent,
  ) => SessionBeforeSwitchResult | null | undefined;
  private onUserInputFn?: (ev: UserInputEvent) => UserInputResult | null | undefined;
  private onTurnStoppingFn?: (
    ev: TurnStoppingEvent,
  ) => TurnStoppingResult | null | undefined;
  private onEvent = new Map<number, (ev: EventNotify) => void>();

  private wr?: FrameWriter;
  private rd?: FrameReader;
  private host: HelloInfo = { cwd: "", sessionID: "", extensionDir: "", phiVersion: "" };
  private pendingSubmit = "";
  private nextHostID = 0;
  private readonly opts: ExtensionOptions;

  constructor(name: string, version: string, opts: ExtensionOptions = {}) {
    this.name = name;
    this.version = version;
    this.opts = opts;
  }

  hostInfo(): HelloInfo {
    return { ...this.host };
  }

  registerTool(t: Tool): void {
    if (!t.name || !t.execute) return;
    this.tools.push(t);
  }

  registerCommand(name: string, cmd: Command): void {
    if (!name || !cmd.handler) return;
    this.commands.push({ name, def: cmd });
  }

  onToolCall(fn: (ev: ToolCallEvent) => ToolCallResult | null | undefined): void {
    this.onToolCallFn = fn;
    this.intercept = appendUnique(this.intercept, EvToolCall);
  }

  onToolResult(fn: (ev: ToolResultEvent) => ToolResultResult | null | undefined): void {
    this.onToolResultFn = fn;
    this.intercept = appendUnique(this.intercept, EvToolResult);
  }

  onBeforeAgentStart(
    fn: (ev: BeforeAgentStartEvent) => BeforeAgentStartResult | null | undefined,
  ): void {
    this.onBeforeAgentStartFn = fn;
    this.intercept = appendUnique(this.intercept, EvBeforeAgentStart);
  }

  onSessionBeforeSwitch(
    fn: (ev: SessionBeforeSwitchEvent) => SessionBeforeSwitchResult | null | undefined,
  ): void {
    this.onSessionBeforeSwitchFn = fn;
    this.intercept = appendUnique(this.intercept, EvSessionBeforeSwitch);
  }

  onUserInput(fn: (ev: UserInputEvent) => UserInputResult | null | undefined): void {
    this.onUserInputFn = fn;
    this.intercept = appendUnique(this.intercept, EvUserInput);
  }

  onTurnStopping(
    fn: (ev: TurnStoppingEvent) => TurnStoppingResult | null | undefined,
  ): void {
    this.onTurnStoppingFn = fn;
    this.intercept = appendUnique(this.intercept, EvTurnStopping);
  }

  subscribe(event: string, fn?: () => void): void {
    this.subscribeEvent(event, () => {
      fn?.();
    });
  }

  subscribeEvent(event: string, fn?: (ev: EventNotify) => void): void {
    const code = eventCode(event);
    if (code === 0) return;
    this.events = appendUnique(this.events, code);
    if (fn) this.onEvent.set(code, fn);
  }

  notify(level: string, message: string): void {
    this.wr?.write(TypeNotify, 0, 0, encodeNotify({ level, message }));
  }

  setStatus(text: string): void {
    this.wr?.write(TypeNotify, 0, 0, encodeNotify({ status: text, statusSet: true }));
  }

  submit(text: string): void {
    this.pendingSubmit = text;
  }

  sendUserMessage(text: string): void {
    if (!this.wr || !text) return;
    this.wr.write(
      TypeHostRequest,
      0,
      0,
      encodeHostRequest({ method: "send_user_message", arg: text }),
    );
  }

  async confirm(title: string, message: string): Promise<boolean> {
    return (await this.confirmOpts({ title, message })).ok;
  }

  async confirmOpts(req: ConfirmRequest): Promise<ConfirmReply> {
    if (!this.wr || !this.rd) return { ok: false };
    const id = ++this.nextHostID;
    this.wr.write(
      TypeHostRequest,
      FlagHasID,
      id,
      encodeHostRequest({ method: "confirm", arg: JSON.stringify(req) }),
    );
    for (;;) {
      const fr = await this.rd.read();
      switch (fr.type) {
        case TypeHostResult: {
          if ((fr.flags & FlagHasID) === 0 || fr.id !== id) continue;
          const res = decodeHostResult(fr.body);
          return { ok: res.ok };
        }
        case TypeSessionMeta: {
          const meta = decodeSessionMeta(fr.body);
          if (meta.sessionID) this.host.sessionID = meta.sessionID;
          if (meta.cwd) this.host.cwd = meta.cwd;
          break;
        }
        case TypeEvent: {
          const ev = decodeEventNotify(fr.body);
          this.onEvent.get(ev.event)?.(ev);
          break;
        }
        case TypeShutdown:
          this.wr.write(TypeShutdownAck, 0, 0, new Uint8Array());
          return { ok: false };
        default:
          break;
      }
    }
  }

  /** Speak PXB on stdin/stdout until shutdown. */
  async run(): Promise<void> {
    const stdin = this.opts.stdin ?? defaultStdin;
    const stdout = this.opts.stdout ?? defaultStdout;
    this.wr = new FrameWriter(stdout);
    this.rd = new FrameReader(stdin);

    let caps = 0;
    if (this.commands.length > 0) caps |= CapCommands;
    if (this.tools.length > 0) caps |= CapTools;
    if (this.events.length > 0) caps |= CapEvents;
    if (this.intercept.length > 0) caps |= CapIntercept;

    this.wr.write(
      TypeHello,
      0,
      0,
      encodeHello({
        name: this.name,
        version: this.version,
        caps,
        protocol: ProtocolVersion,
      }),
    );

    const ackFrame = await this.rd.read();
    if (ackFrame.type !== TypeHelloAck) {
      throw new Error("sdk: expected hello_ack frame");
    }
    const ack = decodeHelloAck(ackFrame.body);
    this.host = {
      cwd: ack.cwd,
      sessionID: ack.sessionID,
      extensionDir: ack.extensionDir,
      phiVersion: ack.phiVersion,
    };

    for (const t of this.tools) {
      const schema = utf8(JSON.stringify(t.parameters ?? {}));
      this.wr.write(
        TypeRegisterTool,
        0,
        0,
        encodeRegisterTool({
          name: t.name,
          description: t.description,
          schemaJSON: schema,
        }),
      );
    }
    for (const c of this.commands) {
      this.wr.write(
        TypeRegisterCommand,
        0,
        0,
        encodeRegisterCommand({ name: c.name, description: c.def.description }),
      );
    }
    if (this.events.length > 0 || this.intercept.length > 0) {
      this.wr.write(
        TypeSubscribe,
        0,
        0,
        encodeSubscribe({ events: this.events, intercept: this.intercept }),
      );
    }
    this.wr.write(TypeReady, 0, 0, new Uint8Array());

    const toolByName = new Map(this.tools.map((t) => [t.name, t]));
    const cmdByName = new Map(this.commands.map((c) => [c.name, c.def]));

    for (;;) {
      const fr = await this.rd.read();
      switch (fr.type) {
        case TypeShutdown:
          this.wr.write(TypeShutdownAck, 0, 0, new Uint8Array());
          return;
        case TypeCommandInvoked: {
          const inv = decodeCommandInvoked(fr.body);
          const resp = { ok: true, error: "", notify: "", submit: "" };
          const cmd = cmdByName.get(inv.name);
          if (cmd) {
            try {
              const ctx: ExtensionContext = {
                cwd: this.host.cwd,
                sessionID: this.host.sessionID,
                hasUI: true,
              };
              await cmd.handler(inv.args, ctx);
            } catch (e) {
              resp.ok = false;
              resp.error = e instanceof Error ? e.message : String(e);
            }
          } else {
            resp.ok = false;
            resp.error = "unknown command";
          }
          resp.submit = this.pendingSubmit;
          this.pendingSubmit = "";
          this.wr.write(
            TypeCommandResponse,
            fr.flags,
            fr.id,
            encodeCommandResponse(resp),
          );
          break;
        }
        case TypeToolInvoke: {
          const inv = decodeToolInvoke(fr.body);
          const tr = {
            content: "",
            detail: "",
            output: "",
            isError: false,
            error: "",
          };
          const tool = toolByName.get(inv.name);
          if (tool) {
            try {
              const res = await tool.execute(inv.args);
              tr.content = res.content ?? "";
              tr.detail = res.detail ?? "";
              tr.output = res.output ?? "";
            } catch (e) {
              const msg = e instanceof Error ? e.message : String(e);
              tr.isError = true;
              tr.error = msg;
              tr.content = msg;
            }
          } else {
            tr.isError = true;
            tr.error = "unknown tool";
          }
          this.wr.write(TypeToolResult, fr.flags, fr.id, encodeToolResult(tr));
          break;
        }
        case TypeIntercept: {
          const req = decodeInterceptReq(fr.body);
          const resp = this.handleIntercept(req);
          this.wr.write(
            TypeInterceptResponse,
            fr.flags,
            fr.id,
            encodeInterceptResp(resp),
          );
          break;
        }
        case TypeEvent: {
          const ev = decodeEventNotify(fr.body);
          this.onEvent.get(ev.event)?.(ev);
          break;
        }
        case TypeSessionMeta: {
          const meta = decodeSessionMeta(fr.body);
          if (meta.sessionID) this.host.sessionID = meta.sessionID;
          if (meta.cwd) this.host.cwd = meta.cwd;
          break;
        }
        default:
          break;
      }
    }
  }

  private handleIntercept(req: InterceptReq): InterceptResp {
    switch (req.event) {
      case EvToolCall: {
        const r = this.onToolCallFn?.({
          toolName: req.toolName,
          toolCallID: req.toolCallID,
          input: req.input,
        });
        if (!r) return {};
        return { block: r.block, reason: r.reason, input: r.input, context: r.context };
      }
      case EvToolResult: {
        const r = this.onToolResultFn?.({
          toolName: req.toolName,
          toolCallID: req.toolCallID,
          input: req.input,
          content: req.content,
          isError: req.isError,
          err: req.errText,
        });
        if (!r) return {};
        return { content: r.content, context: r.context, stop: r.stop, reason: r.reason };
      }
      case EvBeforeAgentStart: {
        const r = this.onBeforeAgentStartFn?.({ prompt: req.prompt });
        if (!r) return {};
        return { systemPromptAppend: r.systemPromptAppend, prompt: r.prompt };
      }
      case EvSessionBeforeSwitch: {
        const r = this.onSessionBeforeSwitchFn?.({
          reason: req.reason,
          targetSessionID: req.targetID,
        });
        if (!r) return {};
        return { cancel: r.cancel, reason: r.reason, toast: r.toast };
      }
      case EvUserInput: {
        const r = this.onUserInputFn?.({ text: req.prompt });
        if (!r) return {};
        return { handled: r.handled, prompt: r.text, reason: r.reason };
      }
      case EvTurnStopping: {
        const r = this.onTurnStoppingFn?.({ turnIndex: req.turnIndex });
        if (!r) return {};
        return { continue: r.continue, prompt: r.message, reason: r.reason };
      }
      default:
        return {};
    }
  }
}

/** Alias matching the plan / docs example. */
export function createExtension(
  name: string,
  version: string,
  opts?: ExtensionOptions,
): Extension {
  return new Extension(name, version, opts);
}

function appendUnique(xs: number[], v: number): number[] {
  return xs.includes(v) ? xs : [...xs, v];
}
