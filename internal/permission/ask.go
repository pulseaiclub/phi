package permission

import "context"

// AskResult is the user's response to an Ask decision.
type AskResult struct {
	Approved bool
	Feedback string // non-empty when user denied with guidance for the model
}

// AskHandler handles an approval prompt after Gate.Check returns Ask (after mode folding).
// Approved=false means deny; Feedback is forwarded to the model as the tool result.
type AskHandler interface {
	Ask(ctx context.Context, req Request, reason string) (AskResult, error)
}

// AskFunc adapts a function into an Ask handler.
type AskFunc func(ctx context.Context, req Request, reason string) (AskResult, error)

func (f AskFunc) Ask(ctx context.Context, req Request, reason string) (AskResult, error) {
	return f(ctx, req, reason)
}
