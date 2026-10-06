# Phi TypeScript extension SDK (`ext/ts`)

TypeScript is a **first-class language for Phi extensions** — on par with the
Go SDK in [`ext/go`](../go) and the Rust SDK in [`ext/rust`](../rust). Same
PXB wire protocol on stdin/stdout, same host features (LLM tools, slash
commands, intercepts, event subscriptions, confirm dialogs), byte-for-byte
interop, and the same install flow (`phi.yaml` + an entry under
`~/.phi/extensions/<name>/`). **Zero runtime dependencies** — the PXB wire
codec is hand-rolled on `node:` builtins; TypeScript is a dev dependency for
type-checking and `dist` builds.

Wire compatibility with the Go SDK is pinned byte-for-byte by golden tests
against `ext/go/pxb/testdata/*.bin` (`test/golden.test.ts`).

## Layout

| Path | Role |
|------|------|
| `src/pxb/` | Wire protocol: frames (`codec`), tagged fields (`fields`), message codecs (`msg`), types/events (`types`) |
| `src/phi/` | Author SDK: `Extension`, `Tool`, `Command`, `Context`, `Schema` |
| `examples/` | Runnable extensions: `hello` (commands, intercepts, subscribe), `full` (tool, confirm, submit) |
| `test/` | Golden byte-compat + unit + end-to-end fake-host tests (`test/fixtures/probe.ts`) |

## Authoring

Until the package is published, vendor `ext/ts` into your project or install
a tarball (`npm pack` in `ext/ts`, then `npm install
pulseaiclub-phi-ext-0.1.0.tgz`) — npm has no subdirectory git deps, so a
monorepo URL alone won't resolve.

Published releases come from `ext/ts/v*` tags (`@pulseaiclub/phi-ext`).
The package versions on its own line — `0.1.0` is its first release,
independent of the Rust crate's `ext/rust/v*` series:

```bash
npm install @pulseaiclub/phi-ext
```

```ts
import { Event, Extension } from "@pulseaiclub/phi-ext";

const m = new Extension("hello", "0.1.0");

m.registerCommand("hello", {
  description: "Say hi",
  handler: async (_args, ctx) => {
    await ctx.notify("info", "Hello!");
    // ctx.submit("follow-up");      // after /hello returns
    // await ctx.sendUserMessage("…"); // enqueue a turn anytime
  },
});
// needsArgs → picker/bare "/plan" fills "/plan " for the user to finish
m.registerCommand("plan", {
  description: "plan mode — /plan on|off|status",
  needsArgs: true,
  handler: async () => {},
});
m.onUserInput((_ev) => null);    // return { handled: true } to swallow
m.onToolCall((_ev) => null);     // return { block: true, reason: "…" } to deny
m.onToolResult((_ev) => null);   // return { stop: true } to end the loop
m.onTurnStopping((_ev) => null); // return { continue: true, message: "…" } to steer
m.subscribe(Event.SessionStart, (_ev) => {});
await m.run();
```

Tools are usually IO-bound, so `execute` may be async — the run loop awaits
it while the host waits for the result anyway:

```ts
import { Extension, Schema } from "@pulseaiclub/phi-ext";

m.registerTool({
  name: "fetch",
  description: "GET a URL and return its length",
  schema: Schema.object().property("url", Schema.string()),
  execute: async (args) => {
    // Network / IO calls go here; `args` is the raw JSON args.
    return { content: `got ${args.byteLength} bytes of args` };
  },
  timeoutSec: 60, // host wait; 0 = host default (30s)
  readable: true, // side-effect-free: the host may batch calls
});
```

Command handlers get a `Context` for host interaction: `notify`, `setStatus`,
`submit`, `sendUserMessage`, `confirm`, `confirmOpts`. While a command waits
on `confirm`, the run loop keeps servicing the pipe: host results wake the
waiter and work frames queue in a bounded inbox, then replay in order when
the command returns (handlers stay serial).

### Manifest and install

A TS extension ships as `phi.yaml` pointing at Node:

```yaml
name: hello
exec: node
args: [hello.mjs]   # plain JS (built), or a .ts file run by Node ≥ 23.6
```

`node` must be on the host's `PATH`. Build with `npm run build` and ship
`dist/` for Node versions without native TS stripping.

```bash
cd ext/ts && npm install && npm run build
mkdir -p ~/.phi/extensions/hello
cp -r dist examples/hello.mjs phi.yaml ~/.phi/extensions/hello/   # shape up to you
```

Reload in the TUI: **Ctrl+K → extensions → reload**.

## Development

```bash
npm install
npm run check        # tsc --noEmit + node --test
npm run build        # emits dist/ (ESM, .ts imports rewritten to .js)
```

Tests run TypeScript directly via Node's built-in type stripping, so the
suite needs Node ≥ 23.6 (the published `dist/` supports Node ≥ 20). Sources
use erasable-syntax-only TS — no enums, no parameter properties.

The run loop is a single background frame pump; handlers run serially, so
state cannot race — the queue discipline replaces the Go SDK's mutexes.
