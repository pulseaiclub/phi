# Building LLM context from the session tree

`internal/session/manager.go` keeps every session message in a tree. Entries
are never deleted: compaction summarizes old history into a new node instead of
removing it, so the tree only grows. `buildSessionContext` walks one branch of
that tree on every context-cache miss to produce the messages sent to the LLM.
This document explains what the context must contain, why the original
implementation did work proportional to the *whole* branch, and how the
optimized version bounds the work by the *output*.

## Data model

Entries form a tree linked by parent ID. A conversation branch is the chain
from its leaf back to the first node (the session header sits at
`entries[0]` but is not part of the message chain).

```
root entry ── m1 ── … ── F ── … ── C ── … ── leaf
```

- `C` is the newest **compaction entry**: an LLM-written summary of the
  history below it, stored as a normal tree node whose parent is the current
  leaf at compaction time.
- `F` (`C.Compaction.FirstKeptEntryID`) marks the retention boundary: the
  compaction summarized everything below `F`; from `F` upward messages stay
  verbatim. When a compaction happens, the session is cut somewhere in the
  recent window and `F` names the first kept entry.
- History below `F` is dead for context purposes. It stays in the tree for
  replay and diagnostics, but it can never appear in LLM context again.

The context produced for a branch is, oldest first:

1. the newest compaction entry (its summary reads as history the model no
   longer sees verbatim), then
2. every message from `F` up to the leaf.

Older compaction entries are never re-emitted; only the newest one leads.
Without any compaction the context is simply the whole branch.

Model selections are append-only `EntryModelChange` nodes with an ID, parent ID,
timestamp, and configured model name. The initial selection uses the same
record type; new headers contain no model state. `ModelName` walks the selected
branch back to its latest model change, including history below a compaction
boundary. A branch without a model-change record has no stored model selection.

Retained model-change nodes pass through the entry projection but contribute
no LLM messages or transcript rows. Forks copy the selected branch, including
its model history, so unrelated branches cannot affect the restored model.

## Original implementation

The original `buildSessionContext` did, for every call:

1. walk the branch from the leaf to the root through `byID`, collecting every
   ancestor — **including all summarized history below `F`**;
2. reverse that path;
3. scan it for the newest compaction entry;
4. scan from the compaction back down to the root for `FirstKeptEntryID`;
5. emit the retained tail.

It also preallocated the path with `make([]MessageEntry, 0, len(entries))`,
sized to the *entire* session, not the branch being walked.

Worst case (no compaction) is output-bound at O(branch depth) and fine.
But once a compaction exists the context is usually a small tail while the
branch keeps every historical round, so steps 1–4 re-traverse dead history
on every call. Sessions that are compacted repeatedly accumulate unbounded
dead history, making each context build more expensive than the last even
though the output stays small.

## Optimized implementation

Key observation: the walk can stop at the retention boundary. Once the walk
(from the leaf upward) reaches the newest compaction `C` and reads its
`FirstKeptEntryID`, nothing below that ID can reach the output, so there is no
reason to keep walking toward the root.

The rewrite is a single bounded walk, newest first:

```text
up = []
current = leaf
loop:
  up.push(current)
  if no compaction seen yet and current is a compaction entry:
      C = current; keepFrom = C.FirstKeptEntryID
      if keepFrom is empty: break          # summary is authoritative
  if keepFrom is set and current.ID == keepFrom:
      break                                # retention boundary reached
  current = byID[current.parent]           # stop at chain root / missing parent
```

Emission then prepends the compaction entry and appends the kept messages from
oldest to newest (skipping non-message entries, matching the original filter).
`up` grows from a small capacity to what is actually walked.

- No compaction: the walk still drains the branch — required, the output *is*
  the branch.
- With compaction: only the compaction plus the retained tail (`F` upward) is
  walked, so the cost is O(context size), independent of total history.

## Behavioural equivalence

The rewrite is intentionally behaviour-preserving. Edge cases match the
original:

- **Only the newest compaction leads.** The original picked the last
  compaction in the root-first path; the new code detects the first one
  encountered walking up — the same node.
- **Empty `FirstKeptEntryID`**: nothing below the summary stays verbatim; the
  walk stops at the compaction immediately (original kept `firstKeptIdx =
  compactionIdx`).
- **Dangling `FirstKeptEntryID`** (not on the branch): the original fell back
  to keeping only messages above the compaction; the new code walks to the
  chain root, finds no match, and applies the same fallback.
- **Older compaction nodes inside the retained tail** are filtered out of the
  output, as before.

The only intentional behavioural difference is defensive: an empty entry list
with an unknown leaf used to panic on `entries[len(entries)-1]`; it now
returns `nil`.

## Verification

- Existing session tests (load/replay/manager) pass unchanged.
- New regression subtests in `TestBuildSessionContext` pin the compaction
  semantics: multi-round compaction (round 1's compaction and its history must
  not leak into the context), empty `FirstKeptEntryID`, and dangling
  `FirstKeptEntryID`.
- `TestBuildSessionContextMatchesReference` keeps the original implementation
  in the test file as an oracle and compares both against 2000 deterministic
  random branches (messages plus 0–3 compaction entries with empty, dangling,
  or real keep-from IDs). This is the strongest guarantee that the rewrite
  produces identical output.

## Benchmarks

Representative numbers, Apple M4, `-benchtime=300ms -benchmem`
(reproduce with `go test -run '^$' -bench BenchmarkBuildSessionContext
-benchmem ./internal/session/`):

| Scenario | Optimized | Reference (old) |
| --- | --- | --- |
| compactedHistory (~5000 summarized + 50 kept) | 1.6 µs, 2.5 KB, 3 alloc | 151 µs, 84 KB, 6 alloc |
| forkedShortBranch (10000 other + 30 current) | 0.8 µs, 1 KB, 2 alloc | 11 µs, 165 KB, 7 alloc |
| noCompaction (2000 messages) | 65 µs, 9 alloc | 70 µs, 14 alloc |

Two caveats keep the numbers honest:

- The 97× gap on `compactedHistory` is the scenario where the old code did the
  most dead work; short uncompacted sessions show only constant-factor
  differences.
- In absolute terms the whole call is tens of microseconds against LLM round
  trips of seconds. The practical value is not latency but complexity: work no
  longer grows with total session history, so very long, repeatedly compacted
  sessions stay cheap and predictable.
