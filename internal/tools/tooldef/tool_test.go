package tooldef

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

type testInput struct {
	Path string `json:"path"`
	N    int    `json:"n"`
}

func TestNewToolTypedOptions(t *testing.T) {
	tool := NewTool(
		WithDefinition(llm.ToolDefinition{Name: "t"}),
		WithDetail(func(in testInput) string { return in.Path }),
		WithHandler(func(_ context.Context, in testInput) (Result, error) {
			return Result{Content: strconv.Itoa(in.N)}, nil
		}),
	)

	assert.Equal(t, "t", tool.Definition.Name)
	assert.Equal(t, "src", tool.DetailFromArgs([]byte(`{"path":"src"}`)))

	res, err := tool.Run(t.Context(), []byte(`{"n":3}`))
	require.NoError(t, err)
	assert.Equal(t, "3", res.Content)

	// Undecodable args fail Run and surface as the detail line.
	_, err = tool.Run(t.Context(), []byte(`nope`))
	require.Error(t, err)
	assert.NotEmpty(t, tool.DetailFromArgs([]byte(`nope`)))
}

func TestWithRunRawHandler(t *testing.T) {
	tool := NewTool(WithRun(func(_ context.Context, raw json.RawMessage) (Result, error) {
		return Result{Content: string(raw)}, nil
	}))

	res, err := tool.Run(t.Context(), json.RawMessage(`raw`))
	require.NoError(t, err)
	assert.Equal(t, "raw", res.Content)
}
