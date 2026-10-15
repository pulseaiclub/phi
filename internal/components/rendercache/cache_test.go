package rendercache

import (
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
)

func TestCache(t *testing.T) {
	var c Cache[string]
	calls := 0
	build := func() []components.Span {
		calls++
		return []components.Span{{Text: "hello world"}}
	}
	lines := c.Lines("first", 20, 0, build)
	require.Len(t, lines, 1)
	hit := c.Lines("first", 20, 0, build)
	assert.Same(t, &lines[0], &hit[0])
	assert.Equal(t, 1, calls)
	resized := c.Lines("first", 5, 0, build)
	assert.Len(t, resized, 3)
	assert.Equal(t, 1, calls)
	method := c.Lines("first", 5, xui.WidthMethod(1), build)
	assert.NotSame(t, &resized[0], &method[0])
	assert.Equal(t, 1, calls)
	c.Lines("second", 5, 0, build)
	assert.Equal(t, 2, calls)
	c.Clear()
	assert.Equal(t, Cache[string]{}, c)
	c.Lines("second", 5, 0, build)
	assert.Equal(t, 3, calls)
}

func TestEmptyAndNormalizedWidth(t *testing.T) {
	var c Cache[int]
	calls := 0
	build := func() []components.Span { calls++; return nil }
	first := c.Lines(0, 0, 0, build)
	require.Len(t, first, 1)
	second := c.Lines(0, -1, 0, build)
	assert.Same(t, &first[0], &second[0])
	assert.Equal(t, 1, calls)
}
