---
name: release
description: Cuts a phi release — promotes the CHANGELOG section, bumps version.go, tags vX.Y.Z. Use when asked to cut a release, 发版, or tag a version.
---

# phi Release

A release is one promoted CHANGELOG section, one version bump, and one binary tag. Start from a clean
`main` that matches `origin/main`.

Choose the version first: breaking config or CLI → major; user-visible feature → minor;
fixes only → patch; `-rc1` for a preview.

## 1. Update CHANGELOG.md and internal/version/version.go

`CHANGELOG.md` — promote the `## [Unreleased]` body into a new section inside `<!-- Released section -->`,
directly above the previous newest one:

```markdown
## [X.Y.Z] - YYYY-MM-DD
```

Leave `## [Unreleased]` in place with its headings emptied. In the footer, `[Unreleased]` now compares from
`vX.Y.Z` to `HEAD`, and `[X.Y.Z]: …/releases/tag/vX.Y.Z` becomes the first version link.

`internal/version/version.go` — `var Version = "vX.Y.Z"` (also updated by `scripts/bump.sh`).

Done when `scripts/changelog-extract.sh vX.Y.Z` prints the new section. Commit, then merge to `main` — the
tag belongs on the merge commit, not on the release branch.

## 2. Tag the binary

```sh
git tag -a vX.Y.Z -m "Release vX.Y.Z"
git push origin HEAD --follow-tags
```

`release.yml` runs the tests and GoReleaser, then rewrites the release body from the extracted section. Done
when the release page body matches step 1's output and the archives and checksums are attached.
