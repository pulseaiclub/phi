// End-to-end SDK tests: a fake PXB host drives the example extensions over
// piped stdin/stdout and asserts the full handshake + RPC lifecycle — a port
// of ext/rust/tests/sdk_test.rs. Byte-level fidelity to the Go host is pinned
// separately in golden.test.ts.

import assert from "node:assert/strict";
import { spawn, type ChildProcess } from "node:child_process";
import type { Writable } from "node:stream";
import { fileURLToPath } from "node:url";
import { describe, it } from "node:test";
import { ByteSource, readFrame, writeFrame, type Frame } from "../src/pxb/codec.ts";
import {
  decodeCommandResponse,
  decodeHello,
  decodeHostRequest,
  decodeNotifyMsg,
  decodeToolDetailResult,
  decodeToolResultMsg,
  encodeCommandInvoked,
  encodeEventNotify,
  encodeHelloAck,
  encodeHostResult,
  encodeInterceptReq,
  encodeInterceptResp,
  encodeSessionMeta,
  encodeToolInvoke,
  type Hello,
} from "../src/pxb/msg.ts";
import {
  CAP_COMMANDS,
  CAP_EVENTS,
  CAP_INTERCEPT,
  CAP_TOOLS,
  Event,
  FLAG_HAS_ID,
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
} from "../src/pxb/types.ts";

const READ_TIMEOUT_MS = 5_000;

/** Default InterceptReq (the hello extension registers no-op intercepts) and
 * the all-zero InterceptResp the SDK must answer with. */
const EMPTY_INTERCEPT_REQ = encodeInterceptReq({
  event: 0,
  toolName: "",
  toolCallId: "",
  input: new Uint8Array(0),
  content: "",
  isError: false,
  errText: "",
  prompt: "",
  reason: "",
  targetId: "",
  turnIndex: 0,
});
const EMPTY_INTERCEPT_RESP = encodeInterceptResp({
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
});

/** A minimal PXB host. Uses the SDK's own codec, so byte-level fidelity to
 * the Go host is pinned separately in golden.test.ts. */
class Host {
  readonly child: ChildProcess;
  private readonly src: ByteSource;
  private readonly wr: Writable;

  private constructor(child: ChildProcess) {
    this.child = child;
    this.src = new ByteSource(child.stdout!);
    this.wr = child.stdin!;
  }

  static spawn(script: string): Host {
    const path = fileURLToPath(new URL(script, import.meta.url));
    const child = spawn(process.execPath, [path], {
      stdio: ["pipe", "pipe", "inherit"],
    });
    return new Host(child);
  }

  read(): Promise<Frame> {
    return new Promise<Frame>((resolve, reject) => {
      const timer = setTimeout(
        () => reject(new Error("PXB response timeout")),
        READ_TIMEOUT_MS,
      );
      readFrame(this.src).then(
        (f) => {
          clearTimeout(timer);
          resolve(f);
        },
        (e: unknown) => {
          clearTimeout(timer);
          reject(e);
        },
      );
    });
  }

  /** Expects the next read to fail (EOF / closed pipe) within the timeout. */
  async expectClose(): Promise<void> {
    await assert.rejects(this.read(), (e: unknown) => e instanceof Error);
  }

  /** Expects no frame within a short window (ordering assertions). Checked
   * via the buffered byte count — an abandoned readFrame would keep its
   * bytes and poison the next real read. */
  async expectSilence(ms: number): Promise<void> {
    await new Promise((r) => setTimeout(r, ms));
    assert.equal(this.src.buffered, 0, "extension wrote a frame inside the silence window");
  }

  write(type: number, flags: number, id: number, body: Buffer): Promise<void> {
    return writeFrame(this.wr, type, flags, id, body);
  }

  /** Completes the handshake for any extension: read Hello, reply HelloAck,
   * then consume registration frames up to (and including) Ready. */
  async handshake(): Promise<Hello> {
    const f = await this.read();
    assert.equal(f.header.type, TYPE_HELLO);
    const hello = decodeHello(f.body);
    await this.write(
      TYPE_HELLO_ACK,
      0,
      0,
      encodeHelloAck({
        protocol: PROTOCOL_VERSION,
        phiVersion: "v0.0.0-test",
        cwd: "/tmp",
        sessionId: "s1",
        extensionDir: "/ext",
      }),
    );
    for (;;) {
      const reg = await this.read();
      switch (reg.header.type) {
        case TYPE_REGISTER_COMMAND:
        case TYPE_REGISTER_TOOL:
        case TYPE_SUBSCRIBE:
          break;
        case TYPE_READY:
          return hello;
        default:
          assert.fail(`unexpected frame during registration: ${reg.header.type}`);
      }
    }
  }

  async shutdown(): Promise<void> {
    await this.write(TYPE_SHUTDOWN, 0, 0, Buffer.alloc(0));
    const f = await this.read();
    assert.equal(f.header.type, TYPE_SHUTDOWN_ACK);
    const [code] = await once(this.child, "exit");
    assert.equal(code, 0, `extension exited with ${code}`);
  }

  kill(): void {
    this.child.kill();
  }
}

function once(child: ChildProcess, event: "exit"): Promise<[number | null]> {
  return new Promise((resolve) => {
    child.once(event, (code: number | null) => resolve([code]));
  });
}

const HELLO_ACK_BODY = encodeHelloAck({
  protocol: PROTOCOL_VERSION,
  phiVersion: "v0.0.0-test",
  cwd: "/tmp",
  sessionId: "s1",
  extensionDir: "/ext",
});

describe("sdk e2e (fake host)", () => {
  it("hello extension lifecycle", async () => {
    const h = Host.spawn("../examples/hello.ts");
    try {
      const hello = await h.handshake();
      assert.equal(hello.name, "hello");
      assert.equal(hello.version, "0.1.0");
      assert.equal(hello.protocol, PROTOCOL_VERSION);
      assert.equal(hello.caps, CAP_COMMANDS | CAP_INTERCEPT | CAP_EVENTS);

      // Command invoke → Notify frame, then CommandResponse echoing id.
      await h.write(
        TYPE_COMMAND_INVOKED,
        FLAG_HAS_ID,
        1,
        encodeCommandInvoked({ name: "hello", args: "world" }),
      );
      let f = await h.read();
      assert.equal(f.header.type, TYPE_NOTIFY);
      const n = decodeNotifyMsg(f.body);
      assert.equal(n.level, "info");
      assert.equal(n.message, "Hello!");

      f = await h.read();
      assert.equal(f.header.type, TYPE_COMMAND_RESPONSE);
      assert.equal(f.header.flags & FLAG_HAS_ID, FLAG_HAS_ID);
      assert.equal(f.header.id, 1);
      const resp = decodeCommandResponse(f.body);
      assert.equal(resp.ok, true);
      assert.equal(resp.error, "");
      assert.equal(resp.submit, "");

      // Intercept with no handler decision → empty response, id echoed.
      await h.write(
        TYPE_INTERCEPT,
        FLAG_HAS_ID,
        2,
        encodeInterceptReq({
          event: Event.UserInput,
          prompt: "hi",
          toolName: "",
          toolCallId: "",
          input: new Uint8Array(0),
          content: "",
          isError: false,
          errText: "",
          reason: "",
          targetId: "",
          turnIndex: 0,
        }),
      );
      f = await h.read();
      assert.equal(f.header.type, TYPE_INTERCEPT_RESPONSE);
      assert.equal(f.header.flags & FLAG_HAS_ID, FLAG_HAS_ID);
      assert.equal(f.header.id, 2);
      assert.deepEqual(f.body, EMPTY_INTERCEPT_RESP);

      // Fire-and-forget Event + SessionMeta must not break the loop.
      await h.write(
        TYPE_EVENT,
        0,
        0,
        encodeEventNotify({
          event: Event.SessionStart,
          sessionId: "s9",
          toolName: "",
          toolCallId: "",
          input: new Uint8Array(0),
          isError: false,
          prompt: "",
          reason: "",
          turnIndex: 0,
          previousSessionId: "",
          targetSessionId: "",
        }),
      );
      await h.write(
        TYPE_SESSION_META,
        0,
        0,
        encodeSessionMeta({ sessionId: "s9", cwd: "/new" }),
      );

      await h.shutdown();
    } catch (e) {
      h.kill();
      throw e;
    }
  });

  it("full extension confirm tool and submit", async () => {
    const h = Host.spawn("../examples/full.ts");
    try {
      const hello = await h.handshake();
      assert.equal(hello.name, "full");
      assert.equal(hello.caps, CAP_COMMANDS | CAP_TOOLS);

      // Command "ask" issues a confirm HostRequest (id 1), waits for the
      // HostResult, then notifies and replies with the queued submit.
      await h.write(
        TYPE_COMMAND_INVOKED,
        FLAG_HAS_ID,
        7,
        encodeCommandInvoked({ name: "ask", args: "" }),
      );

      let f = await h.read();
      assert.equal(f.header.type, TYPE_HOST_REQUEST);
      assert.equal(f.header.flags & FLAG_HAS_ID, FLAG_HAS_ID);
      assert.equal(f.header.id, 1);
      const hr = decodeHostRequest(f.body);
      assert.equal(hr.method, "confirm");
      assert.ok(hr.arg.includes('"Title":"Confirm?"'), hr.arg);
      assert.ok(hr.arg.includes('"Message":"Proceed with /tmp/x?"'), hr.arg);

      await h.write(
        TYPE_HOST_RESULT,
        FLAG_HAS_ID,
        1,
        encodeHostResult({ ok: true, error: "", body: "" }),
      );

      f = await h.read();
      assert.equal(f.header.type, TYPE_NOTIFY);
      const n = decodeNotifyMsg(f.body);
      assert.equal(n.level, "info");
      assert.equal(n.message, "Confirmed!");

      f = await h.read();
      assert.equal(f.header.type, TYPE_COMMAND_RESPONSE);
      assert.equal(f.header.id, 7);
      const resp = decodeCommandResponse(f.body);
      assert.equal(resp.ok, true);
      assert.equal(resp.submit, "follow-up from ask");

      // Tool invoke echoes its own id.
      await h.write(
        TYPE_TOOL_INVOKE,
        FLAG_HAS_ID,
        9,
        encodeToolInvoke({ name: "echo", args: Buffer.from('{"text":"hi"}') }),
      );
      f = await h.read();
      assert.equal(f.header.type, TYPE_TOOL_RESULT);
      assert.equal(f.header.id, 9);
      let tr = decodeToolResultMsg(f.body);
      assert.equal(tr.isError, false);
      assert.equal(tr.content, 'echo: {"text":"hi"}');
      assert.equal(tr.error, "");

      // DetailFromArgs RPC (same invoke body, lighter reply).
      await h.write(
        TYPE_TOOL_DETAIL_INVOKE,
        FLAG_HAS_ID,
        10,
        encodeToolInvoke({ name: "echo", args: Buffer.from('{"text":"hi"}') }),
      );
      f = await h.read();
      assert.equal(f.header.type, TYPE_TOOL_DETAIL_RESULT);
      assert.equal(f.header.id, 10);
      const detail = decodeToolDetailResult(f.body);
      assert.equal(detail.detail, '{"text":"hi"}');

      // Async tool handler: the SDK awaits the returned promise.
      await h.write(
        TYPE_TOOL_INVOKE,
        FLAG_HAS_ID,
        11,
        encodeToolInvoke({ name: "async-echo", args: Buffer.from('{"text":"yo"}') }),
      );
      f = await h.read();
      assert.equal(f.header.type, TYPE_TOOL_RESULT);
      assert.equal(f.header.id, 11);
      tr = decodeToolResultMsg(f.body);
      assert.equal(tr.isError, false);
      assert.equal(tr.content, 'async echo: {"text":"yo"}');
      assert.equal(tr.error, "");

      await h.shutdown();
    } catch (e) {
      h.kill();
      throw e;
    }
  });

  it("confirm replays deferred requests in order after the command", async () => {
    const h = Host.spawn("../examples/full.ts");
    try {
      await h.handshake();
      await h.write(
        TYPE_COMMAND_INVOKED,
        FLAG_HAS_ID,
        10,
        encodeCommandInvoked({ name: "ask", args: "" }),
      );
      const confirm = await h.read();
      assert.equal(confirm.header.type, TYPE_HOST_REQUEST);
      for (const [type, id] of [
        [TYPE_TOOL_INVOKE, 11],
        [TYPE_TOOL_DETAIL_INVOKE, 12],
      ] as const) {
        await h.write(
          type,
          FLAG_HAS_ID,
          id,
          encodeToolInvoke({ name: "echo", args: Buffer.from("queued") }),
        );
      }
      await h.write(TYPE_INTERCEPT, FLAG_HAS_ID, 13, EMPTY_INTERCEPT_REQ);
      await h.write(
        TYPE_COMMAND_INVOKED,
        FLAG_HAS_ID,
        14,
        encodeCommandInvoked({ name: "missing", args: "" }),
      );
      await h.write(
        TYPE_HOST_RESULT,
        FLAG_HAS_ID,
        confirm.header.id + 100,
        encodeHostResult({ ok: true, error: "", body: "" }),
      );
      await h.expectSilence(50);
      await h.write(
        TYPE_HOST_RESULT,
        FLAG_HAS_ID,
        confirm.header.id,
        encodeHostResult({ ok: true, error: "", body: "" }),
      );
      assert.equal((await h.read()).header.type, TYPE_NOTIFY);
      for (const [type, id] of [
        [TYPE_COMMAND_RESPONSE, 10],
        [TYPE_TOOL_RESULT, 11],
        [TYPE_TOOL_DETAIL_RESULT, 12],
        [TYPE_INTERCEPT_RESPONSE, 13],
        [TYPE_COMMAND_RESPONSE, 14],
      ] as const) {
        const f = await h.read();
        assert.equal(f.header.type, type);
        assert.equal(f.header.id, id);
        assert.equal(f.header.flags, FLAG_HAS_ID);
      }
      await h.shutdown();
    } catch (e) {
      h.kill();
      throw e;
    }
  });

  it("async timer and tcp, then another rpc", async () => {
    const { createServer } = await import("node:net");
    const listener = createServer();
    const address = await new Promise<string>((resolve) => {
      listener.listen(0, "127.0.0.1", () => {
        resolve(`127.0.0.1:${(listener.address() as { port: number }).port}`);
      });
    });
    const h = Host.spawn("../test/fixtures/probe.ts");
    try {
      await h.handshake();
      await h.write(
        TYPE_TOOL_INVOKE,
        FLAG_HAS_ID,
        41,
        encodeToolInvoke({ name: "io", args: Buffer.from(address) }),
      );
      const stream = await new Promise<import("node:net").Socket>((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error("loopback connection timeout")), 5_000);
        listener.once("connection", (s) => {
          clearTimeout(timer);
          resolve(s);
        });
        listener.once("error", reject);
      });
      stream.write(Buffer.from([42]));
      const f = await h.read();
      assert.equal(f.header.type, TYPE_TOOL_RESULT);
      assert.equal(f.header.id, 41);
      const result = decodeToolResultMsg(f.body);
      assert.equal(result.isError, false, result.error);
      assert.equal(result.content, "42");

      await h.write(
        TYPE_TOOL_DETAIL_INVOKE,
        FLAG_HAS_ID,
        42,
        encodeToolInvoke({ name: "io", args: Buffer.alloc(0) }),
      );
      const d = await h.read();
      assert.equal(d.header.type, TYPE_TOOL_DETAIL_RESULT);
      assert.equal(d.header.id, 42);
      stream.destroy();
      listener.close();
      await h.shutdown();
    } catch (e) {
      listener.close();
      h.kill();
      throw e;
    }
  });

  it("shutdown during confirm exits without another read", async () => {
    const h = Host.spawn("../test/fixtures/probe.ts");
    try {
      await h.handshake();
      await h.write(
        TYPE_COMMAND_INVOKED,
        FLAG_HAS_ID,
        1,
        encodeCommandInvoked({ name: "ask", args: "" }),
      );
      const f = await h.read();
      assert.equal(f.header.type, TYPE_HOST_REQUEST);
      await h.write(TYPE_SHUTDOWN, 0, 0, Buffer.alloc(0));
      assert.equal((await h.read()).header.type, TYPE_SHUTDOWN_ACK);
      await h.expectClose();
      const [code] = await once(h.child, "exit");
      assert.equal(code, 0);
    } catch (e) {
      h.kill();
      throw e;
    }
  });

  it("oversized tool response is an rpc error and the loop survives", async () => {
    const h = Host.spawn("../test/fixtures/probe.ts");
    try {
      await h.handshake();
      await h.write(
        TYPE_TOOL_INVOKE,
        FLAG_HAS_ID,
        9,
        encodeToolInvoke({ name: "large", args: Buffer.alloc(0) }),
      );
      const f = await h.read();
      assert.equal(f.header.type, TYPE_TOOL_RESULT);
      assert.equal(f.header.id, 9);
      const result = decodeToolResultMsg(f.body);
      assert.equal(result.isError, true);
      assert.ok(result.error.includes("exceeds PXB payload limit"), result.error);
      await h.shutdown();
    } catch (e) {
      h.kill();
      throw e;
    }
  });

  it("shutdown during a tool call acks after the result, not before", async () => {
    const h = Host.spawn("../test/fixtures/probe.ts");
    try {
      await h.handshake();
      await h.write(
        TYPE_TOOL_INVOKE,
        FLAG_HAS_ID,
        5,
        encodeToolInvoke({ name: "slow", args: Buffer.alloc(0) }),
      );
      await h.write(TYPE_SHUTDOWN, 0, 0, Buffer.alloc(0));
      // The in-flight reply must beat the ack — the host kills on ack.
      const f = await h.read();
      assert.equal(f.header.type, TYPE_TOOL_RESULT);
      assert.equal(f.header.id, 5);
      assert.equal(decodeToolResultMsg(f.body).content, "done");
      const ack = await h.read();
      assert.equal(ack.header.type, TYPE_SHUTDOWN_ACK);
      const [code] = await once(h.child, "exit");
      assert.equal(code, 0);
    } catch (e) {
      h.kill();
      throw e;
    }
  });

  it("deferred queue overflow fails the run like the rust sdk", async () => {
    const h = Host.spawn("../examples/full.ts");
    try {
      await h.handshake();
      await h.write(
        TYPE_COMMAND_INVOKED,
        FLAG_HAS_ID,
        1,
        encodeCommandInvoked({ name: "ask", args: "" }),
      );
      const confirm = await h.read();
      assert.equal(confirm.header.type, TYPE_HOST_REQUEST);
      // Flood past the deferred-frame bound while the confirm waits.
      for (let id = 2; id <= 34; id++) {
        await h.write(
          TYPE_INTERCEPT,
          FLAG_HAS_ID,
          id,
          EMPTY_INTERCEPT_REQ,
        );
      }
      await h.write(
        TYPE_HOST_RESULT,
        FLAG_HAS_ID,
        confirm.header.id,
        encodeHostResult({ ok: true, error: "", body: "" }),
      );
      // The waiter wakes with a default reply; the handler still emits its
      // "Declined." notify (Rust does too), then the run fails: no command
      // response, EOF, exit 1.
      const f = await h.read();
      assert.equal(f.header.type, TYPE_NOTIFY);
      await h.expectClose();
      const [code] = await once(h.child, "exit");
      assert.equal(code, 1);
    } catch (e) {
      h.kill();
      throw e;
    }
  });
});
