# Project layout

| Path                     | Purpose                                        |
| ------------------------ | ---------------------------------------------- |
| `cmd/`                   | Entry points (`main.go` via `phi`: `phi run`, `phi update`, `phi sessions`, …) |
| `ext/`                   | PXB author SDKs in `ext/go`, `ext/rust`, and `ext/ts` |
| `internal/util/update/`  | Self-update check + GitHub Releases install    |
| `internal/agent/`        | Agent engine and tool executor                     |
| `internal/agent/prompt/` | System prompt templates + Skills/MCP catalogs    |
| `internal/components/`   | TUI widgets (chat, input, palette, mention, diffview, codeview, …) |
| `internal/llm/`          | LLM clients (OpenAI-compatible + Anthropic + Gemini), streaming, skills |
| `internal/llm/extmodel/` | Extension-served model connection adapted to `llm.ChatClient` |
| `internal/project/`      | Workspace layout and config                    |
| `internal/project/model/` | Built-in model presets + request interceptors |
| `internal/session/`      | Session persistence, load/apply                |
| `internal/job/`          | Sub-agent job manager (spawn/wait/cancel)      |
| `internal/tools/`        | Agent tools (`*tool` packages + `tooldef`)     |
| `internal/toolmanager/`  | External tool discovery/download               |
| `internal/tui/editor/`   | TUI root widget (`Editor`), layout, dispatch, branch watch |
| `internal/tui/transcript/` | Session→widget projection (Mapper, Pane) |
| `internal/tui/composer/` | Chat input, `/` `?` `@` `!` pickers, palette |
| `internal/tui/footer/`   | Activity spinner, token labels, update hint |
| `internal/tui/overlays/` | Permission / ext-confirm / error panels |
| `internal/tui/diffpane/` | Git diff review overlay (`/diff`) |
| `internal/tui/codepane/` | Source viewer overlay (`/code`) |
| `internal/tui/submit/`   | Submit, cancel, slash dispatch, bash runner |
| `internal/tui/commands/` | Slash/palette registry, session/extension commands |
| `internal/tui/pathutil/` | Cwd + git branch path labels |
| `internal/tui/controller/` | Engine lifecycle, Bus/Msg, activity |
| `internal/version/`      | Build-time `Version` (splash / `phi update`) |
| `internal/util/`         | Shared helpers (diff, retry, SSE, file search, …) |
| `internal/util/diffreview/` | Unified-diff parse/render, in-memory review notes, git load |
| `internal/permission/`   | Permission policy and ask gate                 |
| `internal/extension/`    | PXB extension host: runner + layers (`core`/`loader`/`install`/`create`) |
| `internal/mcp/`          | MCP config + stdio client + pool (mcp:// URL route) |

| `ext/go/` | Go SDK and PXB protocol types, in a nested Go module |
| `ext/rust/` | Rust SDK for native PXB extensions |
| `ext/ts/` | TypeScript SDK for PXB extensions |

## Internal subpackages

| Path | Purpose |
| ---- | ------- |
| `internal/components/app/` | Root widget primitives and application components |
| `internal/components/block/` | Transcript blocks for messages and tool calls |
| `internal/components/changelist/` | Lists for viewing changed files |
| `internal/components/chat/` | Chat display and transcript interaction |
| `internal/components/chrome/` | Decorations shared by TUI widgets |
| `internal/components/codeview/` | Scrollable, highlighted source display |
| `internal/components/diffview/` | Widgets that display changes and review annotations |
| `internal/components/layout/` | Layout utilities shared by widgets |
| `internal/components/listpicker/` | Selection UI for lists |
| `internal/components/listrows/` | Row formatting shared by list widgets |
| `internal/components/mention/` | Resource and path completion display |
| `internal/components/palette/` | Command palette UI |
| `internal/components/rendercache/` | Caching helpers for rendered widgets |
| `internal/components/splash/` | Startup and welcome rendering |
| `internal/components/status/` | Status display components |
| `internal/components/text/` | Text wrapping and terminal-width helpers |
| `internal/components/textfield/` | Editable text input widgets |
| `internal/components/toast/` | Transient notifications |
| `internal/components/transcript/` | Transcript widgets and display state |
| `internal/components/tree/` | Display and navigation for tree views |
| `internal/debuglog/` | Optional debug logging |
| `internal/extension/core/` | Extension host process and protocol dispatch |
| `internal/extension/create/` | Scaffolding for native extensions |
| `internal/extension/install/` | Extension installation, updates, and removal |
| `internal/extension/loader/` | Extension manifest discovery and loading |
| `internal/extension/ui/` | UI interactions requested by extensions |
| `internal/llm/anthropic/` | Anthropic model requests and streaming |
| `internal/llm/client/` | Shared machinery for model clients |
| `internal/llm/gemini/` | Gemini model requests and streaming |
| `internal/llm/openai/` | OpenAI-compatible chat completions and streaming |
| `internal/llm/openai/responses/` | OpenAI Responses API client |
| `internal/llm/skills/` | Skill discovery and content loading |
| `internal/session/compaction/` | Selects and summarizes history for the session context |
| `internal/session/shellhist/` | Session shell command history |
| `internal/tools/agenttool/` | Tools for starting and inspecting sub-agent jobs |
| `internal/tools/bashtool/` | Local shell execution tool |
| `internal/tools/edittool/` | File editing tool |
| `internal/tools/edittool/sloppy/` | Anchored patch parsing and application |
| `internal/tools/edittool/sloppy/cmd/sloppy-demo/` | Standalone example for the anchored patch format |
| `internal/tools/findtool/` | File discovery tool |
| `internal/tools/greptool/` | Content search tool |
| `internal/tools/lstool/` | Directory and resource listing tool |
| `internal/tools/readtool/` | File and resource reading tool |
| `internal/tools/tooldef/` | Shared tool definitions and registry |
| `internal/tools/writetool/` | File and resource writing tool |
| `internal/tui/configui/` | Terminal editor for `phi config` |
| `internal/urls/` | Parsing and validating internal URLs such as `mcp://` |
| `internal/util/archive/` | Archive extraction utilities |
| `internal/util/clipboard/` | Clipboard text and image helpers |
| `internal/util/filesearch/` | Shared file and content search utilities |
| `internal/util/githubrelease/` | Reading GitHub release metadata and assets |
| `internal/util/gitx/` | Git branch and worktree helpers |
| `internal/util/image/` | Image validation and conversion utilities |
| `internal/util/platform/` | Platform-specific capability and path helpers |

## Design docs

| Path | Purpose |
| ---- | ------- |
| [`extensions.md`](extensions.md) | Extensions: discover, API, events, migration from hooks |
| [`mcp.md`](mcp.md) | MCP: zero schema pollution, mcp:// URLs, config, CLI |
| [`models.md`](models.md) | Supported models, presets, explicit `api`, thinking config |
| [`tui.md`](tui.md) | TUI: package layout, aggregation, interaction flows |
| [`session-context-building.md`](session-context-building.md) | Sessions: tree model, compaction, context building |
