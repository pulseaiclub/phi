package optimizer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNopLeavesCommandFlowUnchanged(t *testing.T) {
	state := CommandState{
		Task:     "run the focused tests",
		Command:  "go test ./internal/optimizer",
		CWD:      "/workspace",
		Purpose:  "verify the optimizer contract",
		Previous: []string{"go test ./internal/llm/jev"},
	}
	result := CommandResult{ExitCode: 0, Output: "ok"}

	var optimizer Optimizer = Nop{}
	before, err := optimizer.BeforeCommand(t.Context(), state)
	require.NoError(t, err)
	after, err := optimizer.AfterCommand(t.Context(), state, result)
	require.NoError(t, err)

	assert.Equal(t, Decision{}, before)
	assert.Equal(t, Decision{}, after)
	assert.Equal(t, DispositionUnchanged, Decision{}.Disposition)
}
