// Message roundtrip tests — mirrors the `all_messages_roundtrip` and
// `decode_skips_unknown_tags` tests in ext/rust/src/pxb/msg.rs.

import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { FieldWriter } from "../src/pxb/fields.ts";
import type {
  CommandInvoked,
  CommandResponse,
  EventNotify,
  Hello,
  HelloAck,
  HostRequest,
  HostResult,
  InterceptReq,
  InterceptResp,
  NotifyMsg,
  RegisterCommand,
  RegisterTool,
  SessionMeta,
  Subscribe,
  ToolDetailResult,
  ToolInvoke,
  ToolResultMsg,
} from "../src/pxb/msg.ts";
import {
  decodeCommandInvoked,
  decodeCommandResponse,
  decodeEventNotify,
  decodeHello,
  decodeHelloAck,
  decodeHostRequest,
  decodeHostResult,
  decodeInterceptReq,
  decodeInterceptResp,
  decodeNotifyMsg,
  decodeRegisterCommand,
  decodeRegisterTool,
  decodeSessionMeta,
  decodeSubscribe,
  decodeToolDetailResult,
  decodeToolInvoke,
  decodeToolResultMsg,
  encodeCommandInvoked,
  encodeCommandResponse,
  encodeEventNotify,
  encodeHello,
  encodeHelloAck,
  encodeHostRequest,
  encodeHostResult,
  encodeInterceptReq,
  encodeInterceptResp,
  encodeNotifyMsg,
  encodeRegisterCommand,
  encodeRegisterTool,
  encodeSessionMeta,
  encodeSubscribe,
  encodeToolDetailResult,
  encodeToolInvoke,
  encodeToolResultMsg,
} from "../src/pxb/msg.ts";

/** decode→encode must reproduce the exact input bytes (deterministic wire). */
function roundtrip<M>(encode: (m: M) => Buffer, decode: (b: Uint8Array) => M, m: M): void {
  const bytes = encode(m);
  assert.deepEqual(encode(decode(bytes)), bytes);
}

describe("messages", () => {
  it("roundtrip all messages deterministically", () => {
    roundtrip<Hello>(encodeHello, decodeHello, {
      name: "greet",
      version: "1.0.0",
      caps: 3,
      protocol: 1,
    });
    roundtrip<HelloAck>(encodeHelloAck, decodeHelloAck, {
      protocol: 1,
      phiVersion: "v0.19.0",
      cwd: "/tmp",
      sessionId: "s1",
      extensionDir: "/ext",
    });
    roundtrip<RegisterCommand>(encodeRegisterCommand, decodeRegisterCommand, {
      name: "hi",
      description: "Say hi",
      needsArgs: true,
    });
    roundtrip<RegisterTool>(encodeRegisterTool, decodeRegisterTool, {
      name: "t",
      description: "d",
      schemaJson: Buffer.from('{"type":"object"}'),
      timeoutSec: 120,
      hasDetail: true,
      readable: true,
    });
    roundtrip<ToolDetailResult>(encodeToolDetailResult, decodeToolDetailResult, {
      detail: "path/to/file",
    });
    roundtrip<Subscribe>(encodeSubscribe, decodeSubscribe, { events: [5, 10], intercept: [1, 2] });
    roundtrip<CommandInvoked>(encodeCommandInvoked, decodeCommandInvoked, {
      name: "hi",
      args: "a b",
    });
    roundtrip<CommandResponse>(encodeCommandResponse, decodeCommandResponse, {
      ok: false,
      error: "boom",
      notify: "",
      submit: "next",
    });
    roundtrip<ToolInvoke>(encodeToolInvoke, decodeToolInvoke, {
      name: "t",
      args: Buffer.from('{"k":1}'),
    });
    roundtrip<ToolResultMsg>(encodeToolResultMsg, decodeToolResultMsg, {
      content: "c",
      detail: "d",
      output: "o",
      isError: true,
      error: "e",
      expanded: true,
    });
    roundtrip<InterceptReq>(encodeInterceptReq, decodeInterceptReq, {
      event: 1,
      toolName: "bash",
      toolCallId: "c1",
      input: Buffer.from('{"command":"ls"}'),
      content: "out",
      isError: false,
      errText: "",
      prompt: "p",
      reason: "r",
      targetId: "t2",
      turnIndex: 3,
    });
    roundtrip<InterceptResp>(encodeInterceptResp, decodeInterceptResp, {
      block: true,
      stop: false,
      cancel: false,
      reason: "r",
      input: Buffer.from("in"),
      content: "c",
      context: "ctx",
      systemPromptAppend: "sys",
      toast: "t",
      handled: true,
      prompt: "p",
      continue: true,
    });
    roundtrip<EventNotify>(encodeEventNotify, decodeEventNotify, {
      event: 5,
      toolName: "t",
      toolCallId: "c",
      input: Buffer.from("i"),
      isError: true,
      prompt: "p",
      reason: "r",
      turnIndex: 2,
      sessionId: "s",
      previousSessionId: "ps",
      targetSessionId: "ts",
    });
    roundtrip<NotifyMsg>(encodeNotifyMsg, decodeNotifyMsg, {
      level: "info",
      message: "Hello",
      status: "st",
      statusSet: true,
    });
    roundtrip<HostRequest>(encodeHostRequest, decodeHostRequest, {
      method: "confirm",
      arg: '{"Title":"t"}',
    });
    roundtrip<HostResult>(encodeHostResult, decodeHostResult, { ok: true, error: "", body: "b" });
    roundtrip<SessionMeta>(encodeSessionMeta, decodeSessionMeta, { sessionId: "s2", cwd: "/x" });
  });

  it("omits opt zeros and empty values on the wire", () => {
    // Only tag 1 ("hi") survives: description is empty, needsArgs is opt-false.
    // Wire layout: tag u16 + kind u8 + len u32 + 2 bytes.
    assert.deepEqual(
      encodeRegisterCommand({ name: "hi", description: "", needsArgs: false }),
      Buffer.from([1, 0, 2, 2, 0, 0, 0, 0x68, 0x69]),
    );
  });

  it("decodes absent tags as typed zero values", () => {
    const h = decodeHello(Buffer.alloc(0));
    assert.deepEqual(h, { name: "", version: "", caps: 0, protocol: 0 });
    assert.equal(typeof h.caps, "number");
    const r = decodeCommandResponse(Buffer.alloc(0));
    assert.equal(r.ok, false);
    const s = decodeSubscribe(Buffer.alloc(0));
    assert.deepEqual(s, { events: [], intercept: [] });
  });

  it("decodes messages that skip unknown tags", () => {
    const w = new FieldWriter();
    w.putString(1, "name");
    w.putString(200, "future field"); // experimental, must be skippable
    w.putString(2, "1.0.0");
    const h = decodeHello(w.bytes());
    assert.equal(h.name, "name");
    assert.equal(h.version, "1.0.0");
  });

  it("rejects wrong wire kinds for known fields", () => {
    const w = new FieldWriter();
    w.putString(1, "greet"); // Hello.caps is u64 on tag 3, not bytes —
    w.putBytes(3, Buffer.from([1])); // a mismatched kind must fail, not misparse
    assert.throws(() => decodeHello(w.bytes()), (e: Error) => e.message.includes("bad wire kind"));
  });
});
