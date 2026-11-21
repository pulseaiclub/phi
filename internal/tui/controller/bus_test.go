package controller_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func TestBusOnWakeOnceUntilDrain(t *testing.T) {
	var wakes int
	b := controller.NewBus(func() { wakes++ })
	b.Publish(controller.JobProgressMsg{Progress: job.Progress{JobID: "j", ToolUseID: "t1", Status: "in-progress"}})
	b.Publish(controller.JobProgressMsg{Progress: job.Progress{JobID: "j", ToolUseID: "t1", Status: "done"}})
	b.Publish(controller.JobProgressMsg{Progress: job.Progress{JobID: "j", ToolUseID: "t2", Status: "done"}})
	require.Equal(t, 1, wakes, "wakes before Drain")
	batch := b.Drain()
	require.Len(t, batch, 2)
	b.Publish(controller.JobProgressMsg{Progress: job.Progress{JobID: "j", ToolUseID: "t3", Status: "done"}})
	require.Equal(t, 2, wakes, "wakes after re-arm")
}

func TestBusCoalesceNonAdjacentAssistant(t *testing.T) {
	var wakes int
	b := controller.NewBus(func() { wakes++ })
	b.Publish(controller.SessionEventMsg{Event: session.AssistantMessageUpdate{Message: session.Message{
		ID: "a1", Text: "one", State: session.StateStreaming,
	}}})
	b.Publish(controller.JobProgressMsg{Progress: job.Progress{JobID: "j", ToolUseID: "t1", Status: "in-progress"}})
	b.Publish(controller.SessionEventMsg{Event: session.AssistantMessageUpdate{Message: session.Message{
		ID: "a1", Text: "two", State: session.StateStreaming,
	}}})
	require.Equal(t, 1, wakes)
	batch := b.Drain()
	require.Len(t, batch, 2, "assistant coalesced across progress")
	te := batch[0].(controller.SessionEventMsg)
	upd := te.Event.(session.AssistantMessageUpdate)
	require.Equal(t, "two", upd.Message.Text)
}

func TestBusCoalesceJobProgressAcrossSession(t *testing.T) {
	b := controller.NewBus(nil)
	b.Publish(controller.JobProgressMsg{Progress: job.Progress{JobID: "j", ToolUseID: "t1", Status: "in-progress"}})
	b.Publish(controller.SessionEventMsg{Event: session.AssistantMessageUpdate{Message: session.Message{
		ID: "a1", Text: "x", State: session.StateStreaming,
	}}})
	b.Publish(controller.JobProgressMsg{Progress: job.Progress{JobID: "j", ToolUseID: "t1", Status: "done"}})
	batch := b.Drain()
	require.Len(t, batch, 2)
	jp := batch[0].(controller.JobProgressMsg)
	require.Equal(t, "done", jp.Progress.Status)
}

func TestBusDoesNotCoalesceAcrossLoopGenerations(t *testing.T) {
	bus := controller.NewBus(nil)
	old := controller.SessionEventMsg{
		Gen:   1,
		Event: session.AssistantMessageUpdate{Message: session.Message{Text: "old"}},
	}
	current := controller.SessionEventMsg{
		Gen:   2,
		Event: session.AssistantMessageUpdate{Message: session.Message{Text: "new"}},
	}
	bus.Publish(old)
	bus.Publish(controller.LoopFinishedMsg{Gen: 1})
	bus.Publish(controller.LoopStartedMsg{Gen: 2})
	bus.Publish(current)
	require.Equal(
		t,
		[]controller.Msg{old, controller.LoopFinishedMsg{Gen: 1}, controller.LoopStartedMsg{Gen: 2}, current},
		bus.Drain(),
	)
}

func TestRedrawRelay_BindAfterBus(t *testing.T) {
	relay := controller.NewRedrawRelay()
	bus := controller.NewBus(relay.Fire)
	var n int
	relay.Bind(func() { n++ })
	bus.Publish(controller.SubmitMsg{Text: "y"})
	assert.GreaterOrEqual(t, n, 1)

	// Drain so the next Publish can re-arm wake + Fire.
	_ = bus.Drain()
	bus.Publish(controller.SubmitMsg{Text: "z"})
	assert.GreaterOrEqual(t, n, 2)
}
