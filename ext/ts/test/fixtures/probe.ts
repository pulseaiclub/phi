// SDK test fixture: async IO, an oversized tool result, and a command that
// outlives shutdown — port of ext/rust/tests/fixtures/probe.rs.

import net from "node:net";
import { MAX_PAYLOAD } from "../../src/pxb/types.ts";
import { Extension, Schema } from "../../src/index.ts";

const decoder = new TextDecoder();

const ext = new Extension("sdk-probe", "0.1.0");

ext.registerTool({
  name: "io",
  description: "Exercise runtime drivers",
  schema: Schema.object(),
  execute: async (args) => {
    await new Promise((r) => setTimeout(r, 10));
    // Rust's TcpStream::connect accepts "host:port"; Node's net.connect
    // would treat that string as an IPC path, so split it.
    const [host, port] = decoder.decode(args).split(":");
    return await new Promise((resolve, reject) => {
      const stream = net.connect(Number(port), host, () => {
        stream.once("data", (chunk: Buffer) => {
          stream.destroy();
          resolve({ content: String(chunk[0]) });
        });
      });
      stream.on("error", (e: Error) => {
        reject(new Error(`expected loopback byte: ${e.message}`));
      });
    });
  },
});

ext.registerTool({
  name: "large",
  description: "Large result",
  schema: Schema.object(),
  execute: () => ({ content: "x".repeat(MAX_PAYLOAD) }),
});

ext.registerTool({
  name: "slow",
  description: "Slow result",
  schema: Schema.object(),
  execute: async () => {
    // Long enough that a shutdown sent right after the invoke lands while
    // this handler is still running.
    await new Promise((r) => setTimeout(r, 150));
    return { content: "done" };
  },
});

ext.registerCommand("ask", {
  description: "Confirm",
  handler: async (_args, ctx) => {
    await ctx.confirm("Proceed?", "Test");
    // A second confirmation must not read again after shutdown.
    await ctx.confirm("Again?", "Test");
  },
});

await ext.run();
