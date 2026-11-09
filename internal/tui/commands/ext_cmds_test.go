package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/tui/controller"
)

type resultSubmitter func(string)

func (resultSubmitter) IsBusy() bool         { return false }
func (s resultSubmitter) Submit(text string) { s(text) }

func TestExtensionCommandResultReleasesBusyBeforeSubmit(t *testing.T) {
	h := &ExtCommands{Bus: controller.NewBus(nil)}
	h.running = true
	var submitted string
	h.Submitter = resultSubmitter(func(text string) {
		assert.False(t, h.Running(), "the normal submit gate must allow the returned prompt")
		submitted = text
	})
	h.Apply(controller.ExtCommandResultMsg{Submit: "from extension"})
	assert.Equal(t, "from extension", submitted)
}

func TestExtensionCommandRemainsBusyUntilCompletionApplied(t *testing.T) {
	for _, generation := range []uint64{0, 1} {
		h := &ExtCommands{Bus: controller.NewBus(nil), Ctrl: &controller.EngineController{}}
		h.running = true
		h.run("missing", "")
		require.True(t, h.Running(), "worker completion must not release the UI submit gate")
		msgs := h.Bus.Drain()
		require.Len(t, msgs, 1)
		msg, ok := msgs[0].(controller.ExtCommandResultMsg)
		require.True(t, ok)
		require.NotEmpty(t, msg.Err)
		msg.Submit = "ignored"
		h.Submitter = resultSubmitter(func(string) { assert.Fail(t, "failed or stale results must not submit") })
		h.gen.Store(generation)
		h.Apply(msg)
		assert.False(t, h.Running(), "even an outdated completion must release the gate")
	}
}
