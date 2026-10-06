// Minimal hello extension, mirroring the Go example in `doc/extensions.md`.

import { Event, Extension } from "../src/index.ts";

const m = new Extension("hello", "0.1.0");

m.registerCommand("hello", {
  description: "Say hi",
  handler: async (_args, ctx) => {
    await ctx.notify("info", "Hello!");
    // await ctx.sendUserMessage("…"); // enqueue a turn anytime
    // ctx.submit("follow-up");        // after /hello returns
  },
});

// .needsArgs → picker/bare "/plan" fills "/plan " for the user to finish
m.registerCommand("plan", {
  description: "plan mode — /plan on|off|status",
  needsArgs: true,
  handler: async () => {},
});

m.onUserInput((_ev) => {
  // return { handled: true } to swallow
  // return { text: "rewritten" } to transform
  return null;
});

m.onToolCall((_ev) => {
  // return { block: true, reason: "…" } to deny
  return null;
});

m.onToolResult((_ev) => {
  // return { stop: true } to end the agent loop
  return null;
});

m.onTurnStopping((_ev) => {
  // return { continue: true, message: "check X" } to steer
  return null;
});

m.subscribe(Event.SessionStart, (_ev) => {
  // Reason, PreviousSessionID, …
});

await m.run();
