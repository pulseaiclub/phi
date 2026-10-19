# Permissions

Every tool call passes through the permission gate. Tools declare *what they are
about to do*; policy decides *whether they may*. This page documents the policy
side: modes, the built-in rules, and the `permissions.rules` list that lets you
add your own.

## What the gate looks at

A tool call arrives as a small set of facts, never as raw arguments:

| Fact      | Meaning                                                          |
| --------- | ---------------------------------------------------------------- |
| `tool`    | tool name, e.g. `bash`, `read`, `agent_spawn`                     |
| `kind`    | category: `exec`, `read`, `write`, `delegate`, `control`          |
| `command` | shell command, for `exec` only                                    |
| `paths`   | filesystem paths (absolute) or internal URLs (`mcp://…`)          |
| `mutating`| derived: `exec` and `write` change state, the rest do not         |

Paths are resolved through symlinks before any rule sees them, so a link inside
the workspace cannot smuggle a write outside it. A path that cannot be resolved
denies the call.

## Modes

Modes fold `ask` decisions; they are not rules and cannot be overridden by one.

| Mode              | Effect on `ask`                                              |
| ----------------- | ------------------------------------------------------------ |
| `interactive`     | prompt the user                                              |
| `readonly`        | deny; allowlisted bash still runs, `agent_spawn` always denies |
| `autopilot`       | deny — same as headless-strict today                          |
| `headless-strict` | deny (`phi run`)                                             |

## Evaluation order

Rules are grouped into three tiers and evaluated **strictest first**:

```
deny  →  ask  →  allow  →  kind default (undeclared kind → ask)
```

This is the safety property of the whole system: **no `allow` entry can outrank
a `deny` or an `ask`.** A rule can tighten a policy; to loosen one you have to
remove or edit the built-in that tightens it (or use a mode).

Two more invariants:

- An allowlist entry (`command` on an `allow` rule) only fires on a single
  simple command. `^ls\b` does not license `ls && rm -rf …`.
- Sensitive paths are matched against both the spelling you wrote and its
  resolved form.

## Built-in rules

| Source                    | Rule                                                             |
| ------------------------- | ---------------------------------------------------------------- |
| `bash.deny`               | `exec` + `command` regex → deny                                   |
| `bash.allow`              | `exec` + `command` regex → allow (simple commands only)           |
| `bash.default`            | fallback decision for `exec` when nothing matched                 |
| `sensitive_path_deny`     | path prefix → deny, for reads, writes, and delegated workdirs     |
| `workspace_only_writes`   | `write` outside the workspace → deny                              |
| `workspace_only_reads`    | `read` outside the workspace → deny; an `mcp://` URL → ask        |
| always                    | `write` to an `mcp://` URL → ask                                  |
| always                    | `agent_spawn` workdir outside the workspace → ask                 |
| always                    | `read` of paths inside the workspace → allow                      |

## Custom rules

```yaml
permissions:
  rules:
    - tool: "mcp__*"          # exact name or glob
      kind: read
      decision: ask
      reason: "external lookup: %s"

    - path:                   # filesystem prefixes, "~" is expanded on load
        - "~/.ssh"
      decision: deny

    - kind: exec
      command: "^git push"    # regex against the whole command
      decision: ask
      reason: "pushing needs a human"

    - kind: write
      scope: outside-workspace
      decision: deny
```

| Field      | Values                                                     |
| ---------- | ---------------------------------------------------------- |
| `tool`     | tool name or glob (`mcp__*`)                               |
| `kind`     | `exec`, `read`, `write`, `delegate`, `control`, `unknown`  |
| `resource` | `path` (filesystem) or `url` (internal URL)                |
| `command`  | regex; selects `exec` calls only                           |
| `path`     | list of absolute prefixes (`~` is expanded); denies too    |
| `scope`    | `outside-workspace`                                        |
| `decision` | `allow`, `ask`, `deny` (required)                          |
| `reason`   | shown to the model; `%s` becomes the command or path       |

Selectors are AND-ed, and a rule needs at least one. Combinations that could
never match are rejected at load with a message naming the entry, because a rule
that silently does nothing looks exactly like a policy nobody wrote:

- `command` together with `path`, `scope`, or `resource` (an `exec` call carries
  no path);
- `kind: exec` together with `path`, `scope`, or `resource`;
- `resource: url` together with `scope` (a URL has no workspace side).

Rules run after the built-ins, inside their decision's tier. So `deny` rules
always win, and a configured `allow` never overturns a built-in `deny` or a
built-in `ask`.

## Sub-agents

A child agent runs with `ChildPolicy`: bash defaults to allow (the hard deny
list still applies) and the mode decides the rest. `readonly` children cannot
spawn further agents.
