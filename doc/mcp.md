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
| Context | All schemas injected at startup | Model sees zero extra tools; URLs ride `ls` / `read` / `write` |
| Many servers | Uninstall / reload Tetris | Always configured; call on demand |
| Permissions | Separate system or wide open | Browsing is read-tier; calls go through the write Gate / Ask / Hooks |
| Footprint | Heavy SDKs, always-on processes | Hand-rolled Go stdio client, lazy start |

MCP is a **URL namespace**, not a tool set. The regular tools gain `mcp://` superpowers:

| Action | Call |
| --- | --- |
| List servers | `ls mcp://` |
| List tools on a server | `ls mcp://<server>/tools/` |
| Inspect one tool's params | `read mcp://<server>/tools/<tool>` |
| Invoke a tool | `write mcp://<server>/tools/<tool>` with content = JSON argument object |

Configured **server names** are listed in the system prompt (like Skills), so the model knows what exists without listing first. Schemas still stay out of context.

Typical rhythm: pick a server from the prompt → `ls mcp://<server>/tools/` → `read mcp://<server>/tools/<tool>` → `write` the arguments.

---

## Interaction flow

```text
Start TUI / phi run
  → load ~/.phi/mcp.json + <cwd>/.phi/mcp.json
  → build Pool (no subprocess yet)
  → executor carries the Pool; no extra tools registered
  → system prompt += MCP catalog (server names only)

User prompt
  → ls mcp://<server>/tools/
  → lazy Client → spawn → initialize → tools/list → names only
  → read mcp://<server>/tools/<tool> → compact param summary
  → write mcp://… + JSON args → Executor → PreHooks → write Gate → tools/call → result to model
```

Human CLI and the agent share the same `internal/mcp` stack:

```text
phi mcp doctor|call  ──┐
                       ├──► Pool ──► Client (stdio JSON-RPC)
model ls/read/write ───┘   (mcp:// URLs)
```

Sub-agents get no MCP pool by default (their executors have none). Disable with `PHI_MCP=off`.

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
- Dead subprocesses reconnect on the next call; no elaborate self-heal state machine
- Some third-party packages may crash on start — use `doctor` + logs.

---

## Related code

| Path | Role |
| --- | --- |
| `internal/mcp/` | config, Client, session, stdio/http transports, Pool |
| `internal/urls/mcp.go` | `mcp://` URL grammar (hierarchy, query, validation) |
| `internal/tools/tooldef/mcpurl.go` | URL → Pool dispatch (`MCPList` / `MCPRead` / `MCPWrite`) |
| `internal/tools/{lstool,readtool,writetool}` | route `mcp://` paths before file handling |
| `internal/agent/executor.go` | carries the Pool into tool calls via ctx |
| `cmd/mcp.go` | `phi mcp` subcommand |
