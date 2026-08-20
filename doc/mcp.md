# MCP

phi connects to MCP the **mcptoon way**: configure as many servers as you want; **tool schemas never enter the model context**.

That is the main difference from hosts that dump every `tools/list` schema into the prompt — ten or a hundred servers will not burn tens of thousands of tokens before you ask a question.

| Audience | This document |
| --- | --- |
| Users | Configure servers, CLI, how to call from the TUI |
| Contributors | Interaction flow and code map |

---

## Why this is a highlight

| Pain | Typical MCP host | phi |
| --- | --- | --- |
| Context | All schemas injected at startup | Model sees only three meta-tools |
| Many servers | Uninstall / reload Tetris | Always configured; call on demand |
| Permissions | Separate system or wide open | Same Gate / Ask / Hooks as builtins |
| Footprint | Heavy SDKs, always-on processes | Hand-rolled Go stdio client, lazy start |

Agent-facing tools:

- `mcp_list` — list tool **names** on one server (compact text; `refresh=true` forces a fresh fetch)
- `mcp_inspect` — slim parameter summary for one tool
- `mcp_call` — actually invoke

Configured **server names** are listed in the system prompt (like Skills), so the model knows what exists without calling `mcp_list` first. Schemas still stay out of context.

Typical rhythm: pick a server from the prompt → `mcp_list(server=…)` → `mcp_inspect` → `mcp_call`.
Use `mcp_list(server=…, refresh=true)` when the server's tool set may have changed.

---

## Interaction flow

```text
Start TUI / phi run
  → load ~/.phi/mcp.json + <cwd>/.phi/mcp.json
  → build Pool (no subprocess yet)
  → tool list += mcp_list / mcp_inspect / mcp_call
  → system prompt += MCP catalog (server names only)

User prompt
  → model may call mcp_list(server=…) directly from the catalog
  → lazy Client → spawn → initialize → tools/list → names only
  → mcp_inspect → compact param summary
  → mcp_call → Executor → PreHooks → Gate → tools/call → result to model
```

Human CLI and the agent share the same `internal/mcp` stack:

```text
phi mcp doctor|call  ──┐
                       ├──► Pool ──► Client (stdio JSON-RPC)
model mcp_* ───────────┘
```

Sub-agents do **not** inherit MCP meta-tools by default. Disable with `PHI_MCP=off`.

---

## Quick start

Config file: `~/.phi/mcp.json` (project `<cwd>/.phi/mcp.json` overrides same-named servers).

```json
{
  "servers": {
    "browsermcp": {
      "transport": "stdio",
      "command": ["npx"],
      "args": ["@browsermcp/mcp@latest"]
    },
    "remote": {
      "transport": "http",
      "url": "http://127.0.0.1:3001/mcp",
      "headers": { "Authorization": "Bearer …" }
    }
  }
}
```

Or via CLI:

```sh
phi mcp add browsermcp -- npx @browsermcp/mcp@latest
phi mcp list
phi mcp doctor
```

**Restart phi** after config changes (Pool loads at startup).

### Migrating from Claude Desktop config

Claude / Cursor style:

```json
{
  "mcpServers": {
    "browsermcp": {
      "command": "npx",
      "args": ["@browsermcp/mcp@latest"]
    }
  }
}
```

phi equivalent:

```json
{
  "servers": {
    "browsermcp": {
      "transport": "stdio",
      "command": ["npx"],
      "args": ["@browsermcp/mcp@latest"]
    }
  }
}
```

`mcpServers` → `servers`; string `command` becomes the first element of the `command` array.

---

## CLI

```text
phi mcp list                         list configured servers
phi mcp add <name> -- <cmd> [args…]  write ~/.phi/mcp.json
phi mcp remove <name>                remove from user config
phi mcp call <server> <tool> [json]  call a tool directly
phi mcp doctor                       check config + connectivity
```

Logs: `~/.phi/logs/mcp/<name>.log` (override with `PHI_MCP_LOG_DIR`).

---

## Limits (v1)

- Transports: **stdio** and **http** (POST JSON / SSE `data:` bodies, `Mcp-Session-Id`)
- MCP tools are not registered individually into the model tool list (by design)
- Tool lists are cached for 5 minutes; `mcp_list(refresh=true)` bypasses the cache. A refresh transfers the full `tools/list` definitions, but only compact names are shown to the model.
- Empty tool lists are cached, and paginated `tools/list` responses are merged before caching.
- Stdio EOF, write failures, malformed responses, timeouts, and cancellations retire the current subprocess; the next call starts a new process and initializes again.
- A stdio tool call that exceeds the 60-second request deadline is terminated with its subprocess; long-running calls may lose server-local state.
- HTTP session expiry is recognized from `404` responses to requests that carried a session ID; HTTP GET SSE and `tools/list_changed` notifications are not implemented.
- The HTTP client negotiates protocol revision `2024-11-05` while using a compatibility subset of newer session-header behavior; the required Streamable HTTP headers and capability negotiation are not implemented.
- Some third-party packages may crash on start — use `doctor` + logs.

---

## Related code

| Path | Role |
| --- | --- |
| `internal/mcp/` | config, Client, session, stdio/http transports, Pool |
| `internal/tools/mcptool/` | `mcp_list` / `mcp_inspect` / `mcp_call` |
| `internal/agent/engine.go` | `EngineOpts.MCP` wires meta-tools |
| `cmd/mcp.go` | `phi mcp` subcommand |
