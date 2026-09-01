# @pulseaiclub/phi-ext

TypeScript author SDK for [Phi](https://github.com/pulseaiclub/phi) PXB extensions.

Speaks the same binary PXB protocol as the Go SDK (`github.com/pulseaiclub/phi/ext/phi`).

## Install

```bash
npm install @pulseaiclub/phi-ext
```

Requires Node 20+ (Bun works too — pure JS, no native addons).

## Quick start

```ts
#!/usr/bin/env node
import { createExtension } from "@pulseaiclub/phi-ext";

const m = createExtension("hello", "0.1.0");
m.registerCommand("hello", {
  description: "Say hi",
  handler: async () => {
    m.notify("info", "Hello!");
  },
});
await m.run();
```

Ship next to `phi.yaml`:

```yaml
name: hello
version: "0.1.0"
exec: ./hello.mjs   # or: node dist/main.js
```

Install under `~/.phi/extensions/hello/` and reload extensions in Phi.

## API

Mirrors the Go `ext/phi` surface: `registerTool`, `registerCommand`,
`onToolCall` / `onToolResult` / `onUserInput` / …, `subscribe` / `subscribeEvent`,
`notify`, `setStatus`, `submit`, `sendUserMessage`, `confirm` / `confirmOpts`, `run()`.
