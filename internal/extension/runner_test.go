package extension

import (
	"testing"

	"github.com/stretchr/testify/assert"

	ext "github.com/pulseaiclub/phi/ext/go"
)

func TestToolFromDefReadable(t *testing.T) {
	got := toolFromDef(ext.Tool{Name: "read", Readable: true})
	assert.True(t, got.Definition.Readable)
	assert.Equal(t, "read", got.Definition.Name)
	assert.False(t, toolFromDef(ext.Tool{Name: "write"}).Definition.Readable)
}
