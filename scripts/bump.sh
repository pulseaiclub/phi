#!/usr/bin/env bash
#
# Usage: ./scripts/bump.sh <new-version>
#
# Updates internal/version/version.go, ext/ts/package.json, commits the change,
# and creates annotated git tags for phi (vX.Y.Z) and the nested ext module
# (ext/vX.Y.Z). Pushing the phi tag triggers .github/workflows/release.yml.
# Example: ./scripts/bump.sh v0.2.0

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VERSION_FILE="$ROOT/internal/version/version.go"
TS_PACKAGE="$ROOT/ext/ts/package.json"
ROOT_GOMOD="$ROOT/go.mod"

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

NPM_VERSION="${NEW_VERSION#v}"
EXT_TAG="ext/$NEW_VERSION"

# Bump the in-tree version shown on the splash screen.
sed -i.bak -E "s|^var Version = \"v[^\"]+\"$|var Version = \"$NEW_VERSION\"|" "$VERSION_FILE"
rm -f "${VERSION_FILE}.bak"

# Keep root go.mod's require on the nested ext module in sync.
if [ -f "$ROOT_GOMOD" ]; then
    sed -i.bak -E "s|github.com/pulseaiclub/phi/ext v[0-9][^[:space:]]*|github.com/pulseaiclub/phi/ext $NEW_VERSION|" "$ROOT_GOMOD"
    rm -f "${ROOT_GOMOD}.bak"
fi

# Keep the TypeScript SDK package version in sync (no leading v).
if [ -f "$TS_PACKAGE" ]; then
    if command -v node >/dev/null 2>&1; then
        node -e "
const fs = require('fs');
const p = process.argv[1];
const v = process.argv[2];
const pkg = JSON.parse(fs.readFileSync(p, 'utf8'));
pkg.version = v;
fs.writeFileSync(p, JSON.stringify(pkg, null, 2) + '\n');
" "$TS_PACKAGE" "$NPM_VERSION"
    else
        sed -i.bak -E "s|\"version\": \"[^\"]+\"|\"version\": \"$NPM_VERSION\"|" "$TS_PACKAGE"
        rm -f "${TS_PACKAGE}.bak"
    fi
fi

if git -C "$ROOT" diff --quiet -- "$VERSION_FILE" "$ROOT_GOMOD" "$TS_PACKAGE" 2>/dev/null; then
    if git -C "$ROOT" diff --quiet -- "$VERSION_FILE"; then
        echo "Error: version file unchanged (already $NEW_VERSION?)" >&2
        exit 1
    fi
fi

git -C "$ROOT" add "$VERSION_FILE"
[ -f "$ROOT_GOMOD" ] && git -C "$ROOT" add "$ROOT_GOMOD"
[ -f "$TS_PACKAGE" ] && git -C "$ROOT" add "$TS_PACKAGE"
git -C "$ROOT" commit -m "chore: bump version to $NEW_VERSION"

# Tag the version-bump commit (phi binary + nested Go ext module).
git -C "$ROOT" tag -a "$NEW_VERSION" -m "Release $NEW_VERSION"
git -C "$ROOT" tag -a "$EXT_TAG" -m "Release $EXT_TAG"

echo ""
echo "Updated $VERSION_FILE"
[ -f "$TS_PACKAGE" ] && echo "Updated $TS_PACKAGE → $NPM_VERSION"
echo "Created commit + tags: $NEW_VERSION , $EXT_TAG"
echo "To push: git push origin HEAD --follow-tags"
echo "Also push the nested-module tag: git push origin $EXT_TAG"
