package llm

import (
	"context"
	"iter"

	"github.com/pulseaiclub/phi/internal/core"
)

// CompactRequest is one summarization call: the prompt plus the output cap
// the provider may spend on it. MaxTokens <= 0 leaves the provider default.
type CompactRequest struct {
	Prompt    string
	MaxTokens int
}

// CompactResult is the model's summary. Truncated reports that the provider
// stopped at the output cap, so Text is a partial summary that must not be
// persisted as a session checkpoint.
type CompactResult struct {
	Text      string
	Truncated bool
}

// Compactor compresses conversation history into a concise summary.
type Compactor interface {
	Compact(ctx context.Context, req CompactRequest) (CompactResult, error)
}

// RequestInterceptor customizes a provider request after endpoint mapping and
// before the HTTP call.
type RequestInterceptor[Req any] interface {
	Before(ctx context.Context, req *Req, cfg core.ModelConfig) error
}

// ChatClient is the transport the agent loop drives a turn with: the builtin
// HTTP client or an extension-served model connection.
type ChatClient interface {
	Stream(ctx context.Context, messages []core.Message) iter.Seq2[core.StreamEvent, error]
	Compactor
}
