package job

import (
	"fmt"
	"strings"
)

// ContextMode selects how a child receives parent conversation context.
type ContextMode string

const (
	// ContextModeClean gives the child only its own task and system prompt.
	ContextModeClean ContextMode = "clean"
	// ContextModeHandoff gives the child a bounded parent context snapshot.
	ContextModeHandoff ContextMode = "handoff"
)

// ParseContextMode normalizes a context mode. Empty input uses the safe clean mode.
func ParseContextMode(s string) (ContextMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(ContextModeClean):
		return ContextModeClean, nil
	case string(ContextModeHandoff):
		return ContextModeHandoff, nil
	default:
		return "", fmt.Errorf("%w: unknown context mode %q (want clean|handoff)", ErrInvalid, s)
	}
}

// ParentContext is the context snapshot supplied to a child at spawn time.
type ParentContext struct {
	Mode ContextMode
	Text string
}

// Validate checks the context mode and treats an empty mode as clean.
func (c ParentContext) Validate() error {
	_, err := ParseContextMode(string(c.Mode))
	return err
}
