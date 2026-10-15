# Internal URLs

`internal/urls` provides URL parsing and routing under development. It is not
connected to the Executor or filesystem tools. Skills continue to expose their
real filesystem Location in prompts, and `read` accepts filesystem paths.

The skill router supports `skill://name` (the skill's `SKILL.md`) and
`skill://name/relative/path` (a file inside that skill's directory). `skills://`
is an alias. Names come from the catalog passed to `NewSkillRouter`; namespaced
names such as `plugin:name` are supported.

`NewSkillRouter` snapshots skill names and paths. Resolution returns a canonical
filesystem path and rejects unknown skills, malformed URLs, and paths escaping
the skill root. The router does not read file contents or perform permission
checks. Any future tool integration must preserve ExtensionPre → Gate/Ask → Run
→ ExtensionPost and use the same filesystem target for permission checks and reads.

No MCP, artifact, network, or `file://` URL handlers are provided. Catalog
snapshots do not freeze file contents. Canonicalization and opening remain
separate filesystem operations, so this is not a sandbox against concurrent
filesystem changes.
