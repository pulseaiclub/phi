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

## Migration status: complete

All eight features are plugins: `toast`, `overlay`, `bash`, `diff`, `code`,
`session`, `activity`, `composer`.

Extension points in the runtime:

| Point | Consumer |
| ----- | -------- |
| `Subscribe` per msg type | toast, overlay, bash (submit filter below), session, activity, composer |
| `AddSubmitFilter` | bash claims `!cmd` lines before the shell submit path |
| `AddFullPane` / `ActiveFullPane` | diff, code (Draw/Handle interception, blocksComposer) |
| `AddBottomPane` / `BottomHeight` / `DrawBottom` / `BottomKey` | overlay (error ask) |
| `AddOverlay` (z-ordered surface layers) | toast |
| `OnTheme` / `ApplyTheme` | every themed plugin |
| `OnBatchStart` / `MarkBatchEvent` / `OnBatchEnd` / `DispatchBatch` | session projection postludes |

The drain-batch semantics survived the migration: `Editor.drainBus` is now
`rt.DispatchBatch(bus.Drain())`, and the root registers the historical
postlude order explicitly (sync → activity-from-snap → status slot →
stick-to-bottom). The order is root composition, not hidden plugin coupling.

`Editor.Update` keeps exactly four shell cases: `SubmitMsg` (submit filter +
submitter), `CancelStreamMsg`, `FooterMsg` (update hint — the activity plugin
consumes the Set/ClearIf kinds), and `ThemeMsg` (root resolves + fans out).
`Submitter` stays a shell coordinator built from ports (`ActivityPort`,
`ShellHooks`); it is a submit-path adapter, not a feature plugin.

`internal/tui/archtest` guards the red lines in CI: runtime imports no
plugin package, and plugins never import each other. Plugins that need
another plugin's effect declare a consumer-shaped port (e.g. `diff.ToastSink`,
`session.ErrorAsker`) and the root passes the method values.

## Out of scope

`internal/tui/configui` is a separate App loop (`phi config`), not an Editor
plugin; it stays as is.
