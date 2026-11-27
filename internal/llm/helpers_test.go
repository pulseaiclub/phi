package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pulseaiclub/phi/internal/core"
)

func TestMarshalToolParams(t *testing.T) {
	assert.Equal(t, `{}`, string(MarshalToolParams(nil, "{}")))
	assert.JSONEq(t, `{"type":"object"}`, string(MarshalToolParams(nil, `{"type":"object"}`)))
	assert.Equal(t, "null", string(MarshalToolParams(nil, "null")))
	assert.Empty(t, string(MarshalToolParams(nil, "")), "an empty fallback leaves the field absent under omitempty")

	raw := MarshalToolParams(&core.FunctionParameters{
		Type:       "object",
		Properties: core.Object{"path": map[string]any{"type": "string"}},
		Required:   []string{"path"},
	}, "{}")
	assert.JSONEq(t, `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`, string(raw))

	// Required is omitted when empty, matching the schema keys the domain
	// types carried before the core split.
	assert.JSONEq(t, `{"type":"object","properties":{}}`, string(MarshalToolParams(
		&core.FunctionParameters{Type: "object", Properties: core.Object{}}, "{}",
	)))
}
