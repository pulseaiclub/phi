package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMarshalToolParams(t *testing.T) {
	assert.Equal(t, `{}`, string(MarshalToolParams(nil, "{}")))
	assert.JSONEq(t, `{"type":"object"}`, string(MarshalToolParams(nil, `{"type":"object"}`)))
	raw := MarshalToolParams(&FunctionParameters{Type: "object", Properties: Object{}}, "{}")
	assert.Contains(t, string(raw), `"type":"object"`)
}
