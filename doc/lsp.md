# Language servers

`internal/util/lsp` is a small LSP client for one workspace root. It exists so the
`/code` pane can answer navigation questions with real type information instead
of a regular-expression guess. It depends on the standard library only and
touches neither disk nor network beyond reading files the server asks about.

## What it does

| Method | Server request |
| ------ | -------------- |
| `Definition` | `textDocument/definition` |
| `References` | `textDocument/references` (declarations included) |
| `Hover` | `textDocument/hover` |
| `Symbols` | `textDocument/documentSymbol` |

Diagnostics, completion, rename, and workspace symbols are deliberately out of
scope: this is a viewer, and every extra capability is another way to be wrong
in the background.

`Hit.Path` is relative to the manager root, or absolute when the target lives
outside it (Go's standard library, for instance — go-to-definition into
`GOROOT` is a normal case, not an error).

## Lifecycle

- `New(root, enabled)` starts a background scan of `PATH` and the usual
  toolchain directories. Nothing is spawned yet, and `State` never spawns at
  all — it only reports what a caller can expect (`off`, `starting`, `ready`,
  `failed`) so a pane can say "starting" rather than show nothing.
- The first question spawns the server for that file type. Servers are shared
  per root and restarted up to three times if they die.
- Documents are opened with a full-text `didOpen` at version 1; the pane closes
  a document when it jumps away, so a long session does not keep every file it
  visited alive in gopls.
- `Close` shuts every server down and refuses later questions. A spawn that was
  already in flight is shut down rather than registered, so quitting the TUI
  cannot leave an orphan.

## Exec surface

Starting a language server is the one place Phi execs a binary it did not
install and the permission gate does not see. The binary comes from `PATH`
(`gopls`, `rust-analyzer`, `pyright-langserver`, `typescript-language-server`,
`clangd`, …), is resolved once at discovery time, and is spawned with `root` as
its working directory and stdout/stderr piped to the client — never a shell, so
arguments cannot be re-parsed. A missing server is an error, not a fallback to
something else.

## Reading files

The pane reads files itself (`internal/components/codeview` renders them, the
pane loads them), so it checks `permission.SensitivePaths` before opening one:
`/code ~/.ssh/id_rsa` is refused. Reads are otherwise unrestricted, because
jumping to a definition outside the workspace is the point. The pane never
writes.
