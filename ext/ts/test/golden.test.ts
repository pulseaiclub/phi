// Golden byte-compat tests against the Go SDK's fixtures in
// ext/go/pxb/testdata/*.bin — the same fixtures pin the Rust port
// (ext/rust/tests/pxb_test.rs). Regenerate with
// `UPDATE_GOLDEN=1 go test ./ext/go/pxb -run TestWriteGoldenFixtures`.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { describe, it } from "node:test";
import { decodeHeader } from "../src/pxb/codec.ts";
import {
  decodeHello,
  decodeHelloAck,
  decodeInterceptReq,
  decodeModelInvoke,
  decodeModelStreamEvent,
  decodeRegisterModel,
  decodeSubscribe,
  encodeHello,
  encodeHelloAck,
  encodeInterceptReq,
  encodeModelInvoke,
  encodeModelStreamEvent,
  encodeRegisterModel,
  encodeSubscribe,
} from "../src/pxb/msg.ts";
import {
  CAP_COMMANDS,
  CAP_TOOLS,
  CONN_EXT,
  Event,
  TYPE_HELLO,
} from "../src/pxb/types.ts";

const TESTDATA = new URL("../../go/pxb/testdata/", import.meta.url);

function golden(name: string): Buffer {
  return readFileSync(new URL(name, TESTDATA));
}

describe("golden fixtures (byte compat with ext/go)", () => {
  it("Hello encodes to hello.bin", () => {
    const bytes = encodeHello({
      name: "greet",
      version: "1.0.0",
      caps: CAP_TOOLS | CAP_COMMANDS,
      protocol: 1,
    });
    assert.deepEqual(bytes, golden("hello.bin"));
    const h = decodeHello(golden("hello.bin"));
    assert.equal(h.name, "greet");
    assert.equal(h.version, "1.0.0");
    assert.equal(h.caps, CAP_TOOLS | CAP_COMMANDS);
    assert.equal(h.protocol, 1);
  });

  it("HelloAck encodes to hello_ack.bin", () => {
    const bytes = encodeHelloAck({
      protocol: 1,
      phiVersion: "v0.19.0",
      cwd: "/tmp",
      sessionId: "s1",
      extensionDir: "/ext",
    });
    assert.deepEqual(bytes, golden("hello_ack.bin"));
    const ack = decodeHelloAck(golden("hello_ack.bin"));
    assert.equal(ack.phiVersion, "v0.19.0");
    assert.equal(ack.cwd, "/tmp");
  });

  it("InterceptReq encodes to intercept_req.bin", () => {
    const bytes = encodeInterceptReq({
      event: Event.ToolCall,
      toolName: "bash",
      toolCallId: "c1",
      input: Buffer.from('{"command":"ls"}'),
      content: "",
      isError: false,
      errText: "",
      prompt: "",
      reason: "",
      targetId: "",
      turnIndex: 0,
    });
    assert.deepEqual(bytes, golden("intercept_req.bin"));
    const ix = decodeInterceptReq(golden("intercept_req.bin"));
    assert.equal(ix.toolName, "bash");
    assert.deepEqual(ix.input, Buffer.from('{"command":"ls"}'));
  });

  it("Subscribe encodes to subscribe.bin", () => {
    const bytes = encodeSubscribe({
      events: [Event.SessionStart, Event.AgentEnd],
      intercept: [Event.ToolCall],
    });
    assert.deepEqual(bytes, golden("subscribe.bin"));
    const sub = decodeSubscribe(golden("subscribe.bin"));
    assert.deepEqual(sub.events, [Event.SessionStart, Event.AgentEnd]);
    assert.deepEqual(sub.intercept, [Event.ToolCall]);
  });

  it("hello_frame.bin is a valid Hello frame", () => {
    const raw = golden("hello_frame.bin");
    const header = decodeHeader(raw);
    assert.equal(header.type, TYPE_HELLO);
    assert.equal(header.flags, 0);
    assert.equal(header.id, 0);
    assert.equal(header.payload, raw.length - 16);
    assert.deepEqual(raw.subarray(16), encodeHello(decodeHello(raw.subarray(16))));
  });

  it("RegisterModel encodes to register_model.bin", () => {
    const m = decodeRegisterModel(golden("register_model.bin"));
    assert.equal(m.name, "echo");
    assert.equal(m.api, "Anthropic");
    assert.equal(m.baseUrl, "http://127.0.0.1:9");
    assert.equal(m.apiKey, "k");
    assert.equal(m.preset, "glm-5.3-flash");
    assert.equal(m.contextWindow, 1000);
    assert.equal(m.imageEnabled, true);
    assert.equal(m.thinkEnabled, true);
    assert.equal(m.thinkLevel, "high");
    assert.equal(m.conn, CONN_EXT);
    assert.deepEqual(encodeRegisterModel(m), golden("register_model.bin"));
  });

  it("ModelInvoke encodes to model_invoke.bin", () => {
    const inv = decodeModelInvoke(golden("model_invoke.bin"));
    assert.equal(inv.model, "echo");
    assert.deepEqual(inv.messagesJson, Buffer.from('[{"role":"user","content":"hi"}]'));
    assert.deepEqual(inv.toolsJson, Buffer.from('[{"name":"t"}]'));
    assert.equal(inv.systemPrompt, "sys");
    assert.equal(inv.think, "high");
    assert.deepEqual(encodeModelInvoke(inv), golden("model_invoke.bin"));
  });

  it("ModelStreamEvent encodes to model_stream.bin", () => {
    const ev = decodeModelStreamEvent(golden("model_stream.bin"));
    assert.equal(ev.type, "delta");
    assert.equal(ev.content, "c");
    assert.equal(ev.reasoning, "r");
    assert.deepEqual(ev.toolCalls, Buffer.from('[{"index":0,"id":"c1"}]'));
    assert.deepEqual(ev.usageJson, Buffer.from('{"total_tokens":3}'));
    assert.equal(ev.error, "");
    assert.deepEqual(encodeModelStreamEvent(ev), golden("model_stream.bin"));
  });
});
