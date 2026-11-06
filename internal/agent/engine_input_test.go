package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

func TestLoopPreservesInputBeforePreparation(t *testing.T) {
	r := loadExt(t, `package main
import (
  "github.com/pulseaiclub/phi/ext/go"
  "github.com/pulseaiclub/phi/ext/go/phi"
)
func main() {
  m := phi.New("test", "0.0.1")
  m.OnUserInput(func(ev ext.UserInputEvent) *ext.UserInputResult {
    return &ext.UserInputResult{Text:ev.Text + "\nuser hook"}
  })
  m.OnBeforeAgentStart(func(ev ext.BeforeAgentStartEvent) *ext.BeforeAgentStartResult {
    return &ext.BeforeAgentStartResult{Prompt:ev.Prompt + "\nbefore hook", SystemPromptAppend:"extra"}
  })
  _ = m.Run()
}
`)
	s, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	e, err := NewEngine(llm.ModelConfig{Name: "test"}, s, WithExtensions(r))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	raw := "original prompt"
	// Stop after append so this exercises preparation without a model request.
	for _, err := range e.Loop(ctx, "expanded reference\n"+raw, LoopOpts{
		RawInput: &raw, PendingSkills: []string{"test-skill"}, OnPromptAppended: cancel,
	}) {
		require.NoError(t, err)
	}
	turns := s.UserTurns()
	require.Len(t, turns, 1)
	assert.Equal(t, raw, turns[0].UserInput())
	for _, injected := range []string{"expanded reference", "test-skill", "user hook", "before hook", "extra"} {
		assert.Contains(t, turns[0].Message.Content, injected)
	}
}
