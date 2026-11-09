package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/tui/controller"
)

type resultSubmitter struct {
	submit func(string)
}

func (resultSubmitter) IsBusy() bool         { return false }
func (s resultSubmitter) Submit(text string) { s.submit(text) }

func TestExtensionCommandResultReleasesBusyBeforeSubmit(t *testing.T) {
	for _, outcome := range []string{"submit", "error"} {
		t.Run(outcome, func(t *testing.T) {
			h := &ExtCommands{Bus: controller.NewBus(nil)}
			h.running.Store(true)
			var submitted string
			h.Submitter = resultSubmitter{submit: func(text string) {
				assert.False(t, h.Running(), "the normal submit gate must allow the returned prompt")
				submitted = text
			}}
			msg := controller.ExtCommandResultMsg{Submit: "from extension"}
			if outcome == "error" {
				msg.Err = "command failed"
			}
			h.Apply(msg)
			assert.False(t, h.Running())
			if outcome == "submit" {
				assert.Equal(t, msg.Submit, submitted)
			} else {
				assert.Empty(t, submitted)
			}
		})
	}
}

func TestExtensionCommandRemainsBusyUntilCompletionApplied(t *testing.T) {
	h := &ExtCommands{Bus: controller.NewBus(nil), Ctrl: &controller.EngineController{}}
	h.running.Store(true)
	h.run("missing", "")
	require.True(t, h.Running(), "worker completion must not release the UI submit gate")
	msgs := h.Bus.Drain()
	require.Len(t, msgs, 1)
	msg, ok := msgs[0].(controller.ExtCommandResultMsg)
	require.True(t, ok)
	require.NotEmpty(t, msg.Err)
	h.gen.Add(1)
	h.Apply(msg)
	assert.False(t, h.Running(), "even an outdated completion must release the gate")
}
