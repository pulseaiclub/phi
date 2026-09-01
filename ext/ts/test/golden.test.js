import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";

import {
  CapCommands,
  CapTools,
  decodeHeader,
  decodeHello,
  decodeInterceptReq,
  decodeSubscribe,
  encodeFrame,
  encodeHello,
  encodeInterceptReq,
  EvToolCall,
} from "../dist/pxb/index.js";

const here = dirname(fileURLToPath(import.meta.url));
const goldenDir = join(here, "../../go/pxb/testdata");

test("decode Go golden hello.bin", () => {
  const raw = readFileSync(join(goldenDir, "hello.bin"));
  const h = decodeHello(raw);
  assert.equal(h.name, "greet");
  assert.equal(h.version, "1.0.0");
  assert.equal(h.caps, CapTools | CapCommands);
  assert.equal(h.protocol, 1);
});

test("decode Go golden intercept_req.bin", () => {
  const raw = readFileSync(join(goldenDir, "intercept_req.bin"));
  const ix = decodeInterceptReq(raw);
  assert.equal(ix.event, EvToolCall);
  assert.equal(ix.toolName, "bash");
  assert.equal(ix.toolCallID, "c1");
  assert.equal(Buffer.from(ix.input).toString(), '{"command":"ls"}');
});

test("decode Go golden subscribe.bin", () => {
  const raw = readFileSync(join(goldenDir, "subscribe.bin"));
  const s = decodeSubscribe(raw);
  assert.deepEqual(s.events, [5, 10]);
  assert.deepEqual(s.intercept, [1]);
});

test("decode Go golden hello_frame.bin", () => {
  const raw = readFileSync(join(goldenDir, "hello_frame.bin"));
  const hdr = decodeHeader(raw.subarray(0, 16));
  assert.equal(hdr.type, 1);
  assert.equal(hdr.payload, raw.length - 16);
  const h = decodeHello(raw.subarray(16));
  assert.equal(h.name, "greet");
});

test("TS encode matches Go golden hello payload", () => {
  const got = encodeHello({
    name: "greet",
    version: "1.0.0",
    caps: CapTools | CapCommands,
    protocol: 1,
  });
  const want = readFileSync(join(goldenDir, "hello.bin"));
  assert.deepEqual(Buffer.from(got), want);
});

test("TS encodeInterceptReq round-trips", () => {
  const body = encodeInterceptReq({
    event: EvToolCall,
    toolName: "bash",
    toolCallID: "c1",
    input: Buffer.from('{"command":"ls"}'),
    content: "",
    isError: false,
    errText: "",
    prompt: "",
    reason: "",
    targetID: "",
    turnIndex: 0,
  });
  const want = readFileSync(join(goldenDir, "intercept_req.bin"));
  assert.deepEqual(Buffer.from(body), want);
});

test("encodeFrame header+body", () => {
  const body = encodeHello({ name: "x", version: "1", caps: 0, protocol: 1 });
  const frame = encodeFrame(1, 0, 0, body);
  const hdr = decodeHeader(frame.subarray(0, 16));
  assert.equal(hdr.type, 1);
  assert.equal(hdr.payload, body.length);
});
