// Package lsp is a lean Language Server Protocol client. It discovers language
// servers on PATH, spawns them lazily per workspace root, and answers
// definition, reference, hover and document-symbol questions.
//
// Everything is best-effort: a server that is missing, slow or broken produces
// an error, never a panic, and callers are expected to fall back to their own
// search. The package only depends on the standard library, never writes to
// disk and never talks to the network.
package lsp
