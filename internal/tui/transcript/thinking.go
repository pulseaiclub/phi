package transcript

import "github.com/pulseaiclub/phi/internal/components/block"

// SetThinkingExpanded applies the global choice now; later per-block toggles remain independent.
func (t *TranscriptPane) SetThinkingExpanded(expanded bool) {
	if t == nil || t.mapper == nil {
		return
	}
	t.mapper.thinkingDefault = expanded
	clear(t.mapper.thinkingExpanded)
	for _, entry := range t.list.Entries {
		if thinking, ok := entry.(*block.ThinkingBlock); ok {
			thinking.Expanded = expanded
		}
	}
	t.list.InvalidateHeights()
}
