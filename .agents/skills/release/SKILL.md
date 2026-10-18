---
name: release
description: Cuts a phi release — promotes the CHANGELOG section, bumps version.go, tags vX.Y.Z, then tags each changed ext/ SDK module. Use when asked to cut a release, 发版, or tag a version.
---

# phi Release

A release is one promoted CHANGELOG section, one version bump, one binary tag, and one tag per changed
`ext/` module. Start from a clean `main` that matches `origin/main`.

Choose the version first: breaking config, CLI, or extension ABI → major; user-visible feature → minor;
fixes only → patch; `-rc1` for a preview.

## 1. Update CHANGELOG.md and internal/version/version.go

`CHANGELOG.md` — promote the `## [Unreleased]` body into a new section inside `<!-- Released section -->`,
directly above the previous newest one:

```markdown
## [X.Y.Z] - YYYY-MM-DD
```

Leave `## [Unreleased]` in place with its headings emptied. In the footer, `[Unreleased]` now compares from
`vX.Y.Z` to `HEAD`, and `[X.Y.Z]: …/releases/tag/vX.Y.Z` becomes the first version link.

`internal/version/version.go` — `var Version = "vX.Y.Z"`. Root `go.mod`'s
`require github.com/pulseaiclub/phi/ext/go` carries the same version.

Done when `scripts/changelog-extract.sh vX.Y.Z` prints the new section. Commit, then merge to `main` — the
tag belongs on the merge commit, not on the release branch.

## 2. Tag the binary

```sh
git tag -a vX.Y.Z -m "Release vX.Y.Z"
git push origin HEAD --follow-tags
```

`release.yml` runs the tests and GoReleaser, then rewrites the release body from the extracted section. Done
when the release page body matches step 1's output and the archives and checksums are attached.

## 3. Tag changed ext/ modules

Each module owns a tag namespace and releases independently. List what has landed since each one's last tag:

```sh
for m in ext/go ext/rust ext/ts; do
  last=$(git tag --list "$m/v*" --sort=-v:refname | head -1)
  echo "== $m (last: $last)"
  git log --oneline "$last"..HEAD -- "$m"
done
```

A module with nothing behind its last tag gets no tag. For the rest, bump the manifest, commit, then tag and
push that module's tag alone:

| Module | Manifest version | Tag |
| --- | --- | --- |
| `ext/go` | none — tag only | `ext/go/vX.Y.Z` |
| `ext/rust` | `ext/rust/Cargo.toml` | `ext/rust/vX.Y.Z` |
| `ext/ts` | `ext/ts/package.json` | `ext/ts/vX.Y.Z` |

```sh
git tag -a ext/rust/vX.Y.Z -m "Release ext/rust/vX.Y.Z"
git push origin ext/rust/vX.Y.Z
```

Done when every module with commits behind its last tag has a new tag equal to its manifest version. That
equality is load-bearing: the publish jobs fail on a mismatch, and a version a registry has already accepted
is burned, so the fix is a new version rather than a retagged one.
