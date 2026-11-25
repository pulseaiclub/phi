# Project layout

| Path                     | Purpose                                        |
| ------------------------ | ---------------------------------------------- |
| `cmd/`                   | Entry points (`main.go` via `phi`: `phi run`, `phi update`, `phi sessions`, …) |
| `internal/util/update/`  | Self-update check + GitHub Releases install    |
| `internal/agent/`        | Agent engine, executor, jobs                     |
| `internal/agent/prompt/` | System prompt templates + Skills/MCP catalogs    |
| `internal/components/`   | TUI widgets (chat, input, palette, mention, diffview, codeview, …) |
| `internal/llm/`          | LLM clients (OpenAI-compatible + Anthropic + Gemini), streaming, skills |
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
| `internal/tui/overlays/` | Loop-error panels |
| `internal/components/pane/` | Full-screen Git diff review (`/diff`) and source viewer (`/code`) |
| `internal/tui/submit/`   | Submit, cancel, slash dispatch, bash runner |
| `internal/tui/commands/` | Slash/palette registry and session commands |
| `internal/tui/pathutil/` | Cwd + git branch path labels |
| `internal/tui/controller/` | Engine lifecycle, Bus/Msg, activity |
| `internal/version/`      | Build-time `Version` (splash / `phi update`) |
| `internal/util/`         | Shared helpers (diff, retry, SSE, file search, …) |
| `internal/util/diffreview/` | Unified-diff parse/render, in-memory review notes, git load |
| `internal/mcp/`          | MCP config + stdio client + pool (mcp:// URL route) |

## Design docs

| Path | Purpose |
| ---- | ------- |
| [`mcp.md`](mcp.md) | MCP: zero schema pollution, mcp:// URLs, config, CLI |
| [`models.md`](models.md) | Supported models, presets, explicit `api`, thinking config |
| [`tui.md`](tui.md) | TUI: package layout, aggregation, interaction flows |
| [`session-context-building.md`](session-context-building.md) | Sessions: tree model, compaction, context building |
