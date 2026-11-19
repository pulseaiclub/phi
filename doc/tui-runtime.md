# TUI runtime + plugin

The shell is moving from "one Editor switch" to a **runtime + plugin** shape:
`internal/tui/runtime` hosts plugin capabilities, `internal/tui/plugin/<name>`
owns one feature each, and `editor.NewEditor` stays the composition root that
constructs the runtime and registers plugins.

## Dependency direction

```
editor (root) ──► plugin/<name> ──► runtime ──► controller / components
      │                                                ▲
      └────────────────────────────────────────────────┘
```

- `runtime` never imports a plugin package — the compiler enforces it as an
  import cycle.
- Plugins never import each other. Cross-plugin requests go through bus msgs.
- Plugins never get a `*Editor` back-pointer; they depend on what they
  register into or what the root passes at construction.

## Runtime contract (`internal/tui/runtime`)

| Capability | API | Semantics |
| ---------- | --- | --------- |
| Msg handling | `runtime.Subscribe(rt, func(m T))` | Per concrete `controller.Msg` type; handlers run on the UI goroutine in registration order |
| Dispatch | `rt.Dispatch(m) bool` | True when at least one handler ran; `Editor.Update` falls through to the shell switch otherwise |
| Overlay layers | `rt.AddOverlay(z, draw)` / `rt.Overlays(ctx)` | Full-surface layers above the root layout, ascending z, stable within one z |
| Theme fan-out | `rt.OnTheme(fn)` / `rt.ApplyTheme(th)` | Registration order |
| Plugin entry | `rt.Register(p)` / `runtime.Plugin` | Called once during assembly; plugins must be fully constructed (no two-phase init) |

## Toast: the first migrated plugin

`plugin/toast` owns the toast widget, subscribes `ToastMsg`, contributes the
z=40 overlay layer in both the normal and the full-screen-overlay draw paths,
and updates on theme switches.

Two timing rules preserved from the shell:

- Bus producers publish `ToastMsg`; the toast appears on the next drain.
- UI-goroutine callers (`plugin/toast.Plugin.Show`) show **in the same
  frame** — error dialog replies, theme announce, `/diff` "No changes" must
  not hop through the bus or they lose a frame.

`components/toast.Toast` takes an optional `Now func() time.Time` clock so
tests and golden frames can freeze the toast lifecycle.

## Migration order (in progress)

Done: toast. Next, smallest-first: overlay (error ask) → bash → diff/code →
session/activity → composer. Each migration:

1. Moves the feature state and its msg handler into `plugin/<name>`.
2. Registers via one `rt.Register` line in `NewEditor`.
3. Keeps behavior identical — golden/interaction tests must stay green.

Deferred until the session plugin migrates: the `drainBus` batch semantics
(`atBottom` capture, stale-gen filtering, one `Sync` + `SyncFromSnap` per
batch) need a batch-aware dispatcher; until then those messages stay in the
`Editor` switch.

## Out of scope

`internal/tui/configui` is a separate App loop (`phi config`), not an Editor
plugin; it stays as is.
