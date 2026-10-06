// Richer example: tools (sync + async), a confirm dialog, and a queued
// follow-up — port of ext/rust/examples/full.rs.

import { Extension, Schema, type ToolResult } from "../src/index.ts";

const decoder = new TextDecoder();

const m = new Extension("full", "0.1.0");

m.registerTool({
  name: "echo",
  description: "Echo the input back",
  schema: Schema.object().property("text", Schema.string()).required(["text"]),
  detailFromArgs: (args) => decoder.decode(args),
  execute: (args): ToolResult => ({ content: `echo: ${decoder.decode(args)}` }),
});

m.registerTool({
  name: "async-echo",
  description: "Echo the input back (async handler)",
  schema: Schema.object().property("text", Schema.string()).required(["text"]),
  timeoutSec: 10,
  execute: async (args) => {
    // Yield once: proves the run loop drives the promise rather than a
    // ready value.
    await Promise.resolve();
    return { content: `async echo: ${decoder.decode(args)}` };
  },
});

m.registerCommand("ask", {
  description: "Ask a yes/no question",
  handler: async (_args, ctx) => {
    const reply = await ctx.confirm("Confirm?", "Proceed with /tmp/x?");
    if (reply.ok) {
      await ctx.notify("info", "Confirmed!");
    } else {
      await ctx.notify("warning", "Declined.");
    }
    ctx.submit("follow-up from ask");
  },
});

await m.run();
