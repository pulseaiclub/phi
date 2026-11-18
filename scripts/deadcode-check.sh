#!/usr/bin/env bash
# Compare deadcode -test findings against scripts/deadcode.baseline.
# New unreachable functions fail the check; baseline entries with no finding
# are reported so the baseline can shrink instead of drifting.
#
# Baseline format: "<file> <func>" (no line numbers).
# Run with -test so helpers reachable only from tests count as live.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BASELINE="$ROOT/scripts/deadcode.baseline"
DEADCODE_TOOLS_VERSION=v0.49.0

deadcode_bin() {
	if command -v deadcode >/dev/null 2>&1; then
		command -v deadcode
		return
	fi
	local gobin
	gobin="$(go env GOBIN)"
	if [ -z "$gobin" ]; then
		gobin="$(go env GOPATH)/bin"
	fi
	if [ -x "$gobin/deadcode" ]; then
		echo "$gobin/deadcode"
		return
	fi
	echo "installing golang.org/x/tools/cmd/deadcode@${DEADCODE_TOOLS_VERSION}" >&2
	go install "golang.org/x/tools/cmd/deadcode@${DEADCODE_TOOLS_VERSION}"
	echo "$gobin/deadcode"
}

DEADCODE="$(deadcode_bin)"

findings="$(mktemp)"
sorted_baseline="$(mktemp)"
trap 'rm -f "$findings" "$sorted_baseline"' EXIT

awk -F': unreachable func: ' '
	{
		sub(/:[0-9]+:[0-9]+$/, "", $1)
		print $1 " " $2
	}
' < <(cd "$ROOT" && "$DEADCODE" -test ./...) | sort -u >"$findings"

if [ ! -f "$BASELINE" ]; then
	echo "deadcode baseline missing: $BASELINE" >&2
	exit 1
fi

# `|| true`: an all-comment (empty) baseline makes grep exit 1 and pipefail
# would kill the script before the comparison runs.
{
	grep -v '^[[:space:]]*#' "$BASELINE" || true
} | {
	grep -v '^[[:space:]]*$' || true
} | sort -u >"$sorted_baseline"

new="$(comm -23 "$findings" "$sorted_baseline")"
stale="$(comm -13 "$findings" "$sorted_baseline")"

if [ -n "$new" ] || [ -n "$stale" ]; then
	if [ -n "$new" ]; then
		echo "deadcode: new unreachable functions (not in baseline):" >&2
		echo "$new" | sed 's/^/  /' >&2
	fi
	if [ -n "$stale" ]; then
		echo "deadcode: baseline entries with no finding (remove from scripts/deadcode.baseline):" >&2
		echo "$stale" | sed 's/^/  /' >&2
	fi
	exit 1
fi

echo "deadcode: ok ($(wc -l <"$findings" | tr -d ' ') known unreachable, baseline matches)"
