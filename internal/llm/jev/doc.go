// Package jev talks to the TypeSafe AI System One API: one piece of state, a
// set of named questions, one answer per question.
//
// It ports the synchronous System One surface of the bundled Python SDK (see
// in this directory): POST /v1/systemone and the
// noul/choice/score question primitives. The async client, model listing,
// custom response models, and the SDK's own retry policy stay behind: retries
// reuse util.DoWithRetry, so transient 429/5xx responses and stale connections
// are retried the way the rest of phi's providers retry them, and per-call
// deadlines come from the context.
package jev
