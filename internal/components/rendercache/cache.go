// Package rendercache memoizes styled text and its layout for a single widget.
// Caches belong to the UI goroutine; they require no locks or global registry.
package rendercache

import (
	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
)

// Cache retains only the latest content and layout. Its zero value is ready to use.
// K must describe every input to the span builder, including theme and state.
// Returned lines and builder-provided spans are owned by the cache and must not
// be mutated. Paint them into a fresh Surface to keep selection edits isolated.
// A Cache must not be accessed concurrently.
type Cache[K comparable] struct {
	key    K
	valid  bool
	spans  []components.Span
	width  int
	method xui.WidthMethod
	lines  []components.RichLine
}

// Lines rebuilds spans only when key changes, and rewraps only when content,
// width or width method changes. build is called synchronously on a content miss.
func (c *Cache[K]) Lines(
	key K,
	width int,
	method xui.WidthMethod,
	build func() []components.Span,
) []components.RichLine {
	width = max(width, 1)
	if !c.valid || c.key != key {
		spans := build()
		// Discard the old layout before publishing new content.
		*c = Cache[K]{key: key, valid: true, spans: spans}
	}
	if c.lines == nil || c.width != width || c.method != method {
		c.lines = components.WrapSpans(c.spans, width, method)
		c.width, c.method = width, method
	}
	return c.lines
}

// Clear releases retained content and layout, including references in the key.
func (c *Cache[K]) Clear() { *c = Cache[K]{} }
