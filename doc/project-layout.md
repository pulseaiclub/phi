# Project layout

| Path                     | Purpose                                        |
| ------------------------ | ---------------------------------------------- |
| `cmd/`                   | Entry points (`main.go` via `phi`: `phi run`, `phi update`, `phi sessions`, …) |
| `ext/`                   | Public extension API, PXB protocol (`ext/go/pxb`), and author SDKs (`ext/go`, `ext/rust`, `ext/ts`) — nested Go module `github.com/pulseaiclub/phi/ext/go` |
| `internal/util/update/`  | Self-update check + GitHub Releases install    |
| `internal/agent/`        | Agent engine and executor                      |
| `internal/agent/prompt/` | System prompt templates + Skills/MCP catalogs  |
| `internal/components/`   | TUI widgets (chat, input, palette, mention, diffview, codeview, …) |
| `internal/components/app/` | vxfw-style TUI application runtime |
| `internal/components/ask/` | Modal ask panel with titled question and selectable options |
| `internal/components/block/` | Transcript widgets for tool and activity rows |
| `internal/components/chat/` | Chat composer widget |
| `internal/components/chrome/` | TUI visual grammar (glyphs, hints, borders, decision-row chrome) |
| `internal/components/codeview/` | Full-screen source-file viewer pane |
| `internal/components/diffview/` | Unified and side-by-side diff review row renderer |
| `internal/components/layout/` | Composable widgets for arranging and decorating UI |
| `internal/components/listpicker/` | Centered, opaque, filterable list overlay |
| `internal/components/listrows/` | Maps domain rows (sessions, git branches, changed files) for the shared list picker |
| `internal/components/mention/` | `@`-file mention picker overlay |
| `internal/components/palette/` | Ctrl+K fuzzy command palette overlay |
| `internal/components/pane/` | Full-screen Git diff review (`/diff`) and source viewer (`/code`) |
| `internal/components/rendercache/` | Memoizes styled text and layout for a single widget |
| `internal/components/splash/` | Startup hero screen with an animated sphere |
| `internal/components/status/` | Small widgets for status rows, spinners, scroll viewports, and tool headers |
| `internal/components/text/` | Text wrapping, highlighting, and markdown helpers |
| `internal/components/textfield/` | Single-line text buffer shared by the palette, list picker, feedback box, and config form |
| `internal/components/toast/` | Transient top-overlay notifications |
| `internal/components/transcript/` | Scrollable, virtualized message list |
| `internal/components/tree/` | Box-drawing prefixes for nested TUI rows |
| `internal/llm/`          | LLM clients (OpenAI-compatible + Anthropic + Gemini) and streaming |
| `internal/llm/anthropic/` | Anthropic (Claude) Messages API client |
| `internal/llm/client/` | LLM facade selecting OpenAI-compatible, Anthropic, or Gemini by model config |
| `internal/llm/extmodel/` | Extension-served model connection adapted to `llm.ChatClient` |
| `internal/llm/gemini/` | Google Gemini LLM API client |
| `internal/llm/openai/` | OpenAI-compatible `/chat/completions` client |
| `internal/llm/openai/responses/` | OpenAI Responses API (`/v1/responses`) client |
| `internal/skills/`       | Loads and parses `SKILL.md` files from skill directories |
| `internal/project/`      | Workspace layout and config                    |
| `internal/project/model/` | Built-in model presets + request interceptors |
| `internal/project/model/cmd/modelgen/` | CLI generator syncing built-in preset specs from `models.dev` into `specs_gen.go` |
| `internal/session/`      | Session persistence, load/apply                |
| `internal/session/compaction/` | Session compaction for managing conversation history |
| `internal/session/shellhist/` | Opt-in composer prompt history (`PHI_PROMPT_HISTORY=on`) and history lock |
| `internal/job/`          | Sub-agent job manager (spawn/wait/cancel)      |
| `internal/tools/`        | Agent tools (`*tool` packages + `tooldef`)     |
| `internal/tools/agenttool/` | `agent_*` tools that spawn and manage sub-agents |
| `internal/tools/bashtool/` | `bash` tool and shell execution helpers |
| `internal/tools/edittool/` | `edit` tool applying sloppy anchored patches to one or more files |
| `internal/tools/edittool/sloppy/` | Parser for the sloppy anchored edit format (`FIND`/`PUT`/`AFTER`) into an operation IR |
| `internal/tools/edittool/sloppy/cmd/sloppy-demo/` | CLI helper printing the operation IR of a sloppy edit payload |
| `internal/tools/findtool/` | `find` tool (`fd`-backed filename search) |
| `internal/tools/greptool/` | `grep` tool for workspace content search |
| `internal/tools/lstool/` | `ls` tool for directory tree listing |
| `internal/tools/readtool/` | `read` tool for reading workspace files |
| `internal/tools/tooldef/` | Core tool types (`Tool`, `Handler`, `Result`, `Registry`) |
| `internal/tools/writetool/` | `write` tool for creating and overwriting files |
| `internal/toolmanager/`  | External tool discovery/download               |
| `internal/tui/editor/`   | TUI root widget (`Editor`), layout, dispatch, branch watch |
| `internal/tui/transcript/` | Session→widget projection (Mapper, Pane) |
| `internal/tui/composer/` | Chat input, `/` `?` `@` `!` pickers, palette |
| `internal/tui/configui/` | Full-screen keyboard form editor behind `phi config` over `~/.phi/config.yaml` |
| `internal/tui/footer/`   | Activity spinner, token labels, update hint |
| `internal/tui/overlays/` | Extension confirmation / error panels |
| `internal/tui/submit/`   | Submit, cancel, slash dispatch, bash runner |
| `internal/tui/commands/` | Slash/palette registry, session/extension commands |
| `internal/tui/pathutil/` | Cwd + git branch path labels |
| `internal/tui/controller/` | Engine lifecycle, Bus/Msg, activity |
| `internal/version/`      | Build-time `Version` (splash / `phi update`) |
| `internal/util/`         | Shared helpers (diff, retry, SSE, file search, …) |
| `internal/util/archive/` | Release archive extraction with path validation |
| `internal/util/clipboard/` | Reads image data from the system clipboard |
| `internal/util/diffreview/` | Unified-diff parse/render, in-memory review notes, git load |
| `internal/util/filesearch/` | Finds workspace files via the `fd` binary |
| `internal/util/githubrelease/` | Shared helpers for GitHub Releases API and asset downloads |
| `internal/util/gitx/` | Runs `git` for TUI overlays that need repo state |
| `internal/util/image/` | Loads image files from disk as raw bytes plus a content-sniffed MIME type |
| `internal/util/platform/` | Small operating-system adaptations shared across packages |
| `internal/extension/`    | PXB extension host: runner + layers (`core`/`loader`/`install`/`create`) |
| `internal/extension/core/` | Extension kernel handling PXB transport, subprocess lifecycle, and aggregation |
| `internal/extension/create/` | Scaffolds a PXB extension and compiles it |
| `internal/extension/install/` | On-disk extension store (`~/.phi/extensions`): install, refresh, and removal |
| `internal/extension/loader/` | Extension config discovery (`phi.yaml` manifests) without spawning processes |
| `internal/extension/ui/` | Bridges extension UI effects onto host callbacks (notify, confirm, status) |
| `internal/mcp/`          | MCP config + stdio client + pool (mcp:// URL route) |
| `internal/urls/`         | Internal URL parsing that preserves authority and path spelling |
| `internal/debuglog/`     | Opt-in diagnostics log file (`PHI_DEBUG=1`) keeping the TUI clean |

## Design docs

| Path | Purpose |
| ---- | ------- |
| [`extensions.md`](extensions.md) | Extensions: discover, API, events, migration from hooks |
| [`mcp.md`](mcp.md) | MCP: zero schema pollution, mcp:// URLs, config, CLI |
| [`models.md`](models.md) | Supported models, presets, explicit `api`, thinking config |
| [`tui.md`](tui.md) | TUI: package layout, aggregation, interaction flows |
| [`session-context-building.md`](session-context-building.md) | Sessions: tree model, compaction, context building |
