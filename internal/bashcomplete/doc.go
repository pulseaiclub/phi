// Package bashcomplete completes "!" command lines from local sources only:
// the project shell history, executables on PATH, and the project directory.
//
// Every source is a read that is already on disk, so a completion costs no
// round trip and needs no confidence gates: a row either literally completes
// the typed text or it does not appear. Sources are injected as functions, so
// the package owns choosing and ranking candidates while callers own where
// they come from.
package bashcomplete
