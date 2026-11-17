package block

import (
	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/text"
)

// shellHighlightLimit bounds the memo; a full session can stream hundreds of
// distinct commands. On overflow the map resets wholesale — LRU bookkeeping
// would cost more than the occasional re-tokenise burst.
const shellHighlightLimit = 128

// shellHighlight memoises chroma spans per command. Transcript redraws (scroll,
// spinner ticks) re-run Draw constantly and tokenising a 2KB script costs ~2ms.
type shellHighlight struct {
	cache map[shellHighlightKey][]components.Span
}

type shellHighlightKey struct {
	cmd  string
	base xui.Style
	th   components.Theme
}

// spans returns highlighted spans for cmd, falling back to one base-styled
// span. Entries must not be mutated; callers only append around them.
func (h *shellHighlight) spans(cmd string, base xui.Style, th components.Theme) []components.Span {
	if cmd == "" {
		return nil
	}
	key := shellHighlightKey{cmd, base, th}
	if spans, ok := h.cache[key]; ok {
		return spans
	}
	spans := text.HighlightShell(cmd, base, th)
	if len(spans) == 0 {
		spans = []components.Span{{Text: cmd, Style: base}}
	}
	if h.cache == nil {
		h.cache = make(map[shellHighlightKey][]components.Span, shellHighlightLimit)
	} else if len(h.cache) >= shellHighlightLimit {
		clear(h.cache)
	}
	h.cache[key] = spans
	return spans
}
