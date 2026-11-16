# Extension Architecture

How phi loads, runs and hot-swaps PXB extensions. For the user-facing side see
[extension-management-guide.md](extension-management-guide.md); for the wire
protocol see [extensions.md](extensions.md).

The design follows [Cordis](https://github.com/cordiverse/cordis): every
extension is a supervised unit with its own lifecycle, and the loader is
separate from the runtime.

## Layers

```
Runner   one per phi process: owns the fiber set, dispatches host events
  ↓
Fiber    one per extension: lifecycle, process handle, last error
  ↓
Proc     PXB framing, RPC correlation, register tables (core)
```

`internal/extension` also holds `loader` (discovery + `phi.yaml`), `create`
(materialising an extension module for tests and `phi ext create`), `install`
(fetching extensions) and `core` (the subprocess and its framing). The Runner
never touches PXB directly; the Fiber never touches the TUI.

## Fiber states

```
Load()                     Reload() → new process, old one closed after handshake
  pending ──→ loading ──→ active
                            │  │
             (start error) ─┘  └──── Unload() ──→ disposed
```

- **pending** — no process: never started.
- **loading** — a blocking start is in flight.
- **active** — running and handshaken.
- **failed** — start failed, or the process died on its own. `Fiber.State()`
  reports `failed` whenever an "active" fiber's process is already closed, so
  the host cannot advertise an extension it can no longer call.
- **disposed** — permanently shut down; cannot be loaded again.

## Operations

| Operation | Effect | Guarantee |
| --- | --- | --- |
| `Load` | spawn, handshake, publish | a name is reserved before the slow start, so two concurrent loads cannot both win |
| `Unload` | stop the process, `disposed` | idempotent; releases tools, commands and handlers |
| `Reload` | start a replacement, then close the old process | the replacement handshakes **before** the old process is closed: a broken build leaves the running extension serving, reporting the error but staying `active` |

`Runner.LoadExtension/UnloadExtension/ReloadExtension` are the entry points the
TUI uses. They own the map entry and the host wiring; `Fiber` only owns the
process.

## Concurrency

Two locks per fiber, deliberately:

- `opMu` serialises lifecycle operations. Starting and stopping block on the
  child (handshake, shutdown budget), so a reload's check-then-act window has to
  stay atomic across that I/O. Without it a reload racing an unload can publish
  a live process onto a disposed fiber — a child nothing owns and nothing will
  ever close.
- `mu` guards the published fields (`state`, `proc`, `api`, `err`) and is the
  only lock read paths take.

`Runner.mu` guards the fiber map and a cached read model. Mutating operations
hold it only to edit the map; they do subprocess I/O outside it, so a slow
extension never blocks dispatch.

## The read model

Event dispatch used to walk every fiber and re-allocate handler lists four times
per tool call. Instead, `Runner` keeps an immutable snapshot (`view.go`) of
everything the host derives from extensions — event handlers, the tool list,
extension tools, slash commands — and rebuilds it only when the topology
changes. Hot paths become one map lookup, and the per-tool-call cost is flat in
the number of extensions.

Two properties fall out of the snapshot:

- **Deterministic order.** Fibers live in a map, so iterating them is
  randomised. The model sorts by extension name, which makes "first handler
  wins" (tool-call blocking, prompt rewriting, user-input interception)
  reproducible.
- **Cheap staleness.** Handler, tool and command registration is write-once: the
  SDK serialises it during the handshake and `Proc` drops register frames
  afterwards, and a hot swap replaces the whole API. So the only thing that can
  invalidate the model is a topology change, and every mutation path calls
  `invalidateLocked`.

Invalidation is not enough on its own — the host also has to refresh its own
copy of the tool schemas and the palette. Every mutation calls
`topologyChanged()`, which drops the model and fires the `onChange` hook.
The TUI wires that hook (constructor option `WithOnChange`) to
`ExtTopologyChangedMsg`, which refreshes the agent's
tool list and re-syncs the slash commands, so a reload (which runs off the
UI goroutine) cannot leave the palette showing commands that no longer exist.

## Packages

| Package | Contents |
| --- | --- |
| `core` | the kernel, one package like cordis: `proc.go` (PXB transport), `fiber.go` (the `Fiber` state machine), `runner.go` (event dispatch, host binding, `Close`), `view.go` (cached read model), `manage.go` (per-extension load/unload/reload) |
| `ui` | `BusUI`: extension UI effects → host callbacks |

`Load` (the facade package's `load.go`) is the bootstrap: discovery plus the
first start of every extension.
