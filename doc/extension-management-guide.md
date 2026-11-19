# Extension Management Guide

The legacy runner is disconnected during the plugin refactor. `phi plugin`
still manages installed files, but Phi does not start them. The palette and
runtime workflows below describe the previous integration.
Layout, `phi.yaml` reference and the authoring SDK live in
[extensions.md](extensions.md); the host-side architecture lives in
[extension-architecture.md](extension-architecture.md).

## Palette surface

`Ctrl+K` opens the command palette. Type `extensions` to reach the
`manage extensions` row, which holds two entries:

| Entry | Effect |
| ----- | ------ |
| `list` | Discovery results with live states and per-extension actions |
| `reload all` | Rescan both extension directories and restart every extension |

## `list`

`list` pushes a submenu named `Extensions`. Rows come from a fresh discovery
scan plus the runner's states:

```
<name>  [<user|project>]  <path> [<state>]
```

Each row is itself a submenu titled with the manifest name and holds two
actions:

- `reload` — hot-swap this one extension.
- `unload` — stop and drop this one extension.

Discovery problems appear as disabled `warn: <path>: <message>` rows. An empty
scan yields a disabled `No extensions found` row, and a failed scan yields a
disabled `error: <message>` row.

## States

| State | Meaning |
| ----- | ------- |
| `pending` | No process: never started |
| `loading` | Process is starting up |
| `active` | Running and healthy |
| `failed` | Start/reload error, or the process died on its own |
| `disposed` | Shut down for good; unload drops the fiber, so the row then reads `unknown` |
| `unknown` | Discovered on disk but the runner holds no fiber for it |

`unknown` is what you see for an extension that failed to load at startup
(load failures are logged, not surfaced as rows) or that you unloaded.

`failed` is reported lazily: a fiber that is still marked active but whose
process exited on its own reads as `failed` with error `extension process
exited`, so the host never advertises an extension it can no longer call.

## `reload all`

Rescans `~/.phi/extensions` and `<cwd>/.phi/extensions`, builds a fresh runner
with a process per discovered extension, swaps it in, and closes the previous
runner. Every extension restarts; new directories appear and removed ones
disappear. A successful run toasts `Extensions: reloaded <n>`; discovery
warnings turn that into `Extensions: reloaded <n> (<m> warning(s))`.

The same full reload also runs when the model changes, so a model switch
restarts every extension.

## `reload` one extension

`reload` hot-swaps a single extension; the others keep running.

1. The replacement process starts and must finish its handshake (hello, host
   ack, registration, ready) within 5s.
2. Only then is the old process closed.
3. Host wiring, tool schemas and the extension slash commands are re-derived
   from the new process.

A failed reload reports `Failed to reload <name>: <error>` and leaves the old
process serving; the extension stays `active`. It flips to `failed` only when
there was no live old process to keep (for example after the previous process
died).

## `unload`

`unload` stops the process and returns `Unloaded extension: <name>`. The
extension's tools and slash commands are gone immediately, its palette row
stays (it is discovery-based) and now reads `unknown`. It comes back on the
next `reload all` or restart.

## `phi.yaml`

One directory per extension: `<dir>/<name>/phi.yaml` (or `phi.yml`) plus the
binary `exec` points at.

| Field | Notes |
| ----- | ----- |
| `name` | Extension name; defaults to the directory name |
| `exec` | Required. Path to the binary, relative to this directory or absolute |
| `args` | Optional argv for `exec` |
| `version` | Optional, informational |
| `description` | Optional, informational |
| `enabled` | Default `true`; `false` means the entry is not discovered at all |

Discovery rules: user dir `~/.phi/extensions`, project dir
`<cwd>/.phi/extensions`; on the same name the project wins; dot-prefixed
directories are skipped; symlinked directories are followed; duplicate names
inside one directory, unreadable manifests and a missing `exec` are warnings.

## Installing extensions

```bash
phi plugin install alice/greet   # GitHub release asset, git clone fallback
phi plugin list
phi plugin update [greet]        # [--check] to report without installing
phi plugin remove greet
```

Installs land in `~/.phi/extensions/<repo>/`. Run `Ctrl+K → extensions →
reload all` (or restart phi) to pick them up.

`PHI_EXTENSIONS=off` disables discovery entirely; no extension is listed or
started.

## Troubleshooting

Extension missing from `list`:

- The directory must contain `phi.yaml` with `exec`; anything else is skipped
  or shown as a `warn:` row.
- `PHI_EXTENSIONS=off` turns the whole layer off.
- A project extension with the same name shadows the user one.

State reads `unknown`: discovery sees the directory, the runner has no fiber —
the startup load failed, or you unloaded it.

Reload fails but the extension keeps working: that is the designed behavior.
The error text names the step (`start`, `hello timeout`, `protocol N
unsupported`, `first frame type N want hello`, `registration timeout`).

Where to look:

- Child stderr: `~/.phi/logs/ext-<name>.log` (temp dir fallback when the user
  extensions dir is unknown).
- Host events: `PHI_DEBUG=1` writes to `$PHI_DEBUG_FILE` or `phi-debug.log` in
  the working directory.
