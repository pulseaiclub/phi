// Model registration example: a direct-connect gateway and an
// extension-served echo model — mirrors ext/rust/examples/models.rs.

import { CONN_EXT, Extension } from "../src/index.ts";

const m = new Extension("models", "0.1.0");

// Direct-connect: the host dials baseUrl itself, like a config.yaml entry.
m.registerModel({
  name: "gw",
  api: "Anthropic",
  baseUrl: "http://127.0.0.1:9",
  apiKey: "k",
  contextWindow: 42,
});

// Extension-served: every chat completion lands in the stream handler.
// Emit deltas as they arrive; return (or throw) to end the stream — the
// SDK sends the done / error terminal frame. Check signal.aborted (or pass
// the signal to fetch) when forwarding upstream; the host cancels on Esc.
m.registerModel({
  name: "echo",
  conn: CONN_EXT,
  contextWindow: 1000,
  thinkEnabled: true,
  thinkLevel: "high",
  stream: async (req, emit, signal) => {
    const last = req.messages[req.messages.length - 1]?.content ?? "";
    await emit({ content: "echo: " });
    await emit({ content: `${last} [think:${req.think}]` });
    await emit({
      usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 },
    });
    if (signal.aborted) {
      throw new Error("stream cancelled");
    }
  },
});

await m.run();
