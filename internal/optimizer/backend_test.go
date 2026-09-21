package optimizer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm/jev"
)

type recordingBackend struct {
	state     any
	questions jev.Questions
	response  jev.Response
}

func (b *recordingBackend) Evaluate(_ context.Context, state any, questions jev.Questions) (jev.Response, error) {
	b.state = state
	b.questions = questions
	return b.response, nil
}

func TestBackendContractPreservesStructuredJevResponse(t *testing.T) {
	backend := &recordingBackend{
		response: jev.Response{
			Model:     "jev-latest",
			RequestID: "req-1",
			Answers: map[string]jev.Answer{
				"relevant": {Type: jev.AnswerNoul, Noul: 0.92},
			},
		},
	}
	state := map[string]any{"command": "go test ./..."}
	questions := jev.Questions{"relevant": jev.Noul{Instructions: "Is this relevant?"}}

	got, err := backend.Evaluate(t.Context(), state, questions)
	require.NoError(t, err)

	assert.Equal(t, state, backend.state)
	assert.Equal(t, questions, backend.questions)
	assert.Equal(t, "jev-latest", got.Model)
	assert.Equal(t, "req-1", got.RequestID)
	assert.InDelta(t, 0.92, got.Answers["relevant"].Noul, 0.0001)
}

func TestNewJevBackendRejectsNilClient(t *testing.T) {
	_, err := NewJevBackend(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Jev client is required")
}

func TestJevBackendRejectsMissingClient(t *testing.T) {
	var backend *JevBackend
	_, err := backend.Evaluate(t.Context(), "state", jev.Questions{"ok": jev.Noul{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Jev backend is not configured")
}
