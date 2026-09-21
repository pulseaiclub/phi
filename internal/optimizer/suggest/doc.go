// Package suggest ranks recent shell commands as completions for typed text.
//
// It ports the judgement half of a shell-history autosuggester: code owns every
// exact rule (dedupe, literal prefix detection, ordering, thresholds), and one
// judge call answers the two questions that need judgement — which candidate the
// user is completing, and whether any candidate completes it at all. Asking both
// in one request keeps a keystroke down to a single round trip.
//
// A suggestion is advisory. This package never runs a command: the caller shows
// the suggestion and decides when to accept it.
package suggest
