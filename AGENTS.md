# Phi

Lean, high-performance Go terminal coding-agent harness. Layout: [doc/project-layout.md](doc/project-layout.md). Humans: [CONTRIBUTING.md](CONTRIBUTING.md).

## Communication Preferences

- Dry, concise, low-key humor. No flattery, no forced memes. Skip preambles and postambles.
- Comments explain "why", not "what". English only — this repo was migrated off Chinese comments.
- Error messages: actionable and specific. No vague "something went wrong".

## Constraints

- **Tool loop is ExtensionPre → Gate/Ask → Run → ExtensionPost.** Don't bypass the permission gate when changing the executor. Don't put MCP server tool schemas on the model — only `mcp_list` / `mcp_inspect` / `mcp_call`.
- **Extensions are PXB subprocesses** (`phi.yaml` + native binary under `~/.phi/extensions` / `.phi/extensions`). Wire: `ext/go/pxb`. Author SDK: Go `ext/go/phi` (module `github.com/pulseaiclub/phi/ext/go`). Host: `internal/extension`. See [doc/extensions.md](doc/extensions.md).
- **PXB message payloads are generated.** Fields are declared once in `ext/go/pxb/schema.go` (`pxb:"<tag>[,opt]"`); `make generate` writes `msg_gen.go`. Never hand-edit `msg_gen.go` or add encode/decode switches — `make check` runs `generate-check` and fails on drift. The Rust side (`ext/rust/src/pxb/msg.rs`) mirrors the same tags by hand.
- **Keep `edit` fail-closed.** Edits are anchored sloppy patches (`*** SM:EDIT` / `*** SM:FIND` / `*** SM:PUT`|`*** SM:AFTER`) applied atomically; a missing or ambiguous anchor must fail with actionable guidance, never fall back to whole-file rewrite.
- **Sub-agent transcripts stay under `~/.phi/jobs/<id>/`.** Parent context gets the wait/task summary only. Child engines have no `agent_*` tools (no nesting). Default child role is explore (no write/edit; bash allowed except hard denies).
- **UI split:** `internal/components` render; `internal/tui` wires the shell. Non-shell pieces live under `internal/tui/controller` (Engine/Bus/Msg), `internal/tui/transcript` (Mapper); version in `internal/version`. Keep widgets dumb.
- **TUI assembly:** `cmd` constructs `controller.Bus` / `controller.Controller` / App and passes them into `editor.NewEditor(...)`, which builds the commands registry (`commands.NewBuiltinRegistry`). Do not hide `GetDefaultProject` inside `tui` constructors; do not return half-initialized Controllers (`engineErr` zombies). Prefer constructor parameters over `XxxDeps` bags.
- **Stay lean and fast.** Direct module deps are few on purpose. Don't add a dependency without a clear need. Prefer changes that keep startup, idle RSS, and rebuild time small.
- **Format with `make fmt`** (gofumpt / goimports / golines, 120 cols, local prefix `github.com/pulseaiclub/phi`). Don't hand-fight import groups.
- **`testing` / `testify` stay in `*_test.go`.** `depguard` will fail the lint otherwise.
- **Tests use testify** (`assert` / `require`) for assertions — no raw `t.Fatalf` checks.
- After dependency changes: `go mod tidy`. `go.mod` is not generated.

## Contributor Guidelines

- Keep changes focused and reviewable. Add or update tests next to the code.
- Conventional Commits, English, lowercase, imperative, ≤72 chars. One logical change per commit.
- Do not put `@mentions` or `fixes #...` in commit messages (those belong in the PR).
- Do not add `Co-authored-by:`.
- User-visible changes update `CHANGELOG.md` under `## [Unreleased]`. Only release PRs
  move entries under `<!-- Released section -->` (requires `Unlock Released Changelog`).
  Skip with `Skip Changelog` / `dependencies` / `[chore]` in the PR title when no entry is needed.

## Commands

```
make help
make test                      # go test ./...
go test ./internal/extension -v # one package
make fmt                       # apply formatters
make fmt-check                 # CI formatting gate
make lint                      # golangci-lint
make deadcode                  # unreachable funcs vs baseline
make generate                  # regenerate ext/go/pxb/msg_gen.go
make check                     # fmt-check + lint + deadcode + generate-check (CI)
make build                     # ./phi
```

## Style

- Packages: lowercase, single word, match the directory (`writetool`, not `write_tool`).
- Prefer small packages under `internal/`; keep the exported surface small.
- Tests live beside the code they cover.
