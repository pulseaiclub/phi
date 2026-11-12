# Contributing to phi

Thanks for your interest in contributing! phi is an agent harness for coding
work, written in Go with a terminal UI. This guide covers how to set up the
project, run checks, and submit changes.

## Table of contents

- [Development setup](#development-setup)
- [Project layout](#project-layout)
- [Running checks](#running-checks)
- [Code style](#code-style)
- [Commit conventions](#commit-conventions)
- [Submitting changes](#submitting-changes)
- [Release process](#release-process)

## Development setup

Requirements:

- Go 1.26.3 or newer (see `go.mod`)
- A terminal that supports the features phi uses (the TUI is not a web UI)

Clone and build:

```sh
git clone git@github.com:pulseaiclub/phi.git
cd phi
make build          # produces ./phi
make run            # build and run
make install        # build and install into $GOBIN
```

## Project layout

| Path                     | Purpose                                              |
| ------------------------ | ---------------------------------------------------- |
| `cmd/`                   | Entry points (`main.go`, bootstrap)                  |
| `internal/agent/`        | Agent engine, executor, prompts, sessions            |
| `internal/components/`   | TUI widgets (chat, input, palette, splash, diffview, …) |
| `internal/llm/`          | LLM client, streaming accumulation, skill loading    |
| `internal/project/`      | Project/workspace layout and config                  |
| `internal/session/`      | Session persistence, load/apply, compaction          |
| `internal/tools/`        | Agent tools (bash, read, edit, grep, find, …)        |
| `internal/toolmanager/`  | External tool discovery/download                     |
| `internal/tui/`          | Terminal UI wiring: controller, commands, keymaps    |
| `internal/util/`         | Shared helpers (diff, retry, SSE, file search, diffreview, …) |
| `internal/debuglog/`     | Debug logging                                        |

Sessions are persisted per project directory under
`~/.phi/session/<encoded-cwd>/`.

## Running checks

Before submitting, make sure everything passes locally:

```sh
make test        # go test ./...
make fmt         # apply gofumpt / goimports / golines
make fmt-check   # fail if formatting would change files (same as CI)
make lint        # golangci-lint run ./...
make lint-markdown # markdownlint via Docker (needs a local daemon)
make deadcode    # unreachable functions vs baseline (deadcode -test)
make check       # fmt-check + lint + deadcode (same as CI)
```

Install `golangci-lint` (required for `fmt` / `fmt-check` / `lint` / `check`, pinned to match CI):

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.0
```

If you add or change dependencies, run `go mod tidy` so `go.mod`/`go.sum`
stay clean.

## Code style

- Format with `make fmt` (gofumpt / goimports / golines via `.golangci.yml`).
  CI runs `make fmt-check`.
- Write tests alongside code (testify is used; see existing `*_test.go` files).
- Prefer small, focused packages. The layout under `internal/` is deliberately
  granular — when adding a feature, put it where it fits and keep the public
  surface small.
- Keep UI code decoupled: components render, the controller wires things up.
- English comments only. The repo was migrated from Chinese comments; please
  don't introduce new non-English comments.
- Run the existing tests for the package you touch and keep them green.

## Commit conventions

We use [Conventional Commits](https://www.conventionalcommits.org/). Prefix
the summary with a type and, when relevant, a scope:

- `feat(scope): ...` — new feature
- `fix(scope): ...` — bug fix
- `refactor(scope): ...` — behavior-preserving changes
- `docs: ...` — documentation
- `test: ...` — tests
- `chore: ...` — maintenance (deps, tooling)
- `ci: ...` — CI changes
- `tui: ...` / `session: ...` / `agent: ...` — common scopes used in this repo

Examples from the history:

```text
feat(session): persist sessions and add /sessions slash commands
fix(session): restore mutex on chain manager lost during panda migration
refactor(config): replace internal/config with project workspace
```

Keep the summary lowercase, imperative, and under ~72 characters. One logical
change per commit.

## Submitting changes

1. Open an issue first for non-trivial changes, or link to an existing one in
   your pull request description.
2. Create a branch off `main` (or the current default branch):

   ```sh
   git checkout -b feat/my-change
   ```

3. Make your change, add/update tests, and run `make fmt`, `make test`, and `make lint`.
4. For user-visible changes, add an entry under `## [Unreleased]` in
   `CHANGELOG.md` (Added / Changed / Deprecated / Removed / Fixed / Security).
   You may omit the PR number until the PR exists, then update the entry before
   merge (e.g. `(#123)`).
5. Commit with a conventional message (see above).
6. Push and open a pull request against the main branch. Describe what changed
   and why, and reference the issue number if there is one.
7. Address review feedback with follow-up commits; the diff should stay
   focused on the change.

CI requires every PR to touch `CHANGELOG.md` unless you skip the check by:

- adding the `Skip Changelog` label, or
- adding the `dependencies` label (Dependabot PRs get this automatically), or
- putting `[chore]` in the pull request title.

Do not edit text under `<!-- Released section -->` except in a release PR
(see below).

## Release process

`CHANGELOG.md` is the source of truth for user-facing release notes.

1. Open a release PR that moves entries from `## [Unreleased]` into a new
   version section under `<!-- Released section -->` (for example
   `## [0.12.0] - YYYY-MM-DD`), leaves empty Unreleased headings for the next
   cycle, and updates the compare/tag links at the bottom.
2. Apply the `Unlock Released Changelog` label so CI allows editing the
   released section.
3. After merge, push a tag matching `v*` (for example `v0.12.0` or
   `v0.12.0-rc1`). That triggers `.github/workflows/release.yml`, which runs
   tests and GoReleaser. Release notes are extracted from the matching
   `CHANGELOG.md` section via `scripts/changelog-extract.sh`.

## Code of conduct

Be respectful and constructive in issues, PRs, and reviews. This project is
MIT-licensed (see `LICENSE`); by contributing you agree to license your
contributions under the same terms.
