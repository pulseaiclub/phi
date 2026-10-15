# Internal URLs

`internal/urls` owns the `mcp://` URL grammar. It parses only: no pool access,
no permission checks, no I/O.

## Grammar

| URL | Names |
| --- | --- |
| `mcp://` | the configured servers |
| `mcp://<server>/` | one server |
| `mcp://<server>/tools/` | the tools on one server |
| `mcp://<server>/tools/<tool>` | one tool |

The listing levels accept `cursor` and `limit` query parameters; tool endpoints
take neither a query nor a fragment. Identifiers are case-sensitive and
percent-decoded exactly once, so a namespaced server such as `plugin:name`
stays one opaque name instead of turning into a host/port pair.

## Who consumes it

`internal/tools/tooldef` parses `mcp://` paths and dispatches to the pool:
`ls` lists, `read` shows one tool's parameter summary, `write` invokes it with
the JSON argument object as content. Plain paths fall through to file handling;
a malformed `mcp://` URL is an error, never a silent fallback.

Permission stays in `internal/permission`: internal URLs are kept verbatim
(never cwd-joined or workspace-checked), browsing is read-tier, and invocation
goes through the write gate. Any new URL-backed tool must preserve
ExtensionPre → Gate/Ask → Run → ExtensionPost and authorize the same target it
then reads.

No artifact, network, or `file://` handlers are provided.
