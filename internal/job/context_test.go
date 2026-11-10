package job_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/job"
)

func TestParseContextMode(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want job.ContextMode
	}{
		{name: "empty defaults clean", in: "", want: job.ContextModeClean},
		{name: "clean", in: "clean", want: job.ContextModeClean},
		{name: "handoff", in: "HANDOFF", want: job.ContextModeHandoff},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := job.ParseContextMode(tc.in)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseContextModeRejectsUnknownValue(t *testing.T) {
	_, err := job.ParseContextMode("fork")
	require.Error(t, err)
	assert.ErrorIs(t, err, job.ErrInvalid)
	assert.Contains(t, err.Error(), "clean|handoff")
}

func TestParentContextValidate(t *testing.T) {
	assert.NoError(t, (job.ParentContext{Mode: job.ContextModeClean}).Validate())
	assert.NoError(t, (job.ParentContext{Mode: job.ContextModeHandoff, Text: "parent notes"}).Validate())

	err := (job.ParentContext{Mode: "fork"}).Validate()
	require.Error(t, err)
	assert.ErrorIs(t, err, job.ErrInvalid)
}
