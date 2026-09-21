package optimizer

import (
	"context"
	"errors"

	"github.com/pulseaiclub/phi/internal/llm/jev"
)

// Backend evaluates structured questions about a state.
//
// Optimizers depend on this narrow interface instead of a concrete model
// client, so tests and future decision providers can be injected without
// changing optimization policy.
type Backend interface {
	Evaluate(context.Context, any, jev.Questions) (jev.Response, error)
}

// JevBackend adapts the TypeSafe System One client to Backend.
type JevBackend struct {
	client *jev.Client
}

// NewJevBackend creates a Backend backed by a TypeSafe System One client.
func NewJevBackend(client *jev.Client) (*JevBackend, error) {
	if client == nil {
		return nil, errors.New("optimizer: Jev client is required")
	}
	return &JevBackend{client: client}, nil
}

// Evaluate forwards the state and typed questions to TypeSafe Jev.
func (b *JevBackend) Evaluate(ctx context.Context, state any, questions jev.Questions) (jev.Response, error) {
	if b == nil || b.client == nil {
		return jev.Response{}, errors.New("optimizer: Jev backend is not configured")
	}
	return b.client.SystemOne(ctx, state, questions)
}
