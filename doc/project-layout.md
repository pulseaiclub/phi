# Project layout

| Path                     | Purpose                                        |
| ------------------------ | ---------------------------------------------- |
| `cmd/`                   | Entry points (`main.go` via `phi`: `phi run`, `phi update`, `phi sessions`, …) |
| `ext/`                   | Public extension API, PXB protocol (`ext/go/pxb`), author SDK (`ext/go/phi`) — nested Go module `github.com/pulseaiclub/phi/ext/go` |
| `internal/util/update/`  | Self-update check + GitHub Releases install    |
| `internal/agent/`        | Agent engine and executor                        |
| `internal/agent/prompt/` | System prompt templates + Skills/MCP catalogs    |
| `internal/components/`   | TUI widgets (chat, input, palette, mention, diffview, codeview, …) |
| `internal/components/tree/` | draws box-drawing prefixes for nested TUI rows |
| `internal/components/transcript/` | provides the scrollable, virtualized message list |
| `internal/components/toast/` | provides transient top-overlay notifications |
| `internal/components/textfield/` | is the single-line text buffer shared by the palette, |
| `internal/components/text/` | provides text wrapping, highlighting, and markdown helpers |
| `internal/components/status/` | provides small widgets for status rows, spinners, scroll |
| `internal/components/splash/` | provides the startup hero screen with an animated sphere |
| `internal/components/rendercache/` | memoizes styled text and its layout for a single widget |
| `internal/components/palette/` | provides the Ctrl+K fuzzy command palette overlay |
| `internal/components/mention/` | provides the @-file mention picker overlay |
| `internal/components/listrows/` | maps domain rows — sessions, git branches — into the rows |
| `internal/components/listpicker/` | provides a centered, opaque, filterable list overlay |
| `internal/components/layout/` | provides composable widgets for arranging and decorating UI |
| `internal/components/diffview/` | paints unified and side-by-side diff review rows |
| `internal/components/codeview/` | paints the full-screen source-file viewer pane |
| `internal/components/chrome/` | locks TUI visual grammar: glyphs, hints, borders, and |
| `internal/components/chat/` | provides the chat composer widget |
| `internal/components/changelist/` | maps git changes into the shared list picker |
| `internal/components/block/` | provides transcript widgets for tool and activity rows |
| `internal/components/app/` | provides the vxfw-style application runtime |
| `internal/llm/`          | LLM clients (OpenAI-compatible + Anthropic + Gemini), streaming, skills |
| `internal/llm/skills/` | loads and parses SKILL.md files from skill directories |
| `internal/llm/openai/` | implements the OpenAI-compatible /chat/completions client |
| `internal/llm/openai/responses/` | implements the OpenAI Responses API (/v1/responses) |
| `internal/llm/gemini/` | talks to Google's LLM API |
| `internal/llm/client/` | is the LLM facade: OpenAI-compatible, Anthropic, or Gemini, |
| `internal/llm/anthropic/` | implements the Anthropic (Claude) Messages API client |
| `internal/llm/extmodel/` | Extension-served model connection adapted to `llm.ChatClient` |
| `internal/project/`      | Workspace layout and config                    |
| `internal/project/model/` | Built-in model presets + request interceptors |
| `internal/session/`      | Session persistence, load/apply                |
| `internal/session/shellhist/` | opt-in composer prompt history (`PHI_PROMPT_HISTORY=on`) and history lock |
| `internal/session/compaction/` | provides session compaction functionality for managing conversation history |
| `internal/job/`          | Sub-agent job manager (spawn/wait/cancel)      |
| `internal/tools/`        | Agent tools (`*tool` packages + `tooldef`)     |
| `internal/tools/writetool/` | provides the write and edit tools |
| `internal/tools/tooldef/` | defines the core tool types (Tool, Handler, Result, Registry) |
| `internal/tools/readtool/` | provides the read tool |
| `internal/tools/lstool/` | provides the ls tool (directory tree listing) |
| `internal/tools/greptool/` | provides the grep tool |
| `internal/tools/findtool/` | provides the find tool (fd-backed filename search) |
| `internal/tools/edittool/` | implements the edit tool: a sloppy anchored patch applied |
| `internal/tools/edittool/sloppy/` | parses the "sloppy" anchored edit format: tolerant |
| `internal/tools/edittool/sloppy/cmd/sloppy-demo/` | prints the operation IR of a sloppy edit payload |
| `internal/tools/bashtool/` | provides the bash tool and shell execution helpers |
| `internal/tools/agenttool/` | provides the agent_* tools that spawn and manage sub-agents |
| `internal/toolmanager/`  | External tool discovery/download               |
| `internal/tui/editor/`   | TUI root widget (`Editor`), layout, dispatch, branch watch |
| `internal/tui/transcript/` | Session→widget projection (Mapper, Pane) |
| `internal/tui/composer/` | Chat input, `/` `?` `@` `!` pickers, palette |
| `internal/tui/footer/`   | Activity spinner, token labels, update hint |
| `internal/tui/overlays/` | Extension confirmation / error panels |
| `internal/components/pane/` | Full-screen Git diff review (`/diff`) and source viewer (`/code`) |
| `internal/tui/submit/`   | Submit, cancel, slash dispatch, bash runner |
| `internal/tui/commands/` | Slash/palette registry, session/extension commands |
| `internal/tui/pathutil/` | Cwd + git branch path labels |
| `internal/tui/controller/` | Engine lifecycle, Bus/Msg, activity |
| `internal/version/`      | Build-time `Version` (splash / `phi update`) |
| `internal/util/`         | Shared helpers (diff, retry, SSE, file search, …) |
| `internal/util/platform/` | contains small operating-system adaptations shared by callers |
| `internal/util/image/` | loads image files from disk: raw bytes plus a content-sniffed |
| `internal/util/gitx/` | runs git for TUI overlays that need repo state |
| `internal/util/githubrelease/` | provides shared helpers for GitHub Releases API and asset downloads |
| `internal/util/filesearch/` | finds workspace files via the fd binary |
| `internal/util/clipboard/` | reads image data from the system clipboard |
| `internal/util/archive/` | extracts release archives with path validation |
| `internal/util/diffreview/` | Unified-diff parse/render, in-memory review notes, git load |
| `internal/extension/`    | PXB extension host: runner + layers (`core`/`loader`/`install`/`create`) |
| `internal/extension/ui/` | extension UI effects onto host callbacks (notify / confirm / status) |
| `internal/extension/loader/` | is the extension config layer: it answers "which extensions |
| `internal/extension/install/` | owns the on-disk extension store: what lives in the |
| `internal/extension/create/` | scaffolds a PXB extension and compiles it |
| `internal/extension/core/` | is the extension kernel, mirroring cordis's core package: the |
| `internal/mcp/`          | MCP config + stdio client + pool (mcp:// URL route) |
| `internal/tui/configui/` | is the full-screen editor behind `phi config`: one keyboard |
| `internal/urls/` | internal URL parsing that preserves authority and path spelling |
| `internal/debuglog/` | writes opt-in diagnostics to a file so the TUI stays clean |

## Design docs

| Path | Purpose |
| ---- | ------- |
| [`extensions.md`](extensions.md) | Extensions: discover, API, events, migration from hooks |
| [`mcp.md`](mcp.md) | MCP: zero schema pollution, mcp:// URLs, config, CLI |
| [`models.md`](models.md) | Supported models, presets, explicit `api`, thinking config |
| [`tui.md`](tui.md) | TUI: package layout, aggregation, interaction flows |
| [`session-context-building.md`](session-context-building.md) | Sessions: tree model, compaction, context building |
