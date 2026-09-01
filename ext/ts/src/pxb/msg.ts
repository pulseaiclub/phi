import {
  decodeU16s,
  emptyBytes,
  encodeFields,
  takeBytes,
  takeString,
  takeU64,
  textEncoder,
  walk,
} from "./fields.js";

const f = {
  helloName: 1,
  helloVersion: 2,
  helloCaps: 3,
  helloProtocol: 4,
  ackProtocol: 1,
  ackPhiVersion: 2,
  ackCwd: 3,
  ackSessionID: 4,
  ackExtDir: 5,
  regCmdName: 1,
  regCmdDesc: 2,
  regToolName: 1,
  regToolDesc: 2,
  regToolSchema: 3,
  subEvents: 1,
  subIntercept: 2,
  cmdInvName: 1,
  cmdInvArgs: 2,
  cmdResOK: 1,
  cmdResError: 2,
  cmdResNotify: 3,
  cmdResSubmit: 4,
  toolInvName: 1,
  toolInvArgs: 2,
  toolResContent: 1,
  toolResDetail: 2,
  toolResOutput: 3,
  toolResIsError: 4,
  toolResError: 5,
  ixReqEvent: 1,
  ixReqToolName: 2,
  ixReqToolCallID: 3,
  ixReqInput: 4,
  ixReqContent: 5,
  ixReqIsError: 6,
  ixReqErrText: 7,
  ixReqPrompt: 8,
  ixReqReason: 9,
  ixReqTargetID: 10,
  ixReqTurnIndex: 11,
  ixResBlock: 1,
  ixResStop: 2,
  ixResCancel: 3,
  ixResReason: 4,
  ixResInput: 5,
  ixResContent: 6,
  ixResContext: 7,
  ixResSysAppend: 8,
  ixResToast: 9,
  ixResHandled: 10,
  ixResPrompt: 11,
  ixResContinue: 12,
  evEvent: 1,
  evToolName: 2,
  evToolCallID: 3,
  evInput: 4,
  evIsError: 5,
  evPrompt: 6,
  evReason: 7,
  evTurnIndex: 8,
  evSessionID: 9,
  evPreviousSessionID: 10,
  evTargetSessionID: 11,
  notifyLevel: 1,
  notifyMessage: 2,
  notifyStatus: 3,
  notifyStatusSet: 4,
  hostReqMethod: 1,
  hostReqArg: 2,
  hostResOK: 1,
  hostResError: 2,
  hostResBody: 3,
  metaSessionID: 1,
  metaCwd: 2,
} as const;

export interface Hello {
  name: string;
  version: string;
  caps: number;
  protocol: number;
}

export function encodeHello(h: Hello): Uint8Array {
  return encodeFields((fw) => {
    fw.putString(f.helloName, h.name);
    fw.putString(f.helloVersion, h.version);
    fw.putU32(f.helloCaps, h.caps);
    fw.putU16(f.helloProtocol, h.protocol);
  });
}

export function decodeHello(b: Uint8Array): Hello {
  const h: Hello = { name: "", version: "", caps: 0, protocol: 0 };
  walk(b, (tag, kind, fr) => {
    switch (tag) {
      case f.helloName:
        h.name = takeString(kind, fr);
        break;
      case f.helloVersion:
        h.version = takeString(kind, fr);
        break;
      case f.helloCaps:
        h.caps = takeU64(kind, fr);
        break;
      case f.helloProtocol:
        h.protocol = takeU64(kind, fr);
        break;
      default:
        fr.skip(kind);
    }
  });
  return h;
}

export interface HelloAck {
  protocol: number;
  phiVersion: string;
  cwd: string;
  sessionID: string;
  extensionDir: string;
}

export function encodeHelloAck(h: HelloAck): Uint8Array {
  return encodeFields((fw) => {
    fw.putU16(f.ackProtocol, h.protocol);
    fw.putString(f.ackPhiVersion, h.phiVersion);
    fw.putString(f.ackCwd, h.cwd);
    fw.putString(f.ackSessionID, h.sessionID);
    fw.putString(f.ackExtDir, h.extensionDir);
  });
}

export function decodeHelloAck(b: Uint8Array): HelloAck {
  const h: HelloAck = {
    protocol: 0,
    phiVersion: "",
    cwd: "",
    sessionID: "",
    extensionDir: "",
  };
  walk(b, (tag, kind, fr) => {
    switch (tag) {
      case f.ackProtocol:
        h.protocol = takeU64(kind, fr);
        break;
      case f.ackPhiVersion:
        h.phiVersion = takeString(kind, fr);
        break;
      case f.ackCwd:
        h.cwd = takeString(kind, fr);
        break;
      case f.ackSessionID:
        h.sessionID = takeString(kind, fr);
        break;
      case f.ackExtDir:
        h.extensionDir = takeString(kind, fr);
        break;
      default:
        fr.skip(kind);
    }
  });
  return h;
}

export interface RegisterCommand {
  name: string;
  description: string;
}

export function encodeRegisterCommand(r: RegisterCommand): Uint8Array {
  return encodeFields((fw) => {
    fw.putString(f.regCmdName, r.name);
    fw.putString(f.regCmdDesc, r.description);
  });
}

export interface RegisterTool {
  name: string;
  description: string;
  schemaJSON: Uint8Array;
}

export function encodeRegisterTool(r: RegisterTool): Uint8Array {
  return encodeFields((fw) => {
    fw.putString(f.regToolName, r.name);
    fw.putString(f.regToolDesc, r.description);
    fw.putBytes(f.regToolSchema, r.schemaJSON);
  });
}

export interface Subscribe {
  events: number[];
  intercept: number[];
}

export function encodeSubscribe(s: Subscribe): Uint8Array {
  return encodeFields((fw) => {
    fw.putU16s(f.subEvents, s.events);
    fw.putU16s(f.subIntercept, s.intercept);
  });
}

export function decodeSubscribe(b: Uint8Array): Subscribe {
  const s: Subscribe = { events: [], intercept: [] };
  walk(b, (tag, kind, fr) => {
    switch (tag) {
      case f.subEvents:
        s.events = decodeU16s(takeBytes(kind, fr));
        break;
      case f.subIntercept:
        s.intercept = decodeU16s(takeBytes(kind, fr));
        break;
      default:
        fr.skip(kind);
    }
  });
  return s;
}

export interface CommandInvoked {
  name: string;
  args: string;
}

export function decodeCommandInvoked(b: Uint8Array): CommandInvoked {
  const c: CommandInvoked = { name: "", args: "" };
  walk(b, (tag, kind, fr) => {
    switch (tag) {
      case f.cmdInvName:
        c.name = takeString(kind, fr);
        break;
      case f.cmdInvArgs:
        c.args = takeString(kind, fr);
        break;
      default:
        fr.skip(kind);
    }
  });
  return c;
}

export interface CommandResponse {
  ok: boolean;
  error: string;
  notify: string;
  submit: string;
}

export function encodeCommandResponse(c: CommandResponse): Uint8Array {
  return encodeFields((fw) => {
    fw.putBool(f.cmdResOK, c.ok);
    fw.putString(f.cmdResError, c.error);
    fw.putString(f.cmdResNotify, c.notify);
    fw.putString(f.cmdResSubmit, c.submit);
  });
}

export interface ToolInvoke {
  name: string;
  args: Uint8Array;
}

export function decodeToolInvoke(b: Uint8Array): ToolInvoke {
  const t: ToolInvoke = { name: "", args: new Uint8Array() };
  walk(b, (tag, kind, fr) => {
    switch (tag) {
      case f.toolInvName:
        t.name = takeString(kind, fr);
        break;
      case f.toolInvArgs: {
        const p = takeBytes(kind, fr);
        t.args = p.slice();
        break;
      }
      default:
        fr.skip(kind);
    }
  });
  return t;
}

export interface ToolResultMsg {
  content: string;
  detail: string;
  output: string;
  isError: boolean;
  error: string;
}

export function encodeToolResult(t: ToolResultMsg): Uint8Array {
  return encodeFields((fw) => {
    fw.putString(f.toolResContent, t.content);
    fw.putString(f.toolResDetail, t.detail);
    fw.putString(f.toolResOutput, t.output);
    fw.putBool(f.toolResIsError, t.isError);
    fw.putString(f.toolResError, t.error);
  });
}

export interface InterceptReq {
  event: number;
  toolName: string;
  toolCallID: string;
  input: Uint8Array;
  content: string;
  isError: boolean;
  errText: string;
  prompt: string;
  reason: string;
  targetID: string;
  turnIndex: number;
}

export function encodeInterceptReq(r: InterceptReq): Uint8Array {
  return encodeFields((fw) => {
    fw.putU16(f.ixReqEvent, r.event);
    fw.putString(f.ixReqToolName, r.toolName);
    fw.putString(f.ixReqToolCallID, r.toolCallID);
    fw.putBytes(f.ixReqInput, r.input);
    fw.putString(f.ixReqContent, r.content);
    fw.putBool(f.ixReqIsError, r.isError);
    fw.putString(f.ixReqErrText, r.errText);
    fw.putString(f.ixReqPrompt, r.prompt);
    fw.putString(f.ixReqReason, r.reason);
    fw.putString(f.ixReqTargetID, r.targetID);
    fw.putU32(f.ixReqTurnIndex, r.turnIndex);
  });
}

export function decodeInterceptReq(b: Uint8Array): InterceptReq {
  const r: InterceptReq = {
    event: 0,
    toolName: "",
    toolCallID: "",
    input: new Uint8Array(),
    content: "",
    isError: false,
    errText: "",
    prompt: "",
    reason: "",
    targetID: "",
    turnIndex: 0,
  };
  walk(b, (tag, kind, fr) => {
    switch (tag) {
      case f.ixReqEvent:
        r.event = takeU64(kind, fr);
        break;
      case f.ixReqToolName:
        r.toolName = takeString(kind, fr);
        break;
      case f.ixReqToolCallID:
        r.toolCallID = takeString(kind, fr);
        break;
      case f.ixReqInput:
        r.input = takeBytes(kind, fr).slice();
        break;
      case f.ixReqContent:
        r.content = takeString(kind, fr);
        break;
      case f.ixReqIsError:
        r.isError = takeU64(kind, fr) !== 0;
        break;
      case f.ixReqErrText:
        r.errText = takeString(kind, fr);
        break;
      case f.ixReqPrompt:
        r.prompt = takeString(kind, fr);
        break;
      case f.ixReqReason:
        r.reason = takeString(kind, fr);
        break;
      case f.ixReqTargetID:
        r.targetID = takeString(kind, fr);
        break;
      case f.ixReqTurnIndex:
        r.turnIndex = takeU64(kind, fr);
        break;
      default:
        fr.skip(kind);
    }
  });
  return r;
}

export interface InterceptResp {
  block?: boolean;
  stop?: boolean;
  cancel?: boolean;
  handled?: boolean;
  continue?: boolean;
  reason?: string;
  input?: Uint8Array;
  content?: string;
  context?: string;
  systemPromptAppend?: string;
  toast?: string;
  prompt?: string;
}

export function encodeInterceptResp(r: InterceptResp): Uint8Array {
  return encodeFields((fw) => {
    fw.putBool(f.ixResBlock, !!r.block);
    fw.putBool(f.ixResStop, !!r.stop);
    fw.putBool(f.ixResCancel, !!r.cancel);
    fw.putString(f.ixResReason, r.reason ?? "");
    fw.putBytes(f.ixResInput, r.input ?? emptyBytes);
    fw.putString(f.ixResContent, r.content ?? "");
    fw.putString(f.ixResContext, r.context ?? "");
    fw.putString(f.ixResSysAppend, r.systemPromptAppend ?? "");
    fw.putString(f.ixResToast, r.toast ?? "");
    fw.putBool(f.ixResHandled, !!r.handled);
    fw.putString(f.ixResPrompt, r.prompt ?? "");
    fw.putBool(f.ixResContinue, !!r.continue);
  });
}

export interface EventNotify {
  event: number;
  toolName: string;
  toolCallID: string;
  input: Uint8Array;
  isError: boolean;
  prompt: string;
  reason: string;
  turnIndex: number;
  sessionID: string;
  previousSessionID: string;
  targetSessionID: string;
}

export function decodeEventNotify(b: Uint8Array): EventNotify {
  const e: EventNotify = {
    event: 0,
    toolName: "",
    toolCallID: "",
    input: new Uint8Array(),
    isError: false,
    prompt: "",
    reason: "",
    turnIndex: 0,
    sessionID: "",
    previousSessionID: "",
    targetSessionID: "",
  };
  walk(b, (tag, kind, fr) => {
    switch (tag) {
      case f.evEvent:
        e.event = takeU64(kind, fr);
        break;
      case f.evToolName:
        e.toolName = takeString(kind, fr);
        break;
      case f.evToolCallID:
        e.toolCallID = takeString(kind, fr);
        break;
      case f.evInput:
        e.input = takeBytes(kind, fr).slice();
        break;
      case f.evIsError:
        e.isError = takeU64(kind, fr) !== 0;
        break;
      case f.evPrompt:
        e.prompt = takeString(kind, fr);
        break;
      case f.evReason:
        e.reason = takeString(kind, fr);
        break;
      case f.evTurnIndex:
        e.turnIndex = takeU64(kind, fr);
        break;
      case f.evSessionID:
        e.sessionID = takeString(kind, fr);
        break;
      case f.evPreviousSessionID:
        e.previousSessionID = takeString(kind, fr);
        break;
      case f.evTargetSessionID:
        e.targetSessionID = takeString(kind, fr);
        break;
      default:
        fr.skip(kind);
    }
  });
  return e;
}

export interface NotifyMsg {
  level?: string;
  message?: string;
  status?: string;
  statusSet?: boolean;
}

export function encodeNotify(n: NotifyMsg): Uint8Array {
  return encodeFields((fw) => {
    fw.putString(f.notifyLevel, n.level ?? "");
    fw.putString(f.notifyMessage, n.message ?? "");
    fw.putString(f.notifyStatus, n.status ?? "");
    fw.putBool(f.notifyStatusSet, !!n.statusSet);
  });
}

export interface HostRequest {
  method: string;
  arg: string;
}

export function encodeHostRequest(r: HostRequest): Uint8Array {
  return encodeFields((fw) => {
    fw.putString(f.hostReqMethod, r.method);
    fw.putString(f.hostReqArg, r.arg);
  });
}

export interface HostResult {
  ok: boolean;
  error: string;
  body: string;
}

export function decodeHostResult(b: Uint8Array): HostResult {
  const r: HostResult = { ok: false, error: "", body: "" };
  walk(b, (tag, kind, fr) => {
    switch (tag) {
      case f.hostResOK:
        r.ok = takeU64(kind, fr) !== 0;
        break;
      case f.hostResError:
        r.error = takeString(kind, fr);
        break;
      case f.hostResBody:
        r.body = takeString(kind, fr);
        break;
      default:
        fr.skip(kind);
    }
  });
  return r;
}

export interface SessionMeta {
  sessionID: string;
  cwd: string;
}

export function decodeSessionMeta(b: Uint8Array): SessionMeta {
  const m: SessionMeta = { sessionID: "", cwd: "" };
  walk(b, (tag, kind, fr) => {
    switch (tag) {
      case f.metaSessionID:
        m.sessionID = takeString(kind, fr);
        break;
      case f.metaCwd:
        m.cwd = takeString(kind, fr);
        break;
      default:
        fr.skip(kind);
    }
  });
  return m;
}

export function utf8(s: string): Uint8Array {
  return textEncoder.encode(s);
}
