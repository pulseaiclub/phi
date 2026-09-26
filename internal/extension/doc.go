// Package extension loads PXB extension subprocesses and dispatches host
// events to them (Runner).
//
// The moving parts live in subpackages:
//
//	manifest — phi.yaml parsing and discovery under ~/.phi/extensions
//	proc     — one subprocess: handshake, RPC, event shims
//	plugin   — GitHub install / update / remove of extension directories
//	exttest  — test helper that builds a throwaway extension binary
//
// Each extension is a native binary that speaks the Phi eXtension Binary
// protocol (see package ext/pxb under ext/go/) over stdin/stdout. Authors use
// ext/phi.
//
// See doc/extensions.md.
package extension
