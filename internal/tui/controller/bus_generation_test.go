package controller

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/session"
)

func TestBusDoesNotCoalesceAcrossLoopGenerations(t *testing.T) {
	bus := NewBus(nil)
	old := SessionEventMsg{Gen: 1, Event: session.AssistantMessageUpdate{Message: session.Message{Text: "old"}}}
	current := SessionEventMsg{Gen: 2, Event: session.AssistantMessageUpdate{Message: session.Message{Text: "new"}}}
	bus.Publish(old)
	bus.Publish(LoopFinishedMsg{Gen: 1})
	bus.Publish(LoopStartedMsg{Gen: 2})
	bus.Publish(current)
	require.Equal(t, []Msg{old, LoopFinishedMsg{Gen: 1}, LoopStartedMsg{Gen: 2}, current}, bus.Drain())
}
