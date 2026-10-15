package urls

import (
	"context"
	"fmt"
	"maps"
	"strings"
)

// ProtocolHandler locates local backing paths without reading their contents.
// Implementations must be safe for concurrent calls.
type ProtocolHandler interface {
	Locate(context.Context, InternalURL) (string, error)
}

// Router is an immutable registry. Its zero value has no handlers.
type Router struct {
	handlers map[string]ProtocolHandler
}

// NewRouter copies the registry; handler implementations remain caller-owned.
// Scheme keys must be lowercase without a trailing colon.
func NewRouter(handlers map[string]ProtocolHandler) *Router {
	copied := make(map[string]ProtocolHandler, len(handlers))
	maps.Copy(copied, handlers)
	return &Router{handlers: copied}
}

// Locate returns a local path for an internal URL. The caller must authorize
// that path before opening it; locating does not grant permission to read it.
func (r *Router) Locate(ctx context.Context, input string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	u, err := ParseInternalURL(input)
	if err != nil {
		return "", err
	}
	scheme := strings.TrimSuffix(u.Protocol, ":")
	handler := r.handlers[scheme]
	if handler == nil {
		return "", fmt.Errorf("unknown internal URL scheme %q", scheme)
	}
	return handler.Locate(ctx, u)
}
