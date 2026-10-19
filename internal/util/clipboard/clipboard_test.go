package clipboard

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSelectPreferredImageMimeType(t *testing.T) {
	got := selectPreferredImageMimeType([]string{"text/plain", "image/webp", "image/png"})
	assert.Equal(t, "image/png", got)
}

func TestExtensionForImageMimeType(t *testing.T) {
	assert.Equal(t, "png", ExtensionForImageMimeType("image/png"))
	assert.Equal(t, "jpg", ExtensionForImageMimeType("image/jpeg; charset=binary"))
	assert.Empty(t, ExtensionForImageMimeType("image/bmp"))
}

func TestSplitLines(t *testing.T) {
	assert.Equal(t, []string{"image/png", "text/plain"}, splitLines("image/png\r\ntext/plain\n"))
}
