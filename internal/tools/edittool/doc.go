// Package edittool implements the edit tool: a sloppy anchored patch applied
// to one or more files. The payload syntax is parsed by package sloppy; this
// package holds the apply stage (matching and rewriting) and the tool wiring.
//
// # Apply stage
//
// Operations anchor against the original file text, so earlier edits never
// shift later anchors. All operations of a payload apply atomically: one
// failure and no file is written. A pattern is located by a matching ladder —
// byte-exact, then whitespace- and typography-tolerant, then fuzzy (same
// operator run, at most a few character edits) — and only a unique anchor is
// claimed unless the edit header carries " all". Failures name the fragment
// that missed, show the file region it nearly matches, and hand back a
// copy-ready payload to resend.
//
// # Deviations from the reference engine
//
//   - A "*** SM:PUT" replaces the whole FIND match even when the pattern
//     carries ⟪old│new⟫ selections; selections rewrite in place only when
//     there is no action header. Per-selection positional substitution is not
//     implemented.
//   - Parse-time recovery of garbled payloads (bare selections without the │
//     divider, unified-diff rewrites, ＋/－ marker lines) is rejected instead of
//     reinterpreted; package sloppy errors say so.
//   - A no-op is reported once per call; the reference escalates after three
//     identical no-ops per file and session.
package edittool
