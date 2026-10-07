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

- `mcp_list` — list tool **names** on one server (compact text)
- `mcp_inspect` — slim parameter summary for one tool
- `mcp_call` — actually invoke

Configured **server names** are listed in the system prompt (like Skills), so the model knows what exists without calling `mcp_list` first. Schemas still stay out of context.

Typical rhythm: pick a server from the prompt → `mcp_list(server=…)` → `mcp_inspect` → `mcp_call`.

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

### Keyless web search with Parallel

[Parallel Search MCP](https://docs.parallel.ai/integrations/mcp/search-mcp) provides
`web_search` and `web_fetch` over HTTP without a Parallel API key. Anonymous access
is intended for exploration and light use, with lower rate limits. Model inference
is separate; the direct CLI calls below do not need a model or model credentials.

[Install phi](../CONTRIBUTING.md#development-setup) first (Go 1.26.3 or newer;
`make install` puts `phi` in `$GOBIN`, or `$(go env GOPATH)/bin` by default).
Ensure that directory is on your `PATH`.

Merge this `parallel` entry into the `servers` object in `~/.phi/mcp.json` or your
project's `.phi/mcp.json`. For a new file, use the complete JSON below. Keep other
entries; if `parallel` already exists, choose another name and use it in the calls.
Project entries override user entries with the same name.

```json
{
  "servers": {
    "parallel": {
      "transport": "http",
      "url": "https://search.parallel.ai/mcp",
      "headers": {
        "User-Agent": "phi (https://github.com/pulseaiclub/phi)"
      }
    }
  }
}
```

Restart any running phi instance after changing configuration. From the project
where you saved the config, verify connectivity and make two direct tool calls:

```sh
phi mcp list
phi mcp doctor
phi mcp call parallel web_search '{"objective":"Find the official Go documentation for modules.","search_queries":["Go modules reference documentation"]}'
phi mcp call parallel web_fetch '{"urls":["https://go.dev/ref/mod"],"objective":"Explain the purpose of go.mod."}'
```

`list` should include `parallel`; `doctor` should report it as `ok` with two tools.
`doctor` also checks your other configured servers. Search returns source URLs and
excerpts; fetch returns excerpts from the module reference, including what
`go.mod` declares. Replace the fetch URL with a relevant search result when
researching another topic. These commands send their queries and URLs to Parallel.
If anonymous access is rate limited, wait for the server's retry window before
trying again.

This example uses the human CLI's direct-call path. It does not demonstrate a
model tool loop or change the TUI's permission settings.

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
| `internal/tools/mcptool/` | `mcp_list` / `mcp_inspect` / `mcp_call` |
| `internal/agent/engine.go` | `WithMCP` wires meta-tools |
| `cmd/mcp.go` | `phi mcp` subcommand |
