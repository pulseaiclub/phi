# TUI architecture

Phi’s interactive UI follows a **panda-style** split: a thin `Editor` root widget, domain handlers that **own their state**, and dumb widgets under `internal/components`. Agent lifecycle lives in `internal/tui/controller`; session→widget projection lives in `internal/tui/transcript`.

## Object aggregation

```
cmd/main.go
  └─ editor.NewEditor(app, bus, ctrl, …)
       ├─ TranscriptPane   snap, list, mapper, subagents, welcome, text selection
       ├─ ComposerPane     chat, @/slash pickers, palette (input only)
       ├─ FooterChrome     status slot (activity↔tokens), bottom row for ext/jobs/hints
       ├─ Overlays         permission ask, continue ask
       ├─ DiffPane         full-screen git diff review (`/diff`)
       ├─ CodePane         full-screen source viewer (`/code`)
       └─ Submitter        submit / cancel / slash / bash → Controller
```

### Aggregation rules

| Owner | Composes (lifecycle) | Aggregates (injected) |
| ----- | -------------------- | --------------------- |
| `Editor` | all panes, `Submitter`, `toast`, `CommandRegistry` | `Bus`, `App`, `vx` |
| `TranscriptPane` | `MessageList`, `Mapper`, `SubagentStore`, `welcome`, `textSel` | `theme`, `spinner` ref from footer |
| `ComposerPane` | `ChatInput`, pickers, `palette` | callbacks `onSubmit`, `onCancel`, `onRedraw` |
| `FooterChrome` | `ActivityHandler`, `Spinner` | `labelContext()`, `liveJobs()` closures |
| `Overlays` | `permAskState`, `continueAskState` | `activity` ref, reply callbacks |
| `DiffPane` | review overlay (rows, notes, search) | `cwd`, submit/copy/toast callbacks |
| `CodePane` | source overlay (lines, caret, selection) | `cwd`, add-to-chat/toast callbacks |
| `Submitter` | `BashRunner` | `Controller`, `Bus`, `CommandRegistry`, pane refs |

**Hard rule:** no `*Editor` back-pointers on handlers. Cross-domain work uses injected refs, callbacks, or `Bus.Publish`. Toast feedback uses `ToastMsg` (Editor owns the overlay); do not inject toast callbacks.

---

## Package layout

```text
internal/tui/
├── editor/                 # Editor root: layout, dispatch, branch watch, command bridge
├── controller/             # Engine lifecycle, Bus/Msg, activity, permission replies
├── transcript/             # Mapper, SubagentStore, TranscriptPane
├── composer/               # ComposerPane, Wire(), Input iface
├── footer/                 # FooterChrome, token label helpers
├── overlays/               # permission + continue ask
├── diffpane/               # git diff review overlay (`/diff`)
├── codepane/               # source viewer overlay (`/code`)
├── submit/                 # Submitter, BashRunner
├── commands/               # registry, builtins, SessionCommands, BranchCommands, ExtCommands
└── pathutil/               # short path + git branch labels
```

| Package | Role |
| ------- | ---- |
| `editor` | TUI root `components.Widget`; wires panes; `Draw` drains the bus |
| `controller` | `Controller` runs `agent.Engine`; publishes `Msg` to the bus only |
| `transcript` | Projects `session.Event` → message list; sub-agent rows; copy selection |
| `composer` | Keyboard routing for chat, `/` slash, `@` mention, Ctrl+K palette |
| `footer` | Composer status slot (activity ↔ tokens), bottom footer row (ext status, jobs, update hint) |
| `overlays` | Modal permission / continue-ask panels; replaces composer when active |
| `diffpane` | Full-screen git diff review; comments persist under `.phi/review.json` |
| `codepane` | Full-screen source viewer: syntax highlight, caret, line selection |
| `submit` | User submit path: agent prompt, slash commands, `!bash`, cancel |
| `commands` | Slash/palette registry; session load/clear; extension command bridge |
| `pathutil` | Cwd shortening and git branch labels for composer chrome |

Dumb rendering widgets stay in `internal/components/` (chat, input, palette, mention, transcript blocks, …).

---

## Assembly (`cmd/main.go`)

`cmd` owns project/config loading and constructs collaborators **before** the TUI root:

```text
proj.LoadConfig()
vx, theme, cwd
redraw := controller.NewRedrawRelay()
bus    := controller.NewBus(redraw.Fire)
ctrl   := controller.NewController(bus, proj, cwd)
ui     := editor.NewEditor(app, bus, ctrl, vx, theme, cwd, model, skillPath, contextWindow, modelNames)
redraw.Bind(ui.RequestRedraw)
ui.StartUpdateCheck(...)
ui.StartBranchWatch()
app.Run(ui)
```

Inside `NewEditor`, panes are built first, then `commands.NewBuiltinRegistry`
assembles the registry and domain handlers (`SessionCommands`, `BranchCommands`,
`ExtCommands`, settings/skills/diff). `Builtin.Bind` attaches Submitter / pickers /
stream guard after `Submitter` exists. `ComposerPane.Wire(...)` connects the keyboard
path last.

`Editor` does **not** call `project.GetDefaultProject` or construct `Controller`.
It does **not** own command side effects — those live in `internal/tui/commands`.

---

## UI goroutine loop

```text
xui event
  └─ Editor.Handle → full-screen overlay (DiffPane / CodePane, when active)
       └─ ComposerPane.Handle (keys, paste, focus)
            ├─ overlay keys → Overlays (when active)
            ├─ copy keys    → TranscriptPane
            └─ submit       → bus.Publish(SubmitMsg)

app frame
  └─ Editor.Draw
       ├─ drainBus()          # apply pending Msg batch on UI thread
       ├─ full-screen overlay (DiffPane / CodePane) + toast
       ├─ layout: list | chat/overlay | footer
       └─ toast overlay (if visible)
```

Both full-screen overlays (`DiffPane`, `CodePane`) are UI-goroutine objects: they
load, handle keys and paint in one place, and their callbacks (add-to-chat, copy,
toast) run inline — no pane state needs a lock.

`RequestRedraw` → `vx.QueueRefresh()`. The bus coalesces high-frequency stream events; one armed wake can cover many publishes until the next `Drain`.

---

## Bus: publish and drain

**Publish** (any goroutine): widgets, `Controller`, background tasks (`StartBranchWatch`, `StartUpdateCheck`), extension commands.

**Drain** (UI goroutine only, at start of `Draw`):

| Phase | Messages | Handler |
| ----- | -------- | ------- |
| Batch pass | `SessionEventMsg`, `JobProgressMsg` | `TranscriptPane` → optional `Sync` + footer token refresh |
| Per-msg | everything else | `Editor.Update` → domain handler |

### Message routing

| `controller.Msg` | Handler |
| ---------------- | ------- |
| `SessionEventMsg`, `JobProgressMsg` | `TranscriptPane` (in `drainBus`) |
| `SubmitMsg`, `CancelStreamMsg` | `Submitter` |
| `OverlayMsg` (permission / continue / ext-confirm ask+dismiss) | `Overlays` |
| `FooterMsg` (activity / clear-if / update hint) | `FooterChrome` |
| `ExtSessionEffectsMsg` | `FooterChrome` (+ toast when set) |
| `MentionResultsMsg`, `BranchLabelMsg` | `ComposerPane` |
| `ToastMsg` | `Editor` toast overlay |
| `ExtCommandResultMsg` | `ExtCommands` |

---

## Interaction flows

### 1. Agent submit

```text
User Enter in composer
  → ComposerPane publishes SubmitMsg{text}
  → drainBus → Submitter.Submit
       ├─ "!cmd" prefix  → BashRunner (local shell, SessionEventMsg for output)
       ├─ "/slash"       → CommandRegistry / SessionCommands / ExtCommands
       └─ plain text     → Controller.Submit → agent.Engine.Loop (background)
                              └─ SessionEventMsg, FooterMsg, OverlayMsg, …
```

`Submitter` clears composer input after slash/bash; agent submit passes pending skills from composer.

### 2. Stream and transcript

```text
Controller.runLoop
  → engine.Loop events
  → bus.Publish(SessionEventMsg{Event})
  → drainBus: TranscriptPane.ApplySession
  → TranscriptPane.Sync (mapper + subagent store)
  → FooterChrome.SyncFromSnap (activity / status slot)
  → stick-to-bottom if user was pinned
```

`JobProgressMsg` updates nested sub-agent tool rows without full thread resync when the tree is unchanged.

### 3. Cancel

```text
Esc / composer cancel
  → CancelStreamMsg
  → Submitter.Cancel → Controller cancels stream context
  → FooterMsg{Kind: FooterClearIfActivity} when activity was cancelled
```

### 4. Permission / continue ask

```text
Engine needs approval
  → Controller publishes OverlayMsg (permission / continue / ext-confirm ask)
  → Overlays.Apply → replaces composer bottom panel
  → user keys → Overlays → Controller reply channel
  → OverlayMsg dismiss Kind on timeout/cancel
```

Composer input is blocked while an overlay is active (`OverlayBlocksComposer`).

### 5. Slash / palette / extensions

```text
/something or Ctrl+K
  → ComposerPane local UI OR SubmitMsg with slash text
  → Submitter.dispatchSlash → CommandRegistry
  → SessionCommands (/clear) or builtins
  → ExtCommands (async) → ExtCommandResultMsg → palette push / toast
```

`commands.NewBuiltinRegistry` owns slash/palette registration. Domain handlers
(`SessionCommands`, `SettingsCommands`, `ExtCommands`, …) call `Ctrl` / `Bus` /
composer directly — no Editor closures.

### 6. Background chrome

| Source | Msg | Target |
| ------ | --- | ------ |
| `StartBranchWatch` | `BranchLabelMsg` | composer bottom-right label |
| `StartUpdateCheck` | `FooterMsg` (update available) | footer update hint |
| Extension session lifecycle | `ExtSessionEffectsMsg` | footer status + toast |

---

## Layering vs `internal/components`

| Layer | Responsibility |
| ----- | -------------- |
| `internal/components/*` | Draw/handle only; no bus, no engine |
| `internal/tui/*` | State, routing, session projection, submit |
| `internal/tui/controller` | Agent engine, jobs, permission gate, extensions/MCP |
| `cmd` | Config, xui, bus/controller construction, `NewEditor` |

Reference implementation patterns: panda `interactive.go` (assembly), `message.go` (transcript), `submit.go` (submit/cancel/bash).
