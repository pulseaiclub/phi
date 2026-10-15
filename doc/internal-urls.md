# Internal URLs

`read` supports `skill://name` (the skill's `SKILL.md`) and
`skill://name/relative/path` (a file inside that skill's directory). `skills://`
is an alias. Names come
from the configured skill catalog; namespaced names such as `plugin:name` are
supported. The skill prompt lists both a Read URL and the real Location. Use
Location for edits and for resolving relative script or asset paths.

Each Executor gets a router snapshot loaded from the same `SkillPath` setting
used to build its prompt. Child engines use their own Executor through the same
initialization path. Rebinding an engine's tools refreshes the snapshot; reads
do not rescan the skill catalog.

The execution contract is ExtensionPre → URL resolution → Gate/Ask → read →
ExtensionPost. An extension can replace the input before resolution. Resolution
produces a canonical filesystem path, which replaces `path` in the arguments
passed to both the permission gate and the read handler. Offset and limit are
preserved. Approval remains mandatory whenever the gate requires it; an internal
URL grants no extra access. The read handler does not resolve the URL again.
Only hierarchical `scheme://` inputs with a valid URI scheme enter URL resolution.
Ordinary filesystem paths, including `notes:todo` and `release:2026-final`, keep
their existing behavior.

Resolved canonical paths with leading or trailing whitespace are rejected before
the permission gate or read handler runs, because those layers trim path whitespace
and could otherwise read a different file. For example, `skill://name/asset.txt%20`
is rejected even if `asset.txt` exists. Rename the target to remove the whitespace
before reading it through an internal URL.

Unknown schemes, unknown skills, malformed URLs, and paths escaping the skill
root fail before reading. Filesystem permission denials still apply to resolved
targets. URLs are supported only through the Executor's `read` integration;
`edit`, `write`, and other filesystem tools still require real paths. Read output
uses a filesystem `@file` header so subsequent edits can use the actual target.

No MCP, artifact, network, or `file://` URL handlers are provided. Catalog
snapshots do not freeze file contents. Canonicalization and opening remain
separate filesystem operations, so this is not a sandbox against concurrent
filesystem changes.
