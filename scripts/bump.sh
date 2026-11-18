#!/usr/bin/env bash
#
# Usage: ./scripts/bump.sh <new-version>
#
# Updates internal/version/version.go, commits the change, and creates an
# annotated git tag for phi (vX.Y.Z). Pushing the tag triggers
# .github/workflows/release.yml (GoReleaser); release notes come from the
# matching CHANGELOG.md section (see release.yml).
# Example: ./scripts/bump.sh v0.2.0

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VERSION_FILE="$ROOT/internal/version/version.go"

if [ $# -ne 1 ]; then
    echo "Usage: $0 <new-version>" >&2
    echo "Example: $0 v0.2.0" >&2
    exit 1
fi

NEW_VERSION="$1"

# Validate version format (v<major>.<minor>.<patch>, optionally with a prerelease suffix)
if ! echo "$NEW_VERSION" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$'; then
    echo "Error: version must match 'v<major>.<minor>.<patch>[-prerelease]' (e.g., v0.2.0 or v1.2.0-rc1)" >&2
    exit 1
fi

# Check for uncommitted changes (excluding untracked files)
if ! git -C "$ROOT" diff --quiet HEAD; then
    echo "Error: there are uncommitted changes. Please commit or stash them first." >&2
    exit 1
fi

if [ ! -f "$VERSION_FILE" ]; then
    echo "Error: version file not found: $VERSION_FILE" >&2
    exit 1
fi

if ! grep -qE '^var Version = "v[^"]+"$' "$VERSION_FILE"; then
    echo "Error: could not find Version variable in $VERSION_FILE" >&2
    exit 1
fi

# Bump the in-tree version shown on the splash screen.
sed -i.bak -E "s|^var Version = \"v[^\"]+\"$|var Version = \"$NEW_VERSION\"|" "$VERSION_FILE"
rm -f "${VERSION_FILE}.bak"

if git -C "$ROOT" diff --quiet -- "$VERSION_FILE" 2>/dev/null; then
    echo "Error: version file unchanged (already $NEW_VERSION?)" >&2
    exit 1
fi

git -C "$ROOT" add "$VERSION_FILE"
git -C "$ROOT" commit -m "chore: bump version to $NEW_VERSION"

# Tag the version-bump commit.
git -C "$ROOT" tag -a "$NEW_VERSION" -m "Release $NEW_VERSION"

echo ""
echo "Updated $VERSION_FILE"
echo "Created commit + tag: $NEW_VERSION"
echo "To push: git push origin HEAD --follow-tags"
