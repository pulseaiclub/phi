# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Publishable author SDKs: nested Go module `github.com/pulseaiclub/phi/ext` (`go get …@vX.Y.Z` via `ext/vX.Y.Z` tags) and TypeScript package `@pulseaiclub/phi-ext` (npm). See [doc/extensions.md](doc/extensions.md).

### Changed

### Deprecated

### Removed

### Fixed

- Extension discovery follows symlinked directories (e.g. `.phi/extensions/foo` → `examples/foo`), so linked installs are no longer skipped.

### Security

<!-- Released section -->
<!-- Don't change this section unless doing release -->

## [0.19.0] - 2026-09-01

### Added

- **PXB extensions:** native binary extensions speaking a length-prefixed binary protocol (`ext/pxb`) over stdin/stdout. Author SDK `ext/phi`. Layout: `~/.phi/extensions/<name>/phi.yaml` + `exec`. See [doc/extensions.md](doc/extensions.md). Palette: **extensions → list/reload**. Disable with `PHI_EXTENSIONS=off`.
- `phi plugin install <github-repo[@tag]>`: shallow-clone a GitHub repo into `~/.phi/extensions/<repo>/` (requires `phi.yaml` + compiled binary).
- Extension full chain: `user_input`, `turn_stopping`, `session_compact` intercepts/events; `SubscribeEvent` with payload; `SendUserMessage` host request.
- Extension **Confirm** dialog (blocking RPC).
- `/sessions` opens an opaque filterable session picker (Enter resumes); no longer prints into the transcript.

### Changed

- TUI: `agent_spawn` / `agent_wait` tool rows show role in the detail line (`explore · …`).
- **Breaking:** Shell `plugin.json` hooks (`internal/hooks`, `PHI_HOOKS`, `.phi/hooks`) are removed. Rewrite policy as extensions (migration table in doc/extensions.md).
- **Breaking:** Yaegi-interpreted `.go` extensions are no longer loaded. Migrate to PXB binaries + `phi.yaml`.

### Deprecated

### Removed

- Extension **ShowPane** / **UpdatePane** / **ClosePane** / `OnPaneAction` (and `internal/tui/extpane`). Prefer Ctrl+K-style overlays for list UIs.
- Shell hook plugins (`plugin.json` + `type: "command"`), `doc/hooks.md`, and related TUI **hooks →** commands.
- Yaegi extension loader (`github.com/pulseaiclub/yaegi` dependency).

### Fixed

- `/resume` closes the previous extension runner and rebinds host UI (was leaking subprocesses and dropping Notify/Confirm).
- Controller `Close` and headless `phi run` shut down extension subprocesses; TUI defers `ctrl.Close()` on exit.
- Extension `Notify` / status frames now reach the TUI (`Proc.onNotify` wired in `Runner.Bind`).
- Slash-command `Submit` from PXB `CommandResponse` is delivered to the composer.
- Extension handshake registration respects the handshake timeout (no longer only checked between blocking reads).
- `session_before_switch` toast without cancel is published (previously only on deny).
- SDK command handlers receive a usable `ctx.UI` (maps to `Notify` / `SetStatus`).
- `OnToolResult.Stop` now ends the agent loop (was discarded in the executor).
- Duplicate `turn_end` from the TUI controller removed (engine owns turn indices).
- Session ID / previous / target fields ride on lifecycle `EventNotify`; host pushes `SessionMeta` after `/new` / `/resume`.

### Security

## [0.18.1] - 2026-08-28

### Added

### Changed

- **Breaking:** `plugin.json` hooks use an event-map shape (`PreToolUse`, `PostToolUse`, … + `type: "command"` shell commands). Legacy flat `pre_tool` / `run` manifests are no longer accepted. Phi extensions: `SessionShutdown` (leaving the active session; `SessionEnd` is a deprecated alias), `SessionBeforeSwitch`, `PostTurn`, and `Command` slash hooks. See [doc/hooks.md](doc/hooks.md).
- Hooks: drop unimplemented `plugin.json` fields (`if`, `once`, `statusMessage`, `asyncRewake`) until needed.

### Deprecated

### Removed

### Fixed

- `/resume` and `/clear` now replay tool executions (with their styled rows) instead of dropping them.

### Security

## [0.18.0] - 2026-08-27

### Added

- Composer shortcut help: type `?` at the start of the input (like `/` commands); Esc closes the picker.
- `Ctrl+A` / `Ctrl+E` move the composer cursor to the start/end of the current line.
- `Ctrl+U` clears the composer input, pending images, and pending skills.
- Per-model `image_enabled` config key; the composer blocks clipboard and `@` image attach and shows a warning when the active model does not support images.
- `make check` runs `deadcode -test` against a baseline so new unreachable functions fail CI without blocking on known legacy dead code.

### Changed

- Splash hint now shows `?` for shortcut help instead of the removed `!` shell-command shortcut.

### Deprecated

### Removed

- Dead code: unused layout widgets, `StatusBlock`, `input` package, orphan exported helpers, and unused clipboard read-text path.

### Fixed

- `@` file picker: cancel in-flight `fd` searches on each keystroke / close, surface timeouts as actionable hints, and note when the match list is truncated.

### Security

## [0.17.0] - 2026-08-25

### Added

- `phi run --yolo`: skip all permission checks for one headless run (benchmarks / CI).
- `phi run --tools`: limit a headless run to selected built-in tools.
- Hooks: session lifecycle events now include `usage` — token counts of the latest completed assistant turn.
- Hooks: `post_turn` event fires after each completed assistant stream with per-round `usage` (for audit metrics such as cache hit ratio).
- `util/clipboard` reads and writes the system clipboard (text; images via wl-paste/xclip, osascript/pngpaste, or PowerShell).
- Composer image attachments: Ctrl+V clipboard, `@` image files, pending queue, and LLM `Images` on submit.
- `util/image.Load` reads an image file (raw bytes + content-sniffed MIME type; png/jpeg/gif/webp, up to 10 MiB).

### Changed

- System prompt and `agent_spawn` guidance now state the sub-agent concurrency cap (default 4; spawn beyond it fails, no queue).

### Deprecated

### Removed

### Fixed

- Tool errors no longer duplicate the error text in the TUI (Error and Output shown the same message twice).

### Security

## [0.16.0] - 2026-08-22

### Added

- Hooks: `command` UI intents — `status` (footer), `list` (palette page).
- Hooks: session lifecycle events `session_start`, `session_shutdown`, `session_before_switch`.

### Changed

### Deprecated

### Removed

### Fixed

### Security

## [0.15.0] - 2026-08-20

### Added

### Changed

### Deprecated

### Removed

- `agent_list` `status` filter parameter (always returns the full list; each row still includes `status`).

### Fixed

### Security

## [0.14.0] - 2026-08-20

### Added

- Hook event `command`: `plugin.json` entries register TUI slash commands (`/name` runs `run`). stdout may `submit` a user message or `toast`.

### Changed

- `write` creates or overwrites files (no longer create-only). Use `edit` for surgical changes.
- File tools resolve relative paths against the session cwd and print cwd-relative results (`find`/`ls`/`grep`/`read`/`write`/`edit`). Absolute paths are used internally (including rg/fd) and returned only when the file is outside cwd.
- `find` (formerly `glob`) uses `fd` from `~/.phi/bin` (same as `rg`): respects `.gitignore`, early-stops at limit, optional `limit` arg.
- Renamed directory listing tool `list` → `ls`.

### Deprecated

### Removed

- Built-in `fetch` tool (and `permissions.fetch` config). Use MCP if you still need URL fetching.
- `agent_log` tool (parent agents only get `agent_wait` summaries; job logs remain on disk under `~/.phi/jobs/`).

### Fixed

- `phi update` on Windows: stage the download next to the installed binary (same volume) and fall back to copy when rename still cannot cross drives.
- Assistant fenced code blocks drop the box/`-----` chrome; a muted language caption sits above the highlighted code so mouse selection stays copy-clean.

### Security

## [0.13.0] - 2026-08-18

### Added
- TUI hot-reloads the git branch in the path label: switching branches outside the app (another terminal, an editor) refreshes the label automatically.

### Changed
- TUI activity: tool rows keep a 1-cell braille spinner; the footer uses an
  Knight-Rider scan bar so the two don't share the same glyph.
- Tool routing: bash is no longer described as an inspection tool; grep/glob no
  longer nudge `agent_spawn`; `edit.hash` is the 4 hex chars after `#` in
  `@file path#TAG` (leading `#` / full header copy-paste is accepted).
- **Breaking:** hooks are declared in `plugin.json` (one file, many hooks) instead of
  per-directory `hook.json`. Load `~/.phi/hooks/plugin.json` and
  `~/.phi/hooks/<plugin>/plugin.json` (same under the project `.phi/hooks/`).

### Deprecated

### Removed
- Per-hook `hook.json` directories. Use `plugin.json` instead.

### Fixed

### Security

## [0.12.0] - 2026-08-17

### Added

- Changelog gate: PRs must update `CHANGELOG.md` (with skip labels / `[chore]`), released sections are protected, and GitHub Release notes are taken from this file.

### Changed

- Hashline `edit` now requires a whole-file `@file path#TAG` (`hash` field) from `read`/`grep`; after a successful edit, re-read before another `edit` on that path. Per-line hashes are 3 letters (a-z) and no longer use digits.

### Removed

- Remove the redundant `agent_task` tool; compose `agent_spawn` + `agent_wait` instead.

## [0.11.0] - 2026-08-16

Baseline release when this changelog became the source of truth for user-visible changes.
Earlier releases are available from GitHub tags only.

<!-- Released section ended -->

[Unreleased]: https://github.com/pulseaiclub/phi/compare/v0.19.0...HEAD
[0.19.0]: https://github.com/pulseaiclub/phi/releases/tag/v0.19.0
[0.18.1]: https://github.com/pulseaiclub/phi/releases/tag/v0.18.1
[0.18.0]: https://github.com/pulseaiclub/phi/releases/tag/v0.18.0
[0.17.0]: https://github.com/pulseaiclub/phi/releases/tag/v0.17.0
[0.16.0]: https://github.com/pulseaiclub/phi/releases/tag/v0.16.0
[0.15.0]: https://github.com/pulseaiclub/phi/releases/tag/v0.15.0
[0.14.0]: https://github.com/pulseaiclub/phi/releases/tag/v0.14.0
[0.13.0]: https://github.com/pulseaiclub/phi/releases/tag/v0.13.0
[0.12.0]: https://github.com/pulseaiclub/phi/releases/tag/v0.12.0
[0.11.0]: https://github.com/pulseaiclub/phi/releases/tag/v0.11.0
