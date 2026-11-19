# TUI runtime + plugin

The shell is a **runtime + plugin** architecture:
`internal/tui/runtime` hosts plugin capabilities, `internal/tui/plugin/<name>`
owns one feature each, and `editor.NewEditor` stays the composition root that
constructs the runtime, the command registry, and every plugin.

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

`Runtime` composes six single-concern components, one file each; it is a
composition root, not a god object.

| Component | API | Semantics |
| --------- | --- | --------- |
| `rt.Msgs` (`Dispatcher`) | `runtime.Subscribe(rt, func(m T))`, `Dispatch(m)`, `DispatchBatch(batch)`, `OnBatchStart` / `MarkBatchEvent` / `OnBatchEnd` | Handlers per concrete msg type, UI goroutine, registration order; batch postludes run once per marked batch in root-registered order |
| `rt.Submit` (`SubmitChain`) | `Add(f)` / `Filter(text)` | First filter that claims the line wins |
| `rt.Layers` (`OverlayStack`) | `Add(z, draw)` / `Draw(ctx)` | Full-surface layers above the root layout, ascending z, stable within one z |
| `rt.Panes` (`PaneStack`) | `AddFull` / `Active` / `CloseOthers`; `AddBottom` / `Height` / `Draw` / `Key` / `BlocksComposer` | Whole-frame panes with one-frame exclusivity; composer-slot panes (error ask) |
| `rt.Clock` (`FrameClock`) | `On(fn)` / `Tick()` | Once per UI frame, before layout |
| `rt.Theme` (`ThemeHub`) | `On(fn)` / `Apply(th)` | Registration order |
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

## Transport rules

How data crosses the UI/domain boundary — one rule per direction:

1. **UI goroutine → domain: direct calls.** Wiring sites pass method values
   (`e.ctrl.ImageEnabled`, `e.overlayPlugin.Cancel`), never anonymous
   forwarding closures. When a callback needs two calls or a signature
   adapter, it becomes a named `Editor` method (`publishToast`,
   `copyToClipboard`) so the wiring reads as a table of collaborators.
2. **Any goroutine → UI: `bus.Publish`.** Engine events, job progress,
   background checks publish `Msg`s; the UI goroutine applies them in
   `DispatchBatch`/`Dispatch`.
3. **Widgets stay dumb.** `internal/components` take narrow func fields /
   interfaces; they never import domain packages. The binding happens once,
   in `NewEditor`, as method values.
4. **Immutable collaborators go by value.** Single-creation handles
   (`commands.Context`, working directory) are constructor parameters, not
   accessor funcs — a `func() X` implies it can change or is computed.

## Migration status: complete

Runtime-registered plugins (implement `runtime.Plugin`): `toast`, `overlay`,
`bash`, `diff`, `code`, `session`, `activity`, `composer`. Command-domain
plugins beside them (own slash commands + pickers, no runtime concerns):
`sessions`, `branches`, `skills`, `settings`. The submit path (`Submitter`)
stays a shell coordinator built from ports (`ActivityPort`, `ShellHooks`) —
it is the submit adapter, not a feature plugin.

The drain-batch semantics survived the migration: `Editor.drainBus` is now
`rt.Msgs.DispatchBatch(bus.Drain())`, and the root registers the historical
postlude order explicitly (sync → activity-from-snap → status slot →
stick-to-bottom). The order is root composition, not hidden plugin coupling.

`Editor.Update` keeps exactly two shell cases: `FooterMsg` (update hint — the
activity plugin consumes the Set/ClearIf kinds via its own subscription) and
`ThemeMsg` (root resolves the theme and fans out). The other eleven msg types
are plugin subscriptions: `SubmitMsg`, `CancelStreamMsg`, `OverlayMsg`,
`ToastMsg`, `SessionEventMsg`, `JobProgressMsg`, `LoopStartedMsg`,
`LoopFinishedMsg`, `MentionResultsMsg`, `BashSuggestionsMsg`, `BranchLabelMsg`.

Adding a feature today: one plugin package + one constructor line in
`NewEditor`. Adding a slash command with a picker: one command-domain package
that registers its `commands.Command` and wires its picker through
consumer-shaped ports — no `Bind`-style late callbacks anywhere.

`internal/tui/archtest` guards the red lines in CI: runtime imports no
plugin package, and plugins never import each other. Plugins that need
another plugin's effect declare a consumer-shaped port (e.g. `diff.ToastSink`,
`session.ErrorAsker`) and the root passes the method values.

## Out of scope

`internal/tui/configui` is a separate App loop (`phi config`), not an Editor
plugin; it stays as is.
