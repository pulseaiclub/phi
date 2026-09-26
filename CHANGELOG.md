# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `!` shell commands complete from project history: typing `!git s` ranks recent
  commands with Jev and lists the completions above the composer. Tab (or Enter on
  a row that differs from what was typed) fills the highlighted command; a single
  literal prefix match needs no judgement call. Requires `TYPESAFE_API_KEY`; without
  a judge the picker stays closed, and `PHI_OPTIMIZER=off` disables the
  feature even with a key configured.
- `internal/optimizer/suggest`: history-based command completion with prefix
  filtering and Jev candidate ranking plus a separate completion-confidence
  judgement. A single prefix match skips the model call; fuzzy suggestions must
  pass confidence thresholds, and only the rows above the minimum score are listed.
- User `!` shell commands now share persistent history across sessions in the same
  project, including start time, working directory, and known exit status.
  Failed and canceled attempts are recorded; history write failures show a warning.

### Changed

### Deprecated

### Removed

### Fixed

- The compaction threshold is checked before every request, not only when a turn
  ends. A turn that keeps calling tools could grow past the configured
  `context_window` while it ran: the next check happened after the model stopped
  calling tools, and overflow recovery needed the provider to reject the request
  first. The pre-request check sizes the context from the last reported usage
  plus an estimate of everything appended since, so a long turn is summarized
  before the next request goes out rather than after the window is already
  exceeded.

- Compaction summaries list the files touched by the turn the cut lands in. A
  mid-turn cut summarizes the turn prefix, but its file operations were never
  collected, so the handoff summary reported edited files as read-only (or
  omitted them) and the resumed session could re-read or overwrite that work.
  Read lists are deduplicated too, and the file block follows the summary after
  a single blank line instead of two.

- `@` file search on Windows under MSYS2/Git Bash no longer inserts absolute
  paths: `fd` prints forward slashes there while the cwd keeps backslashes, so
  the root prefix is now normalized (and compared case-insensitively) before it
  is stripped.

### Security

## [0.27.5] - 2026-09-22

### Added

- `/code <path>[:line]`: full-screen source viewer with syntax highlighting, a
  CJK-aware caret (`j`/`k`, `g`/`G`, `h`/`l`), and `v` to select lines then `a`
  to hand them to the chat input. The pane reads files itself, so it answers to
  the same deny list as the tool gate.

### Changed

### Deprecated

### Removed

### Fixed

- Horizontal scrolling no longer drops characters from the middle of a
  wide-glyph (CJK) line in the diff and file views.

### Security

## [0.27.4] - 2026-09-20

### Added

- `/branch`: pick a working branch from the composer — name and last commit
  only, `●` for the current branch, Enter runs `git switch` off the UI goroutine
  and the footer branch label refreshes immediately.
- `/branch <name>`: switch to that branch, or create it from HEAD when nothing
  carries the name yet. Remote rows check out the local branch that tracks them.

- TypeSafe System One client support via `internal/llm/jev`.

### Changed

- `/branch` completes with a trailing space, so typed arguments follow the command
  name instead of gluing onto it.

### Deprecated

### Removed

### Fixed

- `grep` no longer hangs on a matched line larger than its read buffer (minified
  bundles, one-line JSON/sourcemaps). Events over 2MB are now skipped with a
  notice instead of deadlocking the tool against ripgrep's stdout pipe.
- The composer border label carries the active think mode: it reads `model::think`
  when think is enabled, and just the model name when it is off.

### Security

## [0.27.3] - 2026-09-17

### Added

### Changed

### Deprecated

### Removed

### Fixed

- Token usage is four disjoint buckets end to end: `prompt` is the input that
  missed the cache, cache reads and writes are counted separately, and the
  context fill comes from the provider's total (or the bucket sum). Earlier the
  context fill was sized from the uncached input alone, so a cache-heavy turn sat
  at 0% of a 1M window, and Gemini, OpenAI Responses and OpenAI-compatible chat
  each split the cache out differently. Compaction now reads the same number the
  composer shows, and `phi run` emits the cache counts alongside `prompt`.

### Security

## [0.27.2] - 2026-09-16

### Added

- Add built-in `gpt-5.5` and `gpt-5.5-pro` presets using the OpenAI Responses API.

### Changed

### Deprecated

### Removed

### Fixed

- Extension RPCs distinguish host requests from replies, bound blocked writes and
  shutdown, and terminate the plugin on in-flight cancellation or timeout.
- Go and Rust SDKs preserve requests received during confirmation dialogs and
  report oversized tool results explicitly. Rust async tools support network IO
  and timers on the SDK runtime.
- Plugin updates use the installed directory independently of the manifest name,
  prepare replacements before moving the working version, and honor explicit
  version pin changes and downgrades.

### Security

## [0.27.1] - 2026-09-15

### Added

### Changed

- Composer pickers: `Tab` completes the highlighted row into the input
  (`/diff`, `@path`, each with a trailing space) instead of moving down, and never runs a command —
  `/clear`-style no-arg commands still run on `Enter` only. The session and
  diff file pickers accept on `Tab` as well. `Shift+Tab` no longer steps back;
  use `Up` / `Ctrl+P`.

### Deprecated

### Removed

### Fixed

- Compaction summaries are capped at a fraction of the headroom they free
  (0.8x `reserveTokens` for history, 0.5x for a turn prefix), and a summary the
  provider stopped at that cap is rejected instead of persisted. A truncated
  summary used to overwrite the session checkpoint and drop every message it
  stood in for, with no way back.

- Compaction now measures the cut budget per message instead of summing the
  provider's reported usage. Each assistant message reports the size of the
  whole conversation up to that turn, so the budget overflowed on the newest
  message: the recent window was never kept, the cut was always mid-turn, and a
  previous summary could be replaced with `No prior history.`
- Compaction no-ops instead of persisting a summary when nothing falls outside
  the recent window.
- Cutting mid-turn (compaction lands inside a turn) now sends a turn-prefix
  summarization prompt with the prefix. The request carried the conversation
  dump and no instruction at all, so the model's continuation — not a summary —
  was what got persisted as the session summary.
- The transcript compaction marker now shows the pre-cut context size
  (`Compacted from 15k tokens`). The count was persisted on every compaction
  entry but nothing ever read it: the marker text was hardcoded in three
  places, and a resumed session dropped the value entirely.
- Resuming a session (`/sessions`) now refreshes the composer token readout
  from the resumed session instead of keeping the previous session's counts;
  sessions without reported usage clear the label.
- Compaction now reads token usage from the persisted entry instead of the
  in-memory message field, which is dropped on load. Resumed sessions were
  seen as having spent zero tokens, so the first auto-compaction summarized
  nothing and context-overflow recovery did not compact at all.

### Security

## [0.27.0] - 2026-09-15

### Added

- OpenAI Responses API route (`api: OpenAIResponses`) via
  `/v1/responses`, for models that no longer speak chat-completions
  (e.g. GPT-5 tool calling).
- Built-in DeepSeek model presets: config entries named `deepseek-flash`
  or `deepseek-v4-pro` auto-fill base_url / context_window /
  image_enabled from the catalog, so only name + api_key is required.
- Built-in Gemini model presets (`gemini-2.5-pro`, `gemini-2.5-flash`,
  `gemini-3-pro`, `gemini-3-flash`) with provider-native thinking
  config (budget for 2.x, level for 3.x).
- Built-in Kimi model presets (`kimi-k3`, `kimi-k2.7-code`) with Moonshot API defaults.
- Built-in GLM presets (`glm-5.3`, `glm-5.3-flash`) on the z.ai coding
  plan, with Preserved Thinking.
- Per-model `think_enabled` / `think_level` config keys and
  `PHI_THINK_LEVEL` env var override.
- Rust SDK (`ext/rust`, crate `phi-ext`): `Context` gains `cwd()`,
  `session_id()`, and `has_ui()` accessors.
- Docs: [Supported models](doc/models.md).

### Changed

- Provider routing uses explicit `models[].api` (`OpenAI` /
  `OpenAIResponses` / `Anthropic` / `Gemini`) instead of guessing from
  model name or base URL.
- Vendor thinking wire shape (DeepSeek `extra_body`, Gemini budget/level)
  lives on model preset request interceptors, not client name matching.
- Rust SDK: PXB messages are built by the declarative `pxb_message!` macro —
  `pxb::Hello::encode()` / `decode()` replace the free `pxb::encode_hello` /
  `decode_hello` functions (and likewise per message type). Wire format
  unchanged.
- `/diff`: an empty overlay names the comparison (`No changes vs <rev>`) and
  reminds that untracked files never show up; git failures collapse to one
  line with the next step (`fetch first`, `open inside the repo`).

### Deprecated

### Removed

- `/resume` slash command — use `/sessions` picker or `phi run --session <id>` instead.

### Fixed

- Windows: `fd` / `rg` installed in `~/.phi/bin` as `fd.exe` / `rg.exe` are
  found again — the bin dir probe now tries the `.exe` suffix, so `find` and
  `grep` no longer report "fd is not available".
- Windows: Git Bash is found for per-user installs
  (`%LocalAppData%\Programs\Git`) and for custom roots reached through the
  Git on PATH (`D:\Git`), instead of falling through to WSL's legacy
  `bash.exe` shim and failing with `execvpe(/bin/bash): No such file or
  directory`.

### Security

## [0.26.0] - 2026-09-10

### Added

- Extensions: `ToolResult.Expanded` opens the TUI tool row by default (e.g. plan
  body). User toggle still wins after the first interaction.
- CI / `make lint-markdown`: markdownlint on `**/*.md`.
- TUI `/diff`: full-screen git diff review (working tree / staged / HEAD),
  line notes in `.phi/review.json`, `a` sends notes to the agent.

### Changed

### Deprecated

### Removed

### Fixed

- Compaction file-op details are a typed `session.CompactionDetails` (not `any`),
  so persist/reload and later compact rounds keep prior read/modified files.
- `/diff`: cursor movement no longer leaves a blue trail on swept rows; help
  overlay no longer fills with selection background. Notes autosave; drop
  unused GitHub-shaped comment fields and the redundant `w` save key.

### Security

<!-- Released section -->
<!-- Don't change this section unless doing release -->

## [0.25.1] - 2026-09-10

### Added

### Changed

### Deprecated

### Removed

### Fixed

- MCP HTTP transport: SSE bodies that interleave server notifications
  (e.g. `notifications/message` log frames, no `id`) before the JSON-RPC
  response frame now resolve to the response frame instead of the first
  parseable frame, which previously made `tools/call` return an empty
  result when a server streams log frames before answering.
- Preserve Unicode when copying TUI text to the Windows clipboard.

### Security

## [0.25.0] - 2026-09-09

### Added

- Sub-agents: optional per-role model defaults in `agents.models` (`explore` /
  `review` / `worker`). Palette: settings → agents → models → role → model
  (session-only; `(inherit parent)` clears). Omitted roles inherit the parent
  model. (`project`, `agent`, `tui`)

### Changed

- Sub-agents: child gates use `BashDefault=Allow` (hard deny list still applies), so explore/review/worker can run non-allowlisted shell (`make`, pipelines, tests) without Ask→Deny folding. Role hints and parent spawn guidance emphasize task contracts (recon / review report / scoped implement) rather than allowlisted bash. Hard deny now also blocks piping into `sh`/`bash`.

### Deprecated

### Removed

### Fixed

- Agent: mid-loop context-window overflow (`prompt is too long` and similar
  provider errors) now force-compacts once and retries the stream instead of
  failing the turn cold. A second overflow still fails closed. (`agent`, `llm`)
- Config: `agents.enabled` omitted under an `agents:` block (e.g. only
  `agents.models` set) no longer decodes as false; default stays on. (`project`)

### Security

## [0.24.0] - 2026-09-08

### Added

- Extensions: tools can be marked `Readable` (side-effect-free) via the Go
  SDK `Tool.Readable` or the Rust SDK `Tool::readable()`; the host surfaces
  it as `Definition.Readable`, so a batch of all-readable calls — including
  extension tools — runs concurrently.

### Changed

- Agent: when every tool call in a turn targets a read-only tool
  (`Definition.Readable` — read/grep/ls/find), the calls now execute
  concurrently; results keep call order, and any write-capable call in the
  batch falls back to sequential execution.
- Rust SDK (`ext/rust`): tool `execute` handlers can now be async
  (`Tool::new_async`) — the SDK drives them to completion on a
  single-threaded tokio runtime, so network / IO calls work without blocking
  tricks; sync `Tool::new` handlers are unchanged.
- LLM provider errors now read as one compact line — e.g. `anthropic API error
  (400): prompt is too long` — using the provider's own message instead of a
  raw JSON body, which can no longer flood the terminal or session history.
- TUI: assistant blocks that end in error state render their body in red under
  an `Error:` label, so failures read like tool errors.

### Deprecated

### Removed

### Fixed

### Security

## [0.23.0] - 2026-09-07

### Added

- Gemini endpoints (Google AI Studio / Vertex AI): streaming chat and compaction with tool calling, image input, system prompts, and model thinking surfaced as reasoning. (`llm`)
- `phi plugin list` / `phi plugin update [repo[@ref]] [--check]` / `phi plugin remove <repo>` (alias `rm`): audit and update extensions without manual removal. Install records the GitHub source in `~/.phi/extensions/<repo>/.phi-install.json`; `update` re-resolves that source and swaps the directory atomically (release archive preferred, git clone fallback, pinned tags stay pinned unless overridden). Extensions not installed via `phi plugin install` are left untouched.

### Changed

- Rust SDK (`ext/rust`): tool-schema and confirm-dialog JSON now serializes with `serde`/`serde_json` instead of hand-rolled writers (wire output unchanged; `preserve_order` keeps key order).

### Deprecated

### Removed

### Fixed

- Fix PowerShell installer parsing of checksum mismatch errors.
- Extension footer status (e.g. plan-mode hints) is reset when extensions are reloaded or the model is switched, so stale text no longer outlives the extension subprocess.

### Security

## [0.22.0] - 2026-09-04

### Added

### Changed

- TUI: composer bottom-left status slot now toggles between activity/spinner (busy) and token stats (idle); busy state animates a highlight through the activity text (no block-glyph scan bar); the bottom footer row is reserved for extension status, live jobs, and update hints.
- TUI: chat-input border chrome speaks one ambient voice (path, idle tokens, activity); model Identity remains the sole frame accent; context % escalates to Warning/Destructive only under pressure.
- TUI color dialect: Identity ≠ Success; Title for panel/md structure; ToolName is the sole cool action accent (tools, keybinds, paths, code); Warning/Destructive reserved for risk; default theme is Dark (Terminal stays a compatibility option).

### Deprecated

### Removed

### Fixed

### Security

## [0.21.1] - 2026-09-03

### Added

- Slash commands can declare `NeedsArgs` (Go) / `needs_args` (Rust). Picker accept or bare submit of `/name` leaves `/name` plus a trailing space in the composer so the user can type arguments instead of auto-running and toasting usage. Built-in `/resume` uses this.

### Changed

- Go author SDK nested module now publishes under `github.com/pulseaiclub/phi/ext/go` (imports `…/ext/go/phi`, `…/ext/go/pxb`; tags `ext/go/vX.Y.Z`), matching its repo location `ext/go/` so proxies can resolve it.

### Deprecated

### Removed

### Fixed

- Publish Rust crate workflow: write `exists` to `$GITHUB_OUTPUT` so `cargo publish` actually runs when the version is new (previously the step was always skipped).

### Security

## [0.21.0] - 2026-09-03

### Added

- Extension tools can set `DetailFromArgs` (Go) / `detail_from_args` (Rust) so the TUI shows a one-line summary instead of raw JSON args before execution.
- `phi plugin install` prefers a GitHub Release archive for the current OS/arch (`{repo}_{version}_{goos}_{goarch}.tar.gz`, same layout as `phi update`), with optional checksum verification; falls back to shallow git clone when no matching asset exists.

### Changed

### Deprecated

### Removed

### Fixed

### Security

## [0.20.0] - 2026-09-02

### Added

- Extension tools can set `TimeoutSec` (Go SDK) / `timeout_sec` (Rust SDK) so the host waits longer than the default 30s RPC budget for slow tools (e.g. HTTP fetch). Clamped to 1–3600s.
- Rust SDK: typed `phi::Schema` builder for tool parameters (replaces raw `Vec<u8>`), staying zero-dep instead of Codex-style `schemars`.

### Changed

- TUI chrome consistency: shared glyph/hint dialect (`internal/components/chrome`), unified ask/continue/confirm option rows, `Theme.Identity` for model/user/bash chrome (separate from Success status), and aligned transcript expand indent / status suffixes. Permission/continue/confirm overlays navigate with arrows only (no Alt+N accelerators). Sessions list picker uses a wider panel (~90% / up to 112 cols) so previews truncate less.

### Deprecated

### Removed

- Palette command `clipboard copy last message` (`Ctrl+Shift+C`); the key chord still copies selection / last message via the transcript handler.

### Fixed

### Security

## [0.19.2] - 2026-09-02

### Added

- Rust author SDK: zero-dependency `ext/rust` crate (`phi-ext`) — PXB wire protocol (`pxb`) + `Extension` API (`phi`), byte-compatible with the Go SDK via golden fixtures, plus fake-host end-to-end tests. See [doc/extensions.md](doc/extensions.md).

### Changed

### Deprecated

### Removed

### Fixed

### Security

## [0.19.1] - 2026-09-01

### Added

- Publishable Go author SDK: nested module `github.com/pulseaiclub/phi/ext` (`go get …@vX.Y.Z` via `ext/vX.Y.Z` tags). See [doc/extensions.md](doc/extensions.md).

### Changed

### Deprecated

### Removed

### Fixed

- Extension discovery follows symlinked directories (e.g. `.phi/extensions/foo` → `examples/foo`), so linked installs are no longer skipped.

### Security

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

[Unreleased]: https://github.com/pulseaiclub/phi/compare/v0.27.5...HEAD
[0.27.5]: https://github.com/pulseaiclub/phi/compare/v0.27.4...v0.27.5
[0.27.4]: https://github.com/pulseaiclub/phi/releases/tag/v0.27.4
[0.27.3]: https://github.com/pulseaiclub/phi/releases/tag/v0.27.3
[0.27.2]: https://github.com/pulseaiclub/phi/releases/tag/v0.27.2
[0.27.1]: https://github.com/pulseaiclub/phi/releases/tag/v0.27.1
[0.27.0]: https://github.com/pulseaiclub/phi/releases/tag/v0.27.0
[0.26.0]: https://github.com/pulseaiclub/phi/releases/tag/v0.26.0
[0.25.1]: https://github.com/pulseaiclub/phi/releases/tag/v0.25.1
[0.25.0]: https://github.com/pulseaiclub/phi/releases/tag/v0.25.0
[0.24.0]: https://github.com/pulseaiclub/phi/releases/tag/v0.24.0
[0.23.0]: https://github.com/pulseaiclub/phi/releases/tag/v0.23.0
[0.22.0]: https://github.com/pulseaiclub/phi/releases/tag/v0.22.0
[0.21.1]: https://github.com/pulseaiclub/phi/releases/tag/v0.21.1
[0.21.0]: https://github.com/pulseaiclub/phi/releases/tag/v0.21.0
[0.20.0]: https://github.com/pulseaiclub/phi/releases/tag/v0.20.0
[0.19.2]: https://github.com/pulseaiclub/phi/releases/tag/v0.19.2
[0.19.1]: https://github.com/pulseaiclub/phi/releases/tag/v0.19.1
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
