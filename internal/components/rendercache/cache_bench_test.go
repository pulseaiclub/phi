package rendercache

import (
	"testing"

	"github.com/pulseaiclub/phi/internal/components"
)

func BenchmarkCacheHit(b *testing.B) {
	var cache Cache[string]
	build := func() []components.Span { return []components.Span{{Text: "hello 世界"}} }
	cache.Lines("body", 80, 0, build)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		cache.Lines("body", 80, 0, build)
	}
}
