# Project layout

| Path                     | Purpose                                        |
| ------------------------ | ---------------------------------------------- |
| `cmd/`                   | Entry points (`main.go` via pli: `phi run`, `phi update`, `phi sessions`, …) |
| `ext/`                   | Public extension API, PXB protocol (`ext/go/pxb`), author SDK (`ext/go/phi`) — nested Go module `github.com/pulseaiclub/phi/ext/go` |
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
| `internal/tui/composer/` | Chat input, slash/@ pickers, palette |
| `internal/tui/footer/`   | Activity spinner, token labels, update hint |
| `internal/tui/overlays/` | Permission / continue-ask panels |
| `internal/tui/diffpane/` | Git diff review overlay (`/diff`) |
| `internal/tui/codepane/` | Source viewer overlay (`/code`) |
| `internal/tui/submit/`   | Submit, cancel, slash dispatch, bash runner |
| `internal/tui/commands/` | Slash/palette registry, session/extension commands |
| `internal/tui/pathutil/` | Cwd + git branch path labels |
| `internal/tui/controller/` | Engine lifecycle, Bus/Msg, activity |
| `internal/version/`      | Build-time `Version` (splash / `phi update`) |
| `internal/util/`         | Shared helpers (diff, retry, SSE, file search, …) |
| `internal/util/diffreview/` | Unified-diff parse/render, review comments, git load |
| `internal/permission/`   | Permission policy and ask gate                 |
| `internal/extension/`    | PXB extension discover/spawn/runner            |
| `internal/mcp/`          | MCP config + stdio client + pool (meta-tool route) |

## Design docs

| Path | Purpose |
| ---- | ------- |
| [`extensions.md`](extensions.md) | Extensions: discover, API, events, migration from hooks |
| [`mcp.md`](mcp.md) | MCP: zero schema pollution, meta-tools, config, CLI |
| [`models.md`](models.md) | Supported models, presets, explicit `api`, thinking config |
| [`tui.md`](tui.md) | TUI: package layout, aggregation, interaction flows |
| [`session-context-building.md`](session-context-building.md) | Sessions: tree model, compaction, context building |
